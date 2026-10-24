//go:build integration

// Package acl_test contains the integration tests of the native ACL
// surface.
//
// The tests run against a real Mosquitto broker, because the semantics
// MCM models are the semantics of the broker: a deny is handled before a
// grant, the anonymous section applies to unauthenticated clients only,
// the pattern rules apply to every client, and a listener that declares
// its own `acl_file` uses only that file.
//
// The broker is started by the test itself. It uses the `mosquitto` binary
// when MOSQUITTO_BIN points at one, and the eclipse-mosquitto container
// image otherwise. The test is skipped when neither is available.
package acl_test

import (
	"context"
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

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
)

// broker is a running Mosquitto instance.
type broker struct {
	// globalAddr is the address of the listener that uses the
	// broker-wide ACL file.
	globalAddr string
	// scopedAddr is the address of the listener that declares its own
	// ACL file.
	scopedAddr string
	// container is the name of the container that runs the broker. It is
	// empty when the broker runs as a local process.
	container string
	stop      func()
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

// startBroker starts a Mosquitto broker with the given configuration and
// the given ACL files.
//
// The broker exposes two listeners: the first one uses the broker-wide
// ACL file, the second one declares its own ACL file, which is the
// per-listener scope MCM manages.
func startBroker(t *testing.T, dir string, conf string) *broker {
	t.Helper()

	if bin := strings.TrimSpace(os.Getenv("MOSQUITTO_BIN")); bin != "" {
		return startLocalBroker(t, dir, conf, bin)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("neither MOSQUITTO_BIN nor docker is available: the integration test needs a real broker")
	}
	return startDockerBroker(t, dir, conf)
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

// startLocalBroker starts the mosquitto binary with the given
// configuration.
func startLocalBroker(t *testing.T, dir string, conf string, bin string) *broker {
	t.Helper()

	globalPort := freePort(t)
	scopedPort := freePort(t)
	body := strings.ReplaceAll(conf, "{{GLOBAL_PORT}}", strconv.Itoa(globalPort))
	body = strings.ReplaceAll(body, "{{SCOPED_PORT}}", strconv.Itoa(scopedPort))
	body = strings.ReplaceAll(body, "{{CONFIG_DIR}}", dir)
	body = strings.ReplaceAll(body, "{{BIND}}", "127.0.0.1")
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
	stop := func() {
		cancel()
		_ = cmd.Wait()
	}

	globalAddr := fmt.Sprintf("127.0.0.1:%d", globalPort)
	scopedAddr := fmt.Sprintf("127.0.0.1:%d", scopedPort)
	waitForBroker(t, globalAddr)
	waitForBroker(t, scopedAddr)
	return &broker{globalAddr: globalAddr, scopedAddr: scopedAddr, stop: stop}
}

// startDockerBroker starts the eclipse-mosquitto container with the given
// configuration.
func startDockerBroker(t *testing.T, dir string, conf string) *broker {
	t.Helper()

	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o777); err != nil {
		t.Fatalf("create the data directory: %v", err)
	}
	confPath := filepath.Join(dir, "mosquitto.conf")
	// The container publishes fixed ports, so the configuration uses them
	// and the host addresses are discovered afterwards.
	body := strings.ReplaceAll(conf, "{{GLOBAL_PORT}}", "1883")
	body = strings.ReplaceAll(body, "{{SCOPED_PORT}}", "2883")
	body = strings.ReplaceAll(body, "{{CONFIG_DIR}}", "/mosquitto/config")
	// The listener binds every interface of the container: the published
	// host port reaches the container through its network namespace.
	body = strings.ReplaceAll(body, "{{BIND}}", "0.0.0.0")
	if err := os.WriteFile(confPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}
	// The container runs as its own user and needs to read the
	// configuration and to write its data.
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("relax the configuration directory: %v", err)
	}
	if err := os.Chmod(confPath, 0o644); err != nil {
		t.Fatalf("relax the configuration file: %v", err)
	}

	name := fmt.Sprintf("mcm-acl-it-%d", time.Now().UnixNano())
	// The container is not removed automatically: the logs of a failed
	// start are the only evidence of what went wrong.
	cmd := exec.Command("docker", "run", "--detach",
		"--name", name,
		"--publish", "127.0.0.1::1883",
		"--publish", "127.0.0.1::2883",
		// The mount is writable because the image entry point takes
		// ownership of the configuration files before it starts the
		// broker.
		"--volume", fmt.Sprintf("%s:/mosquitto/config", dir),
		"--volume", fmt.Sprintf("%s:/mosquitto/data", dataDir),
		"eclipse-mosquitto:2.0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start the mosquitto container: %v: %s", err, string(out))
	}
	stop := func() {
		_ = exec.Command("docker", "rm", "--force", name).Run()
	}

	globalAddr := containerPort(t, name, "1883/tcp")
	scopedAddr := containerPort(t, name, "2883/tcp")
	waitForBroker(t, globalAddr)
	waitForBroker(t, scopedAddr)
	return &broker{globalAddr: globalAddr, scopedAddr: scopedAddr, container: name, stop: stop}
}

