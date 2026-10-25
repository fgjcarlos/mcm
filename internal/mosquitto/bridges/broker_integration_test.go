//go:build integration

// Package bridges_test contains the integration tests of the bridge
// surface.
//
// The tests run against a real Mosquitto broker, because a bridge is a
// connection between two brokers and only the broker can prove that the
// traffic flows and that the connection recovers. A static audit cannot
// show that.
//
// The broker is started by the test itself. It uses the `mosquitto` binary
// when MOSQUITTO_BIN points at one, and the eclipse-mosquitto container
// image otherwise. The test is skipped when neither is available.
package bridges_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/fgjcarlos/mcm/internal/mosquitto/bridges"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

// broker is a running Mosquitto instance.
type broker struct {
	// addr is the address of the listener of the broker.
	addr string
	// container is the name of the container that runs the broker. It is
	// empty when the broker runs as a local process.
	container string
	// cmd is the local process that runs the broker, when the broker
	// does not run in a container.
	cmd *exec.Cmd
	// cancel stops the local process.
	cancel context.CancelFunc
}

// logs returns the output of the broker. It is empty for a local broker.
func (b *broker) logs() string {
	if b.container == "" {
		return ""
	}
	out, err := exec.Command("docker", "logs", b.container).CombinedOutput()
	if err != nil {
		return string(out)
	}
	return string(out)
}

// stop ends the broker. The port it used stays reserved for the broker
// that takes its place, so a restarted broker comes back on the same
// address.
func (b *broker) stop(t *testing.T) {
	t.Helper()
	if b.container != "" {
		if err := exec.Command("docker", "rm", "--force", b.container).Run(); err != nil {
			t.Logf("remove the broker container: %v", err)
		}
		return
	}
	if b.cancel != nil {
		b.cancel()
	}
	if b.cmd != nil {
		_ = b.cmd.Wait()
	}
}

// freePort returns a port that is free at the moment of the call.
func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer listener.Close() //nolint:errcheck
	return listener.Addr().(*net.TCPAddr).Port
}

// waitForBroker waits until the broker accepts connections.
func waitForBroker(t *testing.T, addr string) {
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
	t.Fatalf("the broker at %s did not accept connections", addr)
}

// startBroker starts a Mosquitto broker with the given configuration on
// the given port.
func startBroker(t *testing.T, dir string, port int, body string) *broker {
	t.Helper()

	if bin := strings.TrimSpace(os.Getenv("MOSQUITTO_BIN")); bin != "" {
		return startLocalBroker(t, dir, port, body, bin)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("neither MOSQUITTO_BIN nor docker is available: the integration test needs a real broker")
	}
	return startDockerBroker(t, dir, port, body)
}

// startLocalBroker starts the mosquitto binary with the given
// configuration.
func startLocalBroker(t *testing.T, dir string, port int, body string, bin string) *broker {
	t.Helper()

	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, bin, "-c", confPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start mosquitto: %v", err)
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForBroker(t, addr)
	return &broker{addr: addr, cmd: cmd, cancel: cancel}
}

// startDockerBroker starts the eclipse-mosquitto container with the given
// configuration.
//
// The container uses the host network: the bridge of the local broker
// reaches the remote broker on the loopback of the host, exactly like it
// does in a deployment where both brokers run on the same machine.
func startDockerBroker(t *testing.T, dir string, port int, body string) *broker {
	t.Helper()

	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o777); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	confPath := filepath.Join(dir, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("relax the configuration directory: %v", err)
	}

	name := fmt.Sprintf("mcm-bridge-it-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--detach",
		"--name", name,
		"--network", "host",
		"--volume", fmt.Sprintf("%s:/mosquitto/config", dir),
		"--volume", fmt.Sprintf("%s:/mosquitto/data", dataDir),
		"eclipse-mosquitto:2.0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start the mosquitto container: %v: %s", err, string(out))
	}

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	waitForBroker(t, addr)
	return &broker{addr: addr, container: name}
}

// subscriber collects the messages of a topic.
type subscriber struct {
	t        *testing.T
	inner    mqtt.Client
	messages chan string
}

// connect opens an MQTT connection to the broker.
func connect(t *testing.T, addr string) mqtt.Client {
	t.Helper()
	options := mqtt.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID(fmt.Sprintf("mcm-bridge-it-%d", time.Now().UnixNano())).
		SetConnectTimeout(10 * time.Second)
	inner := mqtt.NewClient(options)
	token := inner.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatalf("the connection to %s timed out", addr)
	}
	if err := token.Error(); err != nil {
		t.Fatalf("connect to %s: %v", addr, err)
	}
	return inner
}

// subscribe opens a connection and collects the messages of a topic.
func subscribe(t *testing.T, addr string, topic string) *subscriber {
	t.Helper()
	inner := connect(t, addr)
	messages := make(chan string, 32)
	token := inner.Subscribe(topic, 1, func(_ mqtt.Client, message mqtt.Message) {
		messages <- string(message.Payload())
	})
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatalf("the subscription to %s timed out", topic)
	}
	if err := token.Error(); err != nil {
		t.Fatalf("subscribe to %s: %v", topic, err)
	}
	return &subscriber{t: t, inner: inner, messages: messages}
}

