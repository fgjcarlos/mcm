package diagnostics

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

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
	defer conn.Close()

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
