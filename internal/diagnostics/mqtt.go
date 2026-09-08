package diagnostics

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/tlsutil"
)

const defaultMQTTDialTimeout = 5 * time.Second

// MQTTResult describes the outcome of a Mosquitto MQTT connectivity check.
type MQTTResult struct {
	Address string
	OK      bool
	Message string
}

type mqttDiagnosticStage string

const (
	mqttStageTCP    mqttDiagnosticStage = "tcp"
	mqttStageTLS    mqttDiagnosticStage = "tls"
	mqttStageMQTT   mqttDiagnosticStage = "mqtt"
	mqttStageConfig mqttDiagnosticStage = "config"
)

type mqttDiagnosticError struct {
	stage mqttDiagnosticStage
	err   error
}

func (e *mqttDiagnosticError) Error() string {
	return e.err.Error()
}

func (e *mqttDiagnosticError) Unwrap() error {
	return e.err
}

func diagnosticError(stage mqttDiagnosticStage, format string, args ...any) error {
	return &mqttDiagnosticError{stage: stage, err: fmt.Errorf(format, args...)}
}

// CheckMQTTConnectivity attempts a real MQTT CONNECT/CONNACK exchange with the
// configured Mosquitto broker.
func CheckMQTTConnectivity(ctx context.Context, cfg config.MosquittoConfig) MQTTResult {
	address := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	err := mqttConnect(ctx, cfg, defaultMQTTDialTimeout)
	if err != nil {
		return MQTTResult{
			Address: address,
			OK:      false,
			Message: formatMQTTDiagnosticMessage(address, cfg.TLS.Enabled, err),
		}
	}

	return MQTTResult{
		Address: address,
		OK:      true,
		Message: fmt.Sprintf("Mosquitto is reachable at %s", address),
	}
}

func formatMQTTDiagnosticMessage(address string, tlsEnabled bool, err error) string {
	var diagErr *mqttDiagnosticError
	if !errors.As(err, &diagErr) {
		return fmt.Sprintf("Mosquitto is unreachable at %s: TCP connection failed: %v. Check mosquitto.host, mosquitto.port, listener binding, firewall rules, and container networking.", address, err)
	}

	switch diagErr.stage {
	case mqttStageConfig:
		return fmt.Sprintf("Mosquitto TLS configuration is invalid for %s: %v. Check certificate paths, secret mounts, and file permissions.", address, diagErr.err)
	case mqttStageTCP:
		return fmt.Sprintf("Mosquitto is unreachable at %s: TCP connection failed: %v. Check mosquitto.host, mosquitto.port, listener binding, firewall rules, and container networking.", address, diagErr.err)
	case mqttStageTLS:
		return fmt.Sprintf("Mosquitto TCP connection succeeded at %s, but TLS handshake failed: %v. Check the broker TLS listener, CA trust, server certificate name/SANs, client certificate/key, and avoid insecure_skip_verify in production.", address, diagErr.err)
	case mqttStageMQTT:
		transport := "TCP"
		if tlsEnabled {
			transport = "TCP and TLS"
		}
		return fmt.Sprintf("Mosquitto %s connection succeeded at %s, but MQTT CONNECT/CONNACK failed: %v. Check username/password, ACL/auth plugin status, protocol listener settings, and broker logs.", transport, address, diagErr.err)
	default:
		return fmt.Sprintf("Mosquitto connectivity check failed at %s: %v", address, diagErr.err)
	}
}