// publish publishes a message.
func (s *subscriber) publish(topic string, payload string) {
	s.t.Helper()
	token := s.inner.Publish(topic, 1, false, payload)
	if !token.WaitTimeout(10 * time.Second) {
		s.t.Fatalf("the publish to %s timed out", topic)
	}
	if err := token.Error(); err != nil {
		s.t.Fatalf("publish to %s: %v", topic, err)
	}
}

// waitForMessage waits for a message with the given payload.
func (s *subscriber) waitForMessage(payload string, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case received := <-s.messages:
			if received == payload {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// close ends the subscription.
func (s *subscriber) close() {
	s.inner.Disconnect(250)
}

// remoteConf is the configuration of the broker the bridge points at.
func remoteConf(port int) string {
	return fmt.Sprintf(`listener %d 127.0.0.1
allow_anonymous true
persistence false
`, port)
}

// localConf renders the configuration of the bridged broker with the
// bridges package, so the test exercises the very configuration MCM
// writes.
func localConf(t *testing.T, port int, remotePort int) string {
	t.Helper()

	base := fmt.Sprintf(`listener %d 127.0.0.1
allow_anonymous true
persistence false
`, port)

	parsed, err := conf.ParseString(base, "mosquitto.conf")
	if err != nil {
		t.Fatalf("parse the base configuration: %v", err)
	}
	doc := bridges.Document{Bridges: []bridges.Bridge{{
		Name:             "upstream",
		Address:          fmt.Sprintf("127.0.0.1:%d", remotePort),
		RemoteClientID:   "mcm-it-local",
		TryPrivate:       boolPtr(true),
		KeepaliveSeconds: intPtr(5),
		RestartTimeout:   "1 2",
		StartType:        "automatic",
		Topics: []bridges.TopicRule{
			{Topic: "bridge/#", Direction: bridges.DirectionBoth, QoS: 1},
		},
	}}}
	updated := bridges.Apply(parsed, doc)
	rendered, err := updated.Render()
	if err != nil {
		t.Fatalf("render the configuration: %v", err)
	}
	return string(rendered)
}

// TestBridgeForwardsTheTrafficAndRecovers proves against two real brokers
// that the configuration MCM writes carries the traffic in both
// directions and that the bridge reconnects when the remote broker comes
// back.
func TestBridgeForwardsTheTrafficAndRecovers(t *testing.T) {
	remotePort := freePort(t)
	localPort := freePort(t)

	remoteDir := t.TempDir()
	remote := startBroker(t, remoteDir, remotePort, remoteConf(remotePort))
	t.Cleanup(func() { remote.stop(t) })

	localDir := t.TempDir()
	local := startBroker(t, localDir, localPort, localConf(t, localPort, remotePort))
	t.Cleanup(func() { local.stop(t) })

	remoteSubscriber := subscribe(t, remote.addr, "bridge/#")
	t.Cleanup(remoteSubscriber.close)
	localSubscriber := subscribe(t, local.addr, "bridge/#")
	t.Cleanup(localSubscriber.close)

	// The bridge connects when the broker starts. Give it a moment to
	// establish the connection before the first message.
	time.Sleep(2 * time.Second)

	// The local broker forwards its traffic to the remote one.
	localSubscriber.publish("bridge/local", "from-local")
	if !remoteSubscriber.waitForMessage("from-local", 15*time.Second) {
		t.Fatalf("the remote broker did not receive the message of the local broker\nremote broker logs:\n%s", remote.logs())
	}

	// The remote broker forwards its traffic back to the local one.
	remoteSubscriber.publish("bridge/remote", "from-remote")
	if !localSubscriber.waitForMessage("from-remote", 15*time.Second) {
		t.Fatalf("the local broker did not receive the message of the remote broker\nlocal broker logs:\n%s", local.logs())
	}

	// The remote broker goes away. The local broker must survive it and
	// keep accepting connections.
	remote.stop(t)
	time.Sleep(2 * time.Second)
	probe := subscribe(t, local.addr, "bridge/probe")
	probe.publish("bridge/probe", "while-down")
	probe.close()

	// The remote broker comes back on the same address. The bridge
	// reconnects and the traffic flows again. The restarted broker gets
	// its own directory: the image entry point takes the ownership of the
	// configuration files, so the test could not write them again.
	remote = startBroker(t, t.TempDir(), remotePort, remoteConf(remotePort))
	t.Cleanup(func() { remote.stop(t) })

	remoteSubscriber = subscribe(t, remote.addr, "bridge/#")
	t.Cleanup(remoteSubscriber.close)

	deadline := time.Now().Add(60 * time.Second)
	for {
		localSubscriber.publish("bridge/after", "after-recovery")
		if remoteSubscriber.waitForMessage("after-recovery", 5*time.Second) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the bridge did not recover after the remote broker came back\nremote broker logs:\n%s", remote.logs())
		}
	}
}

// TestRenderedConfigurationLoadsInTheBroker proves that the
// configuration MCM renders is accepted by the broker: the directives and
// the grammar of the topic line are the ones of Mosquitto 2.0.
func TestRenderedConfigurationLoadsInTheBroker(t *testing.T) {
	port := freePort(t)
	dir := t.TempDir()

	body := localConf(t, port, 1883)
	broker := startBroker(t, dir, port, body)
	t.Cleanup(func() { broker.stop(t) })

	// The broker started, so it accepted the whole configuration.
	probe := subscribe(t, broker.addr, "bridge/#")
	probe.close()
}

func boolPtr(v bool) *bool { return &v }
func intPtr(v int) *int    { return &v }
