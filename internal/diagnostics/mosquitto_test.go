package diagnostics

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestCheckMosquittoConnectionSucceeds(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		defer client.Close()

		if err := readExpectedConnect(client, ""); err != nil {
			done <- err
			return
		}

		if _, err := client.Write([]byte{0x20, 0x02, 0x00, 0x00}); err != nil {
			done <- err
			return
		}

		if err := readExpectedDisconnect(client); err != nil {
			done <- err
			return
		}

		done <- nil
	}()

	cfg := config.Default().Mosquitto
	err := checkMosquittoConnection(context.Background(), cfg, func(context.Context, string, string) (net.Conn, error) {
		return server, nil
	})
	if err != nil {
		t.Fatalf("checkMosquittoConnection returned error: %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("mock broker returned error: %v", err)
	}
}

func TestCheckMosquittoConnectionIncludesBrokerRejection(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		defer client.Close()

		if err := readExpectedConnect(client, ""); err != nil {
			done <- err
			return
		}

		_, err := client.Write([]byte{0x20, 0x02, 0x00, 0x05})
		done <- err
	}()

	cfg := config.Default().Mosquitto
	err := checkMosquittoConnection(context.Background(), cfg, func(context.Context, string, string) (net.Conn, error) {
		return server, nil
	})
	if err == nil {
		t.Fatal("checkMosquittoConnection succeeded, want error")
	}
	if !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("error missing CONNACK reason; got %v", err)
	}

	if err := <-done; err != nil {
		t.Fatalf("mock broker returned error: %v", err)
	}
}

func TestCheckMosquittoConnectionIncludesDialError(t *testing.T) {
	t.Parallel()

	cfg := config.Default().Mosquitto
	err := checkMosquittoConnection(context.Background(), cfg, func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("connection refused")
	})
	if err == nil {
		t.Fatal("checkMosquittoConnection succeeded, want error")
	}
	if !strings.Contains(err.Error(), "connect to broker: connection refused") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMQTTConnectPacketIncludesCredentials(t *testing.T) {
	t.Parallel()

	cfg := config.Default().Mosquitto
	cfg.Username = "doctor"
	cfg.Password = "secret"

	packet, err := mqttConnectPacket(cfg)
	if err != nil {
		t.Fatalf("mqttConnectPacket returned error: %v", err)
	}

	if packet[0] != 0x10 {
		t.Fatalf("unexpected packet type: 0x%02x", packet[0])
	}
	if !strings.Contains(string(packet), "doctor") {
		t.Fatalf("packet missing username; got %q", string(packet))
	}
	if !strings.Contains(string(packet), "secret") {
		t.Fatalf("packet missing password; got %q", string(packet))
	}
}

func readExpectedConnect(conn net.Conn, wantUsername string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(conn, header); err != nil {
		return err
	}
	if header[0] != 0x10 {
		return errors.New("expected CONNECT packet")
	}

	body := make([]byte, int(header[1]))
	if _, err := io.ReadFull(conn, body); err != nil {
		return err
	}

	if !strings.Contains(string(body), "MQTT") {
		return errors.New("CONNECT packet missing MQTT protocol header")
	}
	if wantUsername != "" && !strings.Contains(string(body), wantUsername) {
		return errors.New("CONNECT packet missing username")
	}

	return nil
}

func readExpectedDisconnect(conn net.Conn) error {
	buffer := make([]byte, 2)
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	if _, err := io.ReadFull(conn, buffer); err != nil {
		return err
	}
	if buffer[0] != 0xE0 || buffer[1] != 0x00 {
		return errors.New("expected DISCONNECT packet")
	}
	return nil
}
