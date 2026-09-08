package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/tlsutil"
)

// PositiveOK reports whether the positive test (subscribe+publish round
// trip) succeeded. Kept as a method to mirror how callers inspect the
// verifier result.
func (r VerifyActiveResult) PositiveOK() bool {
	return r.OK || r.PositiveMessage != "" && (r.Stage == "ok" || r.Stage == "negative" || r.Stage == "positive")
}

// TestVerifyActive covers issue #293 acceptance criteria 2-4. Each
// scenario runs against an in-process mochi MQTT broker with explicit
// per-user ACLs so we can prove the verifier detects "broker reachable
// but serving the OLD configuration".
func TestVerifyActive(t *testing.T) {
	t.Parallel()

	t.Run("positive and negative both pass when broker serves new config", func(t *testing.T) {
		t.Parallel()
		_, addr := startVerifyBroker(t, verifyBrokerACL{
			users: map[string]verifyUserACL{
				"alice": {
					allowed: []string{"sensors/temperature", "sensors/humidity"},
					denied:  []string{"sensors/forbidden"},
				},
			},
		})
		host, port := splitHostPort(t, addr)

		opts := VerifyActiveOptions{
			Config:           cfgWithCreds(host, port, "", ""),
			Username:         "alice",
			Password:         "test-pass-alice",
			AllowedTopic:     "sensors/temperature",
			DeniedTopic:      "sensors/forbidden",
			DialTimeout:      2 * time.Second,
			RoundTripTimeout: 2 * time.Second,
			SubTimeout:       2 * time.Second,
		}
		result := VerifyActive(context.Background(), opts)
		if !result.OK {
			t.Fatalf("VerifyActive OK=false, want true; stage=%s message=%s positive=%q negative=%q",
				result.Stage, result.Message, result.PositiveMessage, result.NegativeMessage)
		}
		if !strings.Contains(result.PositiveMessage, "OK") {
			t.Errorf("PositiveMessage missing 'OK': %s", result.PositiveMessage)
		}
		if !strings.Contains(result.NegativeMessage, "rejected") {
			t.Errorf("NegativeMessage missing 'rejected': %s", result.NegativeMessage)
		}
	})

	t.Run("positive fails when broker still serves old config", func(t *testing.T) {
		t.Parallel()
		// Broker does not know the new test user (only the bootstrap admin) — the
		// positive test's subscribe+publish will be rejected.
		_, addr := startVerifyBroker(t, verifyBrokerACL{
			users: map[string]verifyUserACL{
				"old-user": {
					allowed: []string{"old/topic"},
				},
			},
		})
		host, port := splitHostPort(t, addr)

		opts := VerifyActiveOptions{
			Config:           cfgWithCreds(host, port, "", ""),
			Username:         "alice",
			Password:         "test-pass-alice",
			AllowedTopic:     "sensors/temperature",
			DeniedTopic:      "another/old/topic",
			DialTimeout:      2 * time.Second,
			RoundTripTimeout: 2 * time.Second,
			SubTimeout:       2 * time.Second,
		}
		result := VerifyActive(context.Background(), opts)
		if result.OK {
			t.Fatalf("VerifyActive OK=true, want false (broker serves OLD config — alice does not exist); result: %+v", result)
		}
	})

	t.Run("negative fails when broker accepts unknown user", func(t *testing.T) {
		t.Parallel()
		// Broker has NO ACL file (anonymous-style allow-all): an unknown
		// user can connect. The verifier must flag this — the broker is
		// using the OLD config (no auth check).
		_, addr := startNoACLBroker(t)
		host, port := splitHostPort(t, addr)

		opts := VerifyActiveOptions{
			Config:           cfgWithCreds(host, port, "", ""),
			Username:         "alice",
			Password:         "test-pass-alice",
			AllowedTopic:     "sensors/temperature",
			DeniedTopic:      "sensors/forbidden",
			DialTimeout:      2 * time.Second,
			RoundTripTimeout: 2 * time.Second,
			SubTimeout:       2 * time.Second,
		}
		result := VerifyActive(context.Background(), opts)
		if result.OK {
			t.Fatalf("VerifyActive OK=true, want false (unknown user was accepted); result: %+v", result)
		}
	})

	t.Run("auth failure surfaces as failed verification", func(t *testing.T) {
		t.Parallel()
		_, addr := startVerifyBroker(t, verifyBrokerACL{
			users: map[string]verifyUserACL{
				"alice": {
					allowed: []string{"sensors/temperature"},
				},
			},
		})
		host, port := splitHostPort(t, addr)

		opts := VerifyActiveOptions{
			Config:           cfgWithCreds(host, port, "", ""),
			Username:         "alice",
			Password:         "wrong-password",
			AllowedTopic:     "sensors/temperature",
			DeniedTopic:      "sensors/forbidden",
			DialTimeout:      2 * time.Second,
			RoundTripTimeout: 2 * time.Second,
			SubTimeout:       2 * time.Second,
		}
		result := VerifyActive(context.Background(), opts)
		if result.OK {
			t.Fatalf("VerifyActive OK=true, want false (wrong password); result: %+v", result)
		}
	})
}

// splitHostPort returns (host, port) for the given "host:port" string.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}

// cfgWithCreds builds a MosquittoConfig that points at host:port with
// the given credentials.
func cfgWithCreds(host string, port int, username, password string) config.MosquittoConfig {
	cfg := config.Default().Mosquitto
	cfg.Host = host
	cfg.Port = port
	cfg.Username = username
	cfg.Password = password
	return cfg
}