// containerPort returns the host address of a published container port.
func containerPort(t *testing.T, name string, port string) string {
	t.Helper()
	out, err := exec.Command("docker", "port", name, port).CombinedOutput()
	if err != nil {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		status, _ := exec.Command("docker", "inspect", "--format", "{{.State.Status}} {{.State.ExitCode}}", name).CombinedOutput()
		t.Fatalf("read the published port of %s: %v: %s\ncontainer state: %s\ncontainer logs:\n%s", name, err, string(out), string(status), string(logs))
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		t.Fatalf("the container %s publishes no %s port", name, port)
	}
	return fields[0]
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

// client connects to the broker.
type client struct {
	t     *testing.T
	inner mqtt.Client
}

// connect opens an MQTT connection with the given credentials. An empty
// username connects anonymously.
func connect(t *testing.T, addr string, username string, password string) *client {
	return connectTo(t, nil, addr, username, password)
}

// connectTo opens an MQTT connection and reports the output of the broker
// when the connection fails.
func connectTo(t *testing.T, instance *broker, addr string, username string, password string) *client {
	t.Helper()
	options := mqtt.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID(fmt.Sprintf("mcm-it-%s-%d", username, time.Now().UnixNano())).
		SetConnectTimeout(10 * time.Second)
	if username != "" {
		options.SetUsername(username)
		options.SetPassword(password)
	}
	inner := mqtt.NewClient(options)
	token := inner.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatalf("the connection to %s timed out", addr)
	}
	if err := token.Error(); err != nil {
		logs := ""
		if instance != nil {
			logs = instance.logs()
		}
		t.Fatalf("connect to %s: %v\nbroker logs:\n%s", addr, err, logs)
	}
	return &client{t: t, inner: inner}
}

// close ends the connection.
func (c *client) close() {
	c.inner.Disconnect(250)
}

// subscribe subscribes to a topic and reports whether the broker granted
// the subscription.
//
// Mosquitto answers a denied subscription with the failure code, so the
// SUBACK carries the verdict.
func (c *client) subscribe(topic string) bool {
	c.t.Helper()
	token := c.inner.Subscribe(topic, 1, func(_ mqtt.Client, _ mqtt.Message) {})
	if !token.WaitTimeout(10 * time.Second) {
		c.t.Fatalf("the subscription to %s timed out", topic)
	}
	if err := token.Error(); err != nil {
		c.t.Fatalf("subscribe to %s: %v", topic, err)
	}
	subscribeToken, ok := token.(*mqtt.SubscribeToken)
	if !ok {
		c.t.Fatalf("the subscription to %s returned an unexpected token", topic)
	}
	for _, code := range subscribeToken.Result() {
		// Mosquitto answers a denied subscription with the failure code
		// 0x80.
		if code > 2 {
			return false
		}
	}
	return true
}

// publish publishes a message and reports whether the broker accepted it.
//
// MQTT 3.1.1 has no negative acknowledgement for a publish, so the
// acceptance is proven by the message arriving at a subscriber.
func (c *client) publish(topic string, payload string) {
	c.t.Helper()
	token := c.inner.Publish(topic, 1, false, payload)
	if !token.WaitTimeout(10 * time.Second) {
		c.t.Fatalf("the publish to %s timed out", topic)
	}
	if err := token.Error(); err != nil {
		c.t.Fatalf("publish to %s: %v", topic, err)
	}
}