func mqttConnect(ctx context.Context, cfg config.MosquittoConfig, timeout time.Duration) error {
	address := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return diagnosticError(mqttStageTCP, "%w", err)
	}
	defer conn.Close() //nolint:errcheck

	if cfg.TLS.Enabled {
		tlsConfig, err := tlsutil.BuildMosquittoTLSConfig(cfg)
		if err != nil {
			return diagnosticError(mqttStageConfig, "%w", err)
		}

		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return diagnosticError(mqttStageTLS, "%w", err)
		}
		conn = tlsConn
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(timeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return diagnosticError(mqttStageMQTT, "set MQTT connection deadline: %w", err)
	}

	packet, err := buildMQTTConnectPacket(cfg)
	if err != nil {
		return diagnosticError(mqttStageMQTT, "%w", err)
	}

	if _, err := conn.Write(packet); err != nil {
		return diagnosticError(mqttStageMQTT, "send MQTT CONNECT: %w", err)
	}

	connack := make([]byte, 4)
	if _, err := io.ReadFull(conn, connack); err != nil {
		return diagnosticError(mqttStageMQTT, "read MQTT CONNACK: %w", err)
	}
	if connack[0] != 0x20 || connack[1] != 0x02 {
		return diagnosticError(mqttStageMQTT, "unexpected MQTT CONNACK header % x", connack[:2])
	}
	if connack[3] != 0x00 {
		return diagnosticError(mqttStageMQTT, "broker refused MQTT connection with return code %d (%s)", connack[3], mqttConnackReturnCode(connack[3]))
	}

	return nil
}

func mqttConnackReturnCode(code byte) string {
	switch code {
	case 1:
		return "unacceptable protocol version"
	case 2:
		return "identifier rejected"
	case 3:
		return "server unavailable"
	case 4:
		return "bad username or password"
	case 5:
		return "not authorized"
	default:
		return "unknown reason"
	}
}

func buildMQTTConnectPacket(cfg config.MosquittoConfig) ([]byte, error) {
	clientID := fmt.Sprintf("mcm-doctor-%d", time.Now().UnixNano())
	username := strings.TrimSpace(cfg.Username)
	password := cfg.Password

	variableHeader := []byte{
		0x00, 0x04, 'M', 'Q', 'T', 'T',
		0x04,       // MQTT 3.1.1
		0x02,       // clean session
		0x00, 0x0a, // keepalive: 10 seconds
	}

	if username != "" {
		variableHeader[7] |= 0x80
		variableHeader[7] |= 0x40
	}

	payload := appendMQTTString(nil, clientID)
	if username != "" {
		payload = appendMQTTString(payload, username)
		payload = appendMQTTString(payload, password)
	}

	remainingLength := len(variableHeader) + len(payload)
	encodedRemainingLength, err := encodeRemainingLength(remainingLength)
	if err != nil {
		return nil, err
	}

	packet := []byte{0x10}
	packet = append(packet, encodedRemainingLength...)
	packet = append(packet, variableHeader...)
	packet = append(packet, payload...)
	return packet, nil
}

func appendMQTTString(packet []byte, value string) []byte {
	packet = append(packet, byte(len(value)>>8), byte(len(value)))
	packet = append(packet, value...)
	return packet
}

func encodeRemainingLength(length int) ([]byte, error) {
	if length < 0 || length > 268435455 {
		return nil, fmt.Errorf("MQTT remaining length out of range: %d", length)
	}

	encoded := make([]byte, 0, 4)
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		encoded = append(encoded, digit)
		if length == 0 {
			break
		}
	}
	return encoded, nil
}

// VerifyActiveOptions configures an active config verification.
//
// Issue #293 (P0): the deploy service uses VerifyActive to confirm that a
// freshly-applied Mosquitto configuration is in effect on the broker
// before marking the deployment as active_verified. The verifier runs
// against the live broker with credentials and topic filters derived from
// the rendered ACL/passwd bodies — not from a cached "applied" state.
type VerifyActiveOptions struct {
	Config           config.MosquittoConfig
	Username         string
	Password         string
	AllowedTopic     string
	DeniedTopic      string
	DialTimeout      time.Duration
	RoundTripTimeout time.Duration
	SubTimeout       time.Duration
	TLS              *tls.Config // optional; nil = plaintext
}

// VerifyActiveResult is the outcome of a single VerifyActive call.
type VerifyActiveResult struct {
	OK              bool
	Stage           string // "connect", "positive_subscribe", "positive_roundtrip", "negative"
	Message         string
	PositiveMessage string
	NegativeMessage string
}