func TestCheckMQTTConnectivityReportsReachableBroker(t *testing.T) {
	listener := startMQTTTestBroker(t, []byte{0x20, 0x02, 0x00, 0x00})

	cfg := config.Default().Mosquitto
	cfg.Host = "127.0.0.1"
	cfg.Port = listener.Addr().(*net.TCPAddr).Port

	result := CheckMQTTConnectivity(context.Background(), cfg)
	if !result.OK {
		t.Fatalf("CheckMQTTConnectivity OK=false, want true; message: %s", result.Message)
	}
	if result.Address == "" || result.Message == "" {
		t.Fatalf("CheckMQTTConnectivity returned incomplete result: %+v", result)
	}
}

func TestCheckMQTTConnectivityReportsUnreachableBroker(t *testing.T) {
	listener := startMQTTTestBroker(t, []byte{0x20, 0x02, 0x00, 0x00})
	addr := listener.Addr().(*net.TCPAddr)
	if err := listener.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	cfg := config.Default().Mosquitto
	cfg.Host = "127.0.0.1"
	cfg.Port = addr.Port

	result := CheckMQTTConnectivity(context.Background(), cfg)
	if result.OK {
		t.Fatalf("CheckMQTTConnectivity OK=true, want false; result: %+v", result)
	}
	if !strings.Contains(result.Message, "TCP connection failed") {
		t.Fatalf("message = %q, want TCP failure category", result.Message)
	}
}

func TestCheckMQTTConnectivityReportsTLSHandshakeFailure(t *testing.T) {
	listener := startMQTTTestBroker(t, []byte{0x20, 0x02, 0x00, 0x00})

	cfg := config.Default().Mosquitto
	cfg.Host = "127.0.0.1"
	cfg.Port = listener.Addr().(*net.TCPAddr).Port
	cfg.TLS.Enabled = true
	cfg.TLS.InsecureSkipVerify = true

	result := CheckMQTTConnectivity(context.Background(), cfg)
	if result.OK {
		t.Fatalf("CheckMQTTConnectivity OK=true, want false; result: %+v", result)
	}
	if !strings.Contains(result.Message, "TCP connection succeeded") || !strings.Contains(result.Message, "TLS handshake failed") {
		t.Fatalf("message = %q, want TLS handshake failure category", result.Message)
	}
}

func TestCheckMQTTConnectivityReportsConnackRejection(t *testing.T) {
	listener := startMQTTTestBroker(t, []byte{0x20, 0x02, 0x00, 0x05})

	cfg := config.Default().Mosquitto
	cfg.Host = "127.0.0.1"
	cfg.Port = listener.Addr().(*net.TCPAddr).Port

	result := CheckMQTTConnectivity(context.Background(), cfg)
	if result.OK {
		t.Fatalf("CheckMQTTConnectivity OK=true, want false; result: %+v", result)
	}
	if !strings.Contains(result.Message, "MQTT CONNECT/CONNACK failed") || !strings.Contains(result.Message, "not authorized") {
		t.Fatalf("message = %q, want MQTT CONNACK rejection category", result.Message)
	}
}

func TestBuildTLSConfigReportsMissingClientKeyPairFile(t *testing.T) {
	cfg := config.Default().Mosquitto
	cfg.TLS.Enabled = true
	cfg.TLS.ClientCertFile = "client.crt"

	_, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err == nil {
		t.Fatal("BuildMosquittoTLSConfig returned nil error, want missing key pair error")
	}
	if !strings.Contains(err.Error(), "client_cert_file") || !strings.Contains(err.Error(), "client_key_file") {
		t.Fatalf("error = %q, want actionable client cert/key message", err.Error())
	}
}

func TestBuildTLSConfigIPHostYieldsEmptyServerName(t *testing.T) {
	cfg := config.Default().Mosquitto
	cfg.Host = "127.0.0.1"
	cfg.TLS.Enabled = true

	tlsCfg, err := tlsutil.BuildMosquittoTLSConfig(cfg)
	if err != nil {
		t.Fatalf("BuildMosquittoTLSConfig returned error: %v", err)
	}
	if tlsCfg.ServerName != "" {
		t.Fatalf("ServerName = %q, want empty string for IP host", tlsCfg.ServerName)
	}
}

func TestBuildMQTTConnectPacketIncludesCredentials(t *testing.T) {
	cfg := config.Default().Mosquitto
	cfg.Username = "admin"
	cfg.Password = "secret"

	packet, err := buildMQTTConnectPacket(cfg)
	if err != nil {
		t.Fatalf("buildMQTTConnectPacket returned error: %v", err)
	}
	if packet[0] != 0x10 {
		t.Fatalf("MQTT packet type = %#x, want CONNECT", packet[0])
	}
	connectFlags := packet[9]
	if connectFlags&0x80 == 0 || connectFlags&0x40 == 0 {
		t.Fatalf("CONNECT flags = %#x, want username and password flags", connectFlags)
	}
}

func startMQTTTestBroker(t *testing.T, connack []byte) net.Listener {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}

	done := make(chan struct{})
	t.Cleanup(func() {
		_ = listener.Close()
		<-done
	})

	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			t.Errorf("Accept returned error: %v", err)
			return
		}
		defer conn.Close()

		buf := make([]byte, 256)
		if _, err := conn.Read(buf); err != nil && !errors.Is(err, io.EOF) {
			t.Errorf("Read returned error: %v", err)
			return
		}
		if _, err := conn.Write(connack); err != nil {
			t.Errorf("Write returned error: %v", err)
		}
	}()

	return listener
}
