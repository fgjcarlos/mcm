//go:build integration

// This file proves issue #299's acceptance criterion "Pruebas con
// múltiples listeners TCP y WebSockets en Mosquitto real": the listener
// writer's splice is only trustworthy if a real Mosquitto broker picks
// up the written listeners after a restart and serves MQTT over both
// TCP and WebSockets.
package writer_test

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/fgjcarlos/mcm/internal/listener/writer"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// waitForPort dials addr until it accepts a connection.
func waitForPort(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("nothing accepted connections on %s", addr)
}

// publishedPort returns the host address of a container's published port.
func publishedPort(t *testing.T, name, port string) string {
	t.Helper()
	out, err := exec.Command("docker", "port", name, port).CombinedOutput()
	if err != nil {
		t.Fatalf("docker port %s %s: %v: %s", name, port, err, string(out))
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		t.Fatalf("container %s publishes no %s port", name, port)
	}
	return fields[0]
}

// mqttRoundTrip connects to brokerURL without credentials and proves a
// publish/subscribe round trip on one topic.
func mqttRoundTrip(t *testing.T, name, brokerURL string) error {
	t.Helper()
	client := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID("mcm-listener-it-" + name).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(false))
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("%s: connect timed out", name)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("%s: connect: %w", name, err)
	}
	defer client.Disconnect(100)

	topic := "mcm-listener-it/" + name
	received := make(chan string, 1)
	if token := client.Subscribe(topic, 1, func(_ mqtt.Client, message mqtt.Message) {
		received <- string(message.Payload())
	}); !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("%s: subscribe timed out", name)
	} else if err := token.Error(); err != nil {
		return fmt.Errorf("%s: subscribe: %w", name, err)
	}
	time.Sleep(300 * time.Millisecond)
	if token := client.Publish(topic, 1, false, "listener-ok"); !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("%s: publish timed out", name)
	} else if err := token.Error(); err != nil {
		return fmt.Errorf("%s: publish: %w", name, err)
	}
	select {
	case payload := <-received:
		if payload != "listener-ok" {
			return fmt.Errorf("%s: received %q", name, payload)
		}
		return nil
	case <-time.After(10 * time.Second):
		return fmt.Errorf("%s: message was not delivered", name)
	}
}

// TestListenerWriterServesTCPAndWebSocketsOnRealBroker replaces the
// bootstrap listener of a real Mosquitto container with a TCP and a
// WebSockets listener through the production FileWriter, restarts the
// broker and proves both transports serve MQTT.
func TestListenerWriterServesTCPAndWebSocketsOnRealBroker(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available: the integration test needs a real broker")
	}

	dir := t.TempDir()
	confPath := filepath.Join(dir, "mosquitto.conf")
	bootstrap := strings.Join([]string{
		"# seeded before MCM manages listeners",
		"allow_anonymous true",
		"persistence false",
		"log_dest stdout",
		"listener 1883 0.0.0.0",
		"",
	}, "\n")
	if err := os.WriteFile(confPath, []byte(bootstrap), 0o644); err != nil {
		t.Fatalf("seed mosquitto.conf: %v", err)
	}

	name := fmt.Sprintf("mcm-listener-it-%d", time.Now().UnixNano())
	// Run as the test user: the image entrypoint chowns mounted config
	// dirs to the in-container mosquitto user when started as root, which
	// would take the directory away from this process (the deploy
	// integration test uses the same pattern).
	uid := strconv.Itoa(os.Getuid())
	gid := strconv.Itoa(os.Getgid())
	if out, err := exec.Command("docker", "run", "--detach",
		"--name", name,
		"--user", uid+":"+gid,
		"--publish", "127.0.0.1::1883",
		"--publish", "127.0.0.1::9001",
		"--volume", dir+":/mosquitto/config",
		"eclipse-mosquitto:2.0",
	).CombinedOutput(); err != nil {
		t.Fatalf("start mosquitto: %v: %s", err, string(out))
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
	waitForPort(t, publishedPort(t, name, "1883/tcp"))

	// The production writer splices the listener set into the broker's
	// own configuration file.
	w := writer.New(confPath)
	specs := []listeners.ListenerSpec{
		{ID: "tcp", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
		{ID: "ws", Port: 9001, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolWebSocket}},
	}
	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("write listeners: %v", err)
	}
	if !strings.Contains(rendered.Full, "listener 1883 0.0.0.0") || !strings.Contains(rendered.Full, "listener 9001 0.0.0.0") {
		t.Fatalf("rendered listeners missing a port:\n%s", rendered.Full)
	}
	if !strings.Contains(rendered.Full, "protocol websockets") {
		t.Fatalf("rendered listeners missing the websockets protocol:\n%s", rendered.Full)
	}
	for _, preserved := range []string{"allow_anonymous true", "persistence false", "# seeded before MCM manages listeners"} {
		if !strings.Contains(rendered.Full, preserved) {
			t.Fatalf("splice dropped %q:\n%s", preserved, rendered.Full)
		}
	}
	onDisk, err := os.ReadFile(confPath)
	if err != nil || string(onDisk) != rendered.Full {
		t.Fatalf("on-disk conf diverged from the render: %q %v", string(onDisk), err)
	}

	// Listeners only take effect after a restart; that is exactly the
	// apply semantics the service drives.
	if out, err := exec.Command("docker", "restart", name).CombinedOutput(); err != nil {
		t.Fatalf("restart the broker: %v: %s", err, string(out))
	}
	tcpAddr := publishedPort(t, name, "1883/tcp")
	wsAddr := publishedPort(t, name, "9001/tcp")
	waitForPort(t, tcpAddr)
	waitForPort(t, wsAddr)

	if err := mqttRoundTrip(t, "tcp", "tcp://"+tcpAddr); err != nil {
		t.Fatalf("TCP listener: %v", err)
	}
	if err := mqttRoundTrip(t, "ws", "ws://"+wsAddr+"/mqtt"); err != nil {
		t.Fatalf("WebSockets listener: %v", err)
	}
}
