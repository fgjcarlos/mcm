package agent

import (
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// BrokerStatus describes the health state of the local MQTT broker.
type BrokerStatus string

const (
	// StatusHealthy means the broker accepted the MQTT connection (CONNACK 0).
	StatusHealthy BrokerStatus = "healthy"
	// StatusDegraded means the broker responded but refused the connection.
	StatusDegraded BrokerStatus = "degraded"
	// StatusUnknown means the broker could not be reached or did not respond.
	StatusUnknown BrokerStatus = "unknown"
)

// ProbeResult holds the outcome of a single broker health check.
type ProbeResult struct {
	Status  BrokerStatus
	Message string
	Latency time.Duration
}

// MQTTProbe performs a raw MQTT CONNECT/CONNACK health check against a broker.
// It does not use an MQTT client library — the exchange is done over a plain TCP
// connection to avoid introducing additional dependencies.
type MQTTProbe struct {
	host    string
	port    int
	timeout time.Duration
}

// NewMQTTProbe creates a new probe targeting the given host/port with the given timeout.
func NewMQTTProbe(host string, port int, timeout time.Duration) *MQTTProbe {
	return &MQTTProbe{host: host, port: port, timeout: timeout}
}

// Check dials the broker, sends a minimal MQTT CONNECT packet, reads the
// CONNACK, and returns a ProbeResult with measured latency.
func (p *MQTTProbe) Check(ctx context.Context) ProbeResult {
	start := time.Now()

	address := net.JoinHostPort(p.host, fmt.Sprintf("%d", p.port))

	dialer := net.Dialer{Timeout: p.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return ProbeResult{
			Status:  StatusUnknown,
			Message: fmt.Sprintf("could not connect to broker at %s: %v", address, err),
			Latency: time.Since(start),
		}
	}
	defer conn.Close()

	// Set a deadline for the full CONNECT/CONNACK exchange.
	deadline := time.Now().Add(p.timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return ProbeResult{
			Status:  StatusUnknown,
			Message: fmt.Sprintf("set connection deadline: %v", err),
			Latency: time.Since(start),
		}
	}

	packet := buildProbeConnectPacket()
	if _, err := conn.Write(packet); err != nil {
		return ProbeResult{
			Status:  StatusUnknown,
			Message: fmt.Sprintf("send MQTT CONNECT: %v", err),
			Latency: time.Since(start),
		}
	}

	// Read exactly 4 bytes: fixed header (0x20), remaining length (0x02),
	// session present flag, return code.
	connack := make([]byte, 4)
	if _, err := io.ReadFull(conn, connack); err != nil {
		return ProbeResult{
			Status:  StatusUnknown,
			Message: fmt.Sprintf("read MQTT CONNACK: %v", err),
			Latency: time.Since(start),
		}
	}

	latency := time.Since(start)

	if connack[0] != 0x20 || connack[1] != 0x02 {
		return ProbeResult{
			Status:  StatusUnknown,
			Message: fmt.Sprintf("unexpected CONNACK header: % x", connack[:2]),
			Latency: latency,
		}
	}

	returnCode := connack[3]
	if returnCode == 0x00 {
		return ProbeResult{
			Status:  StatusHealthy,
			Message: fmt.Sprintf("broker at %s is healthy", address),
			Latency: latency,
		}
	}

	return ProbeResult{
		Status:  StatusDegraded,
		Message: fmt.Sprintf("broker refused connection with return code %d (%s)", returnCode, mqttReturnCodeText(returnCode)),
		Latency: latency,
	}
}

// buildProbeConnectPacket builds a minimal MQTT 3.1.1 CONNECT packet with
// clean session set and no authentication.
func buildProbeConnectPacket() []byte {
	clientID := "mcm-probe"

	variableHeader := []byte{
		0x00, 0x04, 'M', 'Q', 'T', 'T', // protocol name
		0x04,       // protocol level: MQTT 3.1.1
		0x02,       // connect flags: clean session
		0x00, 0x0a, // keepalive: 10 seconds
	}

	payload := appendMQTTString(nil, clientID)

	remainingLength := len(variableHeader) + len(payload)

	packet := []byte{0x10} // CONNECT fixed header
	packet = append(packet, encodeVarLen(remainingLength)...)
	packet = append(packet, variableHeader...)
	packet = append(packet, payload...)
	return packet
}

// appendMQTTString appends a length-prefixed UTF-8 string to the packet.
func appendMQTTString(packet []byte, s string) []byte {
	packet = append(packet, byte(len(s)>>8), byte(len(s)))
	packet = append(packet, s...)
	return packet
}

// encodeVarLen encodes an integer using the MQTT variable-length encoding.
func encodeVarLen(length int) []byte {
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
	return encoded
}

// mqttReturnCodeText returns a human-readable description of a CONNACK return code.
func mqttReturnCodeText(code byte) string {
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
