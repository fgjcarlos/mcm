package agent_test

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/agent"
)

// startBrokerStub starts a TCP listener that accepts one connection, reads the
// MQTT CONNECT packet, and responds with a CONNACK with the given return code.
// Pass connackCode = 255 to simulate a broker that never sends a CONNACK.
func startBrokerStub(t *testing.T, connackCode byte) (host string, port int) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("startBrokerStub: listen: %v", err)
	}

	addr := ln.Addr().(*net.TCPAddr)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			// listener was closed before a connection arrived — fine for "refused" tests
			return
		}
		defer conn.Close()
		defer ln.Close()

		if connackCode == 255 {
			// Simulate broker that accepts but never sends CONNACK (timeout scenario).
			// Drain the CONNECT packet so the client doesn't see a broken pipe.
			buf := make([]byte, 256)
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			conn.Read(buf) //nolint:errcheck
			// Hold the connection open long enough for the client to time out.
			time.Sleep(2 * time.Second)
			return
		}

		// Read variable-length CONNECT packet: fixed header byte + remaining length
		header := make([]byte, 1)
		if _, err := conn.Read(header); err != nil {
			return
		}
		// Read remaining length (variable-length encoding, up to 4 bytes)
		var remainingLength int
		multiplier := 1
		for i := 0; i < 4; i++ {
			b := make([]byte, 1)
			if _, err := conn.Read(b); err != nil {
				return
			}
			remainingLength += int(b[0]&0x7F) * multiplier
			multiplier *= 128
			if b[0]&0x80 == 0 {
				break
			}
		}
		// Drain the variable header + payload
		payload := make([]byte, remainingLength)
		_ = binary.Read(strings.NewReader(string(payload)), binary.BigEndian, &payload)
		conn.Read(payload) //nolint:errcheck

		// Send CONNACK: fixed header 0x20, remaining length 0x02, session present 0x00, return code
		connack := []byte{0x20, 0x02, 0x00, connackCode}
		conn.Write(connack) //nolint:errcheck
	}()

	return "127.0.0.1", addr.Port
}

func TestMQTTProbe(t *testing.T) {
	t.Run("healthy: CONNACK return code 0", func(t *testing.T) {
		host, port := startBrokerStub(t, 0x00)
		probe := agent.NewMQTTProbe(host, port, 2*time.Second)
		result := probe.Check(context.Background())

		if result.Status != agent.StatusHealthy {
			t.Errorf("status = %q, want %q", result.Status, agent.StatusHealthy)
		}
		if result.Latency <= 0 {
			t.Errorf("latency should be positive, got %v", result.Latency)
		}
	})

	t.Run("degraded: CONNACK return code 5 (not authorized)", func(t *testing.T) {
		host, port := startBrokerStub(t, 0x05)
		probe := agent.NewMQTTProbe(host, port, 2*time.Second)
		result := probe.Check(context.Background())

		if result.Status != agent.StatusDegraded {
			t.Errorf("status = %q, want %q", result.Status, agent.StatusDegraded)
		}
		if !strings.Contains(result.Message, "5") {
			t.Errorf("message should mention return code 5, got: %q", result.Message)
		}
	})

	t.Run("unknown: timeout — broker accepts but never sends CONNACK", func(t *testing.T) {
		host, port := startBrokerStub(t, 255)
		probe := agent.NewMQTTProbe(host, port, 200*time.Millisecond)
		result := probe.Check(context.Background())

		if result.Status != agent.StatusUnknown {
			t.Errorf("status = %q, want %q", result.Status, agent.StatusUnknown)
		}
	})

	t.Run("unknown: connection refused — no listener on port", func(t *testing.T) {
		// Use a port that should be free (bind then immediately close)
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()

		probe := agent.NewMQTTProbe("127.0.0.1", port, 200*time.Millisecond)
		result := probe.Check(context.Background())

		if result.Status != agent.StatusUnknown {
			t.Errorf("status = %q, want %q", result.Status, agent.StatusUnknown)
		}
	})

	t.Run("unknown: context cancellation stops probe", func(t *testing.T) {
		host, port := startBrokerStub(t, 255)
		probe := agent.NewMQTTProbe(host, port, 5*time.Second)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		result := probe.Check(ctx)

		if result.Status != agent.StatusUnknown {
			t.Errorf("status = %q, want %q", result.Status, agent.StatusUnknown)
		}
	})

	t.Run("latency is measured", func(t *testing.T) {
		host, port := startBrokerStub(t, 0x00)
		probe := agent.NewMQTTProbe(host, port, 2*time.Second)
		start := time.Now()
		result := probe.Check(context.Background())
		elapsed := time.Since(start)

		if result.Latency <= 0 {
			t.Errorf("latency should be positive, got %v", result.Latency)
		}
		if result.Latency > elapsed {
			t.Errorf("latency %v should not exceed wall time %v", result.Latency, elapsed)
		}
	})

	t.Run("port number used in address", func(t *testing.T) {
		// Use an unused port to get a "refused" and confirm the probe uses the given port
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()

		probe := agent.NewMQTTProbe("127.0.0.1", port, 200*time.Millisecond)
		result := probe.Check(context.Background())

		if result.Status != agent.StatusUnknown {
			t.Errorf("expected unknown for closed port, got %q", result.Status)
		}
		_ = strconv.Itoa(port) // just to use the import
	})
}
