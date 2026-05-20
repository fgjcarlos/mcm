package diagnostics

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

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