// watcher collects the messages of a topic.
type watcher struct {
	messages chan string
}

// watch subscribes to a topic and collects the messages it receives.
func watch(t *testing.T, client *client, topic string) *watcher {
	t.Helper()
	collected := &watcher{messages: make(chan string, 16)}
	token := client.inner.Subscribe(topic, 1, func(_ mqtt.Client, message mqtt.Message) {
		select {
		case collected.messages <- string(message.Payload()):
		default:
		}
	})
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatalf("the subscription to %s timed out", topic)
	}
	if err := token.Error(); err != nil {
		t.Fatalf("subscribe to %s: %v", topic, err)
	}
	return collected
}

// received reports whether a message arrived within the given window.
func (w *watcher) received(window time.Duration) bool {
	select {
	case <-w.messages:
		return true
	case <-time.After(window):
		return false
	}
}

// writeBrokerConfig writes a Mosquitto configuration with two listeners:
// the first one uses the broker-wide ACL file, the second one declares
// its own ACL file.
func writeBrokerConfig(t *testing.T, dir string) {
	t.Helper()
	// The global port serves the default listener, which uses the
	// broker-wide ACL file. The explicit listener block declares its own
	// ACL file, which is the per-listener scope.
	body := `per_listener_settings true
port {{GLOBAL_PORT}}
allow_anonymous true
password_file {{CONFIG_DIR}}/passwd
acl_file {{CONFIG_DIR}}/acl

listener {{SCOPED_PORT}} {{BIND}}
protocol mqtt
password_file {{CONFIG_DIR}}/passwd
acl_file {{CONFIG_DIR}}/acl.scoped
`
	if err := os.WriteFile(filepath.Join(dir, "mosquitto.conf"), []byte(body), 0o644); err != nil {
		t.Fatalf("write mosquitto.conf: %v", err)
	}
}

// writePasswd writes a password file with the given users.
func writePasswd(t *testing.T, dir string, users map[string]string) {
	t.Helper()
	var builder strings.Builder
	for username, password := range users {
		hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
		if err != nil {
			t.Fatalf("hash the password of %s: %v", username, err)
		}
		builder.WriteString(username + ":" + hash + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "passwd"), []byte(builder.String()), 0o600); err != nil {
		t.Fatalf("write passwd: %v", err)
	}
}

