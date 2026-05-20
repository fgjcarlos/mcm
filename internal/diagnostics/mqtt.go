package diagnostics

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

const defaultMQTTDialTimeout = 5 * time.Second

// MQTTResult describes the outcome of a Mosquitto MQTT connectivity check.
type MQTTResult struct {
	Address string
	OK      bool
	Message string
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
			Message: fmt.Sprintf("Mosquitto is unreachable at %s: %v", address, err),
		}
	}

	return MQTTResult{
		Address: address,
		OK:      true,
		Message: fmt.Sprintf("Mosquitto is reachable at %s", address),
	}
}

func mqttConnect(ctx context.Context, cfg config.MosquittoConfig, timeout time.Duration) error {
	address := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()

	if cfg.TLS.Enabled {
		serverName := cfg.Host
		if net.ParseIP(serverName) != nil {
			serverName = ""
		}

		tlsConn := tls.Client(conn, &tls.Config{ //nolint:gosec // User-controlled diagnostic option for local/self-signed brokers.
			ServerName:         serverName,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("TLS handshake failed: %w", err)
		}
		conn = tlsConn
	}

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(timeout)
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set MQTT connection deadline: %w", err)
	}

	packet, err := buildMQTTConnectPacket(cfg)
	if err != nil {
		return err
	}

	if _, err := conn.Write(packet); err != nil {
		return fmt.Errorf("send MQTT CONNECT: %w", err)
	}

	connack := make([]byte, 4)
	if _, err := io.ReadFull(conn, connack); err != nil {
		return fmt.Errorf("read MQTT CONNACK: %w", err)
	}
	if connack[0] != 0x20 || connack[1] != 0x02 {
		return fmt.Errorf("unexpected MQTT CONNACK header % x", connack[:2])
	}
	if connack[3] != 0x00 {
		return fmt.Errorf("broker refused MQTT connection with return code %d", connack[3])
	}

	return nil
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