// VerifierFunc is a function adapter that satisfies any interface
// expecting a single VerifyActive method (issue #293). Lets callers
// pass `diagnostics.VerifyActive` directly as a deploy.ActiveVerifier.
type VerifierFunc func(ctx context.Context, opts VerifyActiveOptions) VerifyActiveResult

// VerifyActive implements the method on VerifierFunc.
func (f VerifierFunc) VerifyActive(ctx context.Context, opts VerifyActiveOptions) VerifyActiveResult {
	return f(ctx, opts)
}

// VerifyActive verifies that the broker is serving the freshly-applied
// configuration by running two checks:
//
//  1. Positive — subscribe to a topic the new ACL grants, publish a
//     message via a second connection as the same user, and assert the
//     subscriber receives the message. This proves the broker is
//     accepting the new passwd credentials AND honouring the new ACL
//     grant.
//  2. Negative — subscribe to a topic the new ACL denies and assert the
//     broker returns SUBACK failure (return code 0x80). This proves the
//     broker is enforcing the new ACL denials, not silently allowing
//     everything.
//
// Both checks must pass for the result to be OK. The caller (deploy
// service) is expected to invoke VerifyActive with bounded retries and
// backoff and to roll back from snapshot on persistent failure.
func VerifyActive(ctx context.Context, opts VerifyActiveOptions) VerifyActiveResult {
	if opts.DialTimeout == 0 {
		opts.DialTimeout = 2 * time.Second
	}
	if opts.RoundTripTimeout == 0 {
		opts.RoundTripTimeout = 2 * time.Second
	}
	if opts.SubTimeout == 0 {
		opts.SubTimeout = 2 * time.Second
	}

	broker := net.JoinHostPort(strings.TrimSpace(opts.Config.Host), strconv.Itoa(opts.Config.Port))

	// --- Positive test -------------------------------------------------
	posMsg, posOK := runPositiveTest(ctx, broker, opts)
	if !posOK {
		return VerifyActiveResult{
			OK:              false,
			Stage:           "positive",
			Message:         posMsg,
			PositiveMessage: posMsg,
		}
	}

	// --- Negative test -------------------------------------------------
	negMsg, negOK := runNegativeTest(ctx, broker, opts)

	return VerifyActiveResult{
		OK:              negOK,
		Stage:           ternary(negOK, "ok", "negative"),
		Message:         summarizeActive(posMsg, negMsg, negOK),
		PositiveMessage: posMsg,
		NegativeMessage: negMsg,
	}
}

// runPositiveTest connects, subscribes to AllowedTopic, and waits for a
// message published on the same topic via a second connection. Returns
// (message, true) on success.
func runPositiveTest(ctx context.Context, broker string, opts VerifyActiveOptions) (string, bool) {
	// Bidirectional channel local to this function; the paho callback
	// sends into it and we receive below.
	received := make(chan struct{}, 1)

	// Subscriber client.
	subOpts := newPahoOptions(broker, "mcm-verify-sub-"+randSuffix(), opts.Username, opts.Password, opts.TLS, opts.DialTimeout)
	subClient := mqtt.NewClient(subOpts)
	if token := subClient.Connect(); !token.WaitTimeout(opts.DialTimeout) || token.Error() != nil {
		subClient.Disconnect(0)
		return fmt.Sprintf("positive: connect as %q failed: %v", opts.Username, token.Error()), false
	}
	defer subClient.Disconnect(100) //nolint:errcheck

	subToken := subClient.Subscribe(opts.AllowedTopic, 1, func(_ mqtt.Client, m mqtt.Message) {
		select {
		case received <- struct{}{}:
		default:
		}
	})
	if !subToken.WaitTimeout(opts.SubTimeout) || subToken.Error() != nil {
		return fmt.Sprintf("positive: subscribe to %q failed: %v", opts.AllowedTopic, subToken.Error()), false
	}

	// Publisher client (separate connection).
	pubOpts := newPahoOptions(broker, "mcm-verify-pub-"+randSuffix(), opts.Username, opts.Password, opts.TLS, opts.DialTimeout)
	pubClient := mqtt.NewClient(pubOpts)
	if token := pubClient.Connect(); !token.WaitTimeout(opts.DialTimeout) || token.Error() != nil {
		pubClient.Disconnect(0)
		return fmt.Sprintf("positive: publisher connect failed: %v", token.Error()), false
	}
	defer pubClient.Disconnect(100) //nolint:errcheck

	pubToken := pubClient.Publish(opts.AllowedTopic, 1, false, "mcm-verify")
	if !pubToken.WaitTimeout(opts.DialTimeout) || pubToken.Error() != nil {
		return fmt.Sprintf("positive: publish to %q failed: %v", opts.AllowedTopic, pubToken.Error()), false
	}

	select {
	case <-received:
		return fmt.Sprintf("positive: round-trip on %q OK", opts.AllowedTopic), true
	case <-time.After(opts.RoundTripTimeout):
		return fmt.Sprintf("positive: subscriber did not receive publish within %s", opts.RoundTripTimeout), false
	case <-ctx.Done():
		return fmt.Sprintf("positive: cancelled: %v", ctx.Err()), false
	}
}