// TestBrokerAppliesTheNativeACLSemantics proves the semantics MCM models
// against a real Mosquitto broker.
//
// The test covers the four behaviours the native ACL surface relies on:
//
//   - a deny rule is handled before a grant, so the denied topic is not
//     reachable even though a broader rule grants it;
//   - the anonymous section applies to unauthenticated clients only;
//   - the pattern rules apply to every client, with the username and the
//     client id substituted;
//   - a listener that declares its own ACL file uses only that file, so
//     the rules of the broker-wide file do not apply to it.
//
// The access is proven with the messages that arrive, because the ACL file
// of Mosquitto controls the publish and the delivery of a message, not
// the subscription itself.
func TestBrokerAppliesTheNativeACLSemantics(t *testing.T) {
	dir := t.TempDir()
	writeBrokerConfig(t, dir)
	writePasswd(t, dir, map[string]string{
		"alice": "alice-pass",
		"bob":   "bob-pass",
		"mcm":   "mcm-pass",
	})

	// The broker-wide ACL file carries the rules MCM renders for the
	// global scope.
	globalRules := []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
		{Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/secret/#", Permission: acl.PermissionDeny},
		{Selector: acl.SelectorPattern, TopicFilter: "devices/%u/data", Permission: acl.PermissionReadWrite},
		{Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionReadWrite},
	}
	globalRules = append(globalRules, serviceRules("mcm")...)
	if err := os.WriteFile(filepath.Join(dir, "acl"), []byte(mosquitto.RenderACLFile(globalRules)), 0o600); err != nil {
		t.Fatalf("write the acl file: %v", err)
	}

	// The scoped ACL file carries the rules of one listener plus the
	// service user block MCM emits in every file.
	scopedRules := []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "bob", TopicFilter: "listener/#", Permission: acl.PermissionReadWrite},
	}
	scopedRules = append(scopedRules, serviceRules("mcm")...)
	if err := os.WriteFile(filepath.Join(dir, "acl.scoped"), []byte(mosquitto.RenderACLFile(scopedRules)), 0o600); err != nil {
		t.Fatalf("write the scoped acl file: %v", err)
	}

	instance := startBroker(t, dir, readBrokerConfig(t, dir))
	defer instance.stop()

	t.Run("the deny rule wins over the broader grant", func(t *testing.T) {
		alice := connect(t, instance.globalAddr, "alice", "alice-pass")
		defer alice.close()

		if !delivered(t, instance, instance.globalAddr, "alice", "alice-pass", "sensors/public/1") {
			t.Fatal("the broker refused a publish the grant allows")
		}
		if delivered(t, instance, instance.globalAddr, "alice", "alice-pass", "sensors/secret/1") {
			t.Fatal("the broker delivered a publish a deny rule covers")
		}
	})

	t.Run("the anonymous section applies to unauthenticated clients only", func(t *testing.T) {
		if !delivered(t, instance, instance.globalAddr, "", "", "public/1") {
			t.Fatal("the anonymous section does not apply to an unauthenticated client")
		}
		if delivered(t, instance, instance.globalAddr, "alice", "alice-pass", "public/1") {
			t.Fatal("the anonymous section applies to a user although it is not a fallback for the users")
		}
	})

	t.Run("the pattern rules apply to every client", func(t *testing.T) {
		if !delivered(t, instance, instance.globalAddr, "alice", "alice-pass", "devices/alice/data") {
			t.Fatal("the pattern rule does not apply to the client")
		}
		if delivered(t, instance, instance.globalAddr, "alice", "alice-pass", "devices/bob/data") {
			t.Fatal("the pattern rule applies to another user: the username placeholder was not substituted")
		}
	})

	t.Run("a listener with its own ACL file uses only that file", func(t *testing.T) {
		// The service user reads every topic of the scoped listener, so
		// it observes what the listener accepts.
		if delivered(t, instance, instance.scopedAddr, "alice", "alice-pass", "sensors/1") {
			t.Fatal("the scoped listener applied the broker-wide file to a user it does not grant")
		}
		if !delivered(t, instance, instance.scopedAddr, "bob", "bob-pass", "listener/1") {
			t.Fatal("the scoped listener refused a publish its own ACL file grants")
		}
	})

	t.Run("the broker-wide file applies to the listener without its own file", func(t *testing.T) {
		if !delivered(t, instance, instance.globalAddr, "alice", "alice-pass", "sensors/1") {
			t.Fatal("the broker-wide file did not apply to the listener without its own file")
		}
	})
}

// delivered reports whether a message published by the given client
// reaches a subscriber that may read every topic.
//
// The service user of MCM owns a read grant on every topic, so it
// observes the delivery of the message the publisher produced.
func delivered(t *testing.T, instance *broker, addr string, username string, password string, topic string) bool {
	t.Helper()

	service := connectTo(t, instance, addr, "mcm", "mcm-pass")
	defer service.close()
	observer := watch(t, service, topic)

	publisher := connectTo(t, instance, addr, username, password)
	defer publisher.close()
	publisher.publish(topic, "payload")

	return observer.received(3 * time.Second)
}

// serviceRules returns the service user block MCM emits in every ACL
// file: the runtime account reads every topic and owns the healthcheck
// topic.
func serviceRules(username string) []acl.Rule {
	return []acl.Rule{
		{Selector: acl.SelectorUser, Principal: username, TopicFilter: "#", Permission: acl.PermissionRead},
		{Selector: acl.SelectorUser, Principal: username, TopicFilter: "mcm/healthcheck", Permission: acl.PermissionReadWrite},
	}
}

// readBrokerConfig returns the configuration of the broker.
func readBrokerConfig(t *testing.T, dir string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "mosquitto.conf"))
	if err != nil {
		t.Fatalf("read mosquitto.conf: %v", err)
	}
	return string(body)
}