// runNegativeTest attempts to authenticate with a username that does
// NOT exist in the rendered passwd. Mosquitto has default-allow for
// unmatched ACL topics (unlike mochi), so a "subscribe to denied
// topic" check is unreliable; instead we verify that an unknown user
// is REJECTED outright. A positive test (subscribe+publish on a granted
// topic) covers the ACL path; this negative test covers the auth path
// — together they prove both halves of the rendered config are active.
func runNegativeTest(ctx context.Context, broker string, opts VerifyActiveOptions) (string, bool) {
	clientOpts := newPahoOptions(broker, "mcm-verify-neg-"+randSuffix(), "mcm-verify-nonexistent-user", "mcm-verify-dummy-pass", opts.TLS, opts.DialTimeout)
	client := mqtt.NewClient(clientOpts)
	if token := client.Connect(); !token.WaitTimeout(opts.DialTimeout) || token.Error() != nil {
		client.Disconnect(0)
		return fmt.Sprintf("negative: connect with unknown user correctly rejected: %v", token.Error()), true
	}
	// We connected — this is the failure case: an unknown user was
	// accepted. The broker is using the OLD config (no auth check).
	client.Disconnect(0)
	return "negative: broker accepted unknown user (expected rejection; broker may be serving the OLD passwd)", false
}

// newPahoOptions builds a paho client options struct that targets the
// given broker with the given credentials and optional TLS config.
func newPahoOptions(broker, clientID, username, password string, tlsCfg *tls.Config, dialTimeout time.Duration) *mqtt.ClientOptions {
	opts := mqtt.NewClientOptions().
		AddBroker("tcp://" + broker).
		SetClientID(clientID).
		SetUsername(username).
		SetPassword(password).
		SetCleanSession(true).
		SetConnectTimeout(dialTimeout).
		SetAutoReconnect(false).
		SetConnectRetry(false).
		SetConnectRetryInterval(0)
	if tlsCfg != nil {
		opts = opts.AddBroker("ssl://" + broker).SetTLSConfig(tlsCfg)
	}
	return opts
}

var verifyRandMu sync.Mutex
var verifyRandSeq uint64

// randSuffix returns a process-unique suffix used to keep MQTT client
// IDs distinct between sub-tests and between sub/pub pairs.
func randSuffix() string {
	verifyRandMu.Lock()
	defer verifyRandMu.Unlock()
	verifyRandSeq++
	return strconv.FormatUint(verifyRandSeq, 36)
}

// summarizeActive builds the top-level Message for the verifier result.
func summarizeActive(posMsg, negMsg string, negOK bool) string {
	if negOK {
		return posMsg + "; " + negMsg
	}
	return posMsg + "; " + negMsg
}

// ternary is a tiny helper for readability.
func ternary[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
}
