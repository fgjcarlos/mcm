//go:build integration

// The tests in this file exercise the deploy lifecycle against a REAL
// Mosquitto broker: the files MCM writes, the reload signal and the
// recovery are only proven by the broker itself (issue #294, S27 and the
// global constraint "verify the behaviour changes with integration
// tests").
//
// The broker is the eclipse-mosquitto container image, started as the
// same user as the test process. That is the production model the
// deployment requires: MCM writes the passwd/acl files with mode 0600,
// so the broker must run as the same user to read them without running
// as root.
package deploy

import (
	"context"
	"errors"
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

// realBroker is a Mosquitto broker running in a container.
type realBroker struct {
	// name is the container name.
	name string
	// addr is the host address of the MQTT listener.
	addr string
	// configDir is the host directory mounted as /mosquitto/config. It
	// holds the files MCM writes.
	configDir string
}

// stop removes the container.
func (b *realBroker) stop() {
	_ = exec.Command("docker", "rm", "--force", b.name).Run()
}

// exec runs a command inside the container.
func (b *realBroker) exec(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	argv := append([]string{"docker", "exec", b.name, name}, args...)
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// reload signals the broker to reload its configuration (SIGHUP to PID
// 1, the broker process inside the container).
func (b *realBroker) reload(t *testing.T) {
	t.Helper()
	if _, err := b.exec(t, "kill", "-HUP", "1"); err != nil {
		t.Fatalf("signal the broker: %v", err)
	}
}

// startRealBroker starts a Mosquitto broker that reads the passwd and
// ACL files of the given directory.
func startRealBroker(t *testing.T) *realBroker {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not available: the integration test needs a real broker")
	}

	dir := t.TempDir()
	conf := strings.Join([]string{
		"persistence false",
		"allow_anonymous false",
		"password_file /mosquitto/config/passwd",
		"acl_file /mosquitto/config/acl",
		"listener 1883 0.0.0.0",
		"log_dest stdout",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "mosquitto.conf"), []byte(conf), 0o644); err != nil {
		t.Fatalf("write the broker configuration: %v", err)
	}
	// Mosquitto 2.0 refuses to start when password_file or acl_file do
	// not exist. Seed both files EMPTY: the first deploy apply in the
	// test writes the real content, which also proves MCM creates the
	// managed files from scratch (alta).
	if err := os.WriteFile(filepath.Join(dir, "passwd"), nil, 0o600); err != nil {
		t.Fatalf("seed the password file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "acl"), nil, 0o600); err != nil {
		t.Fatalf("seed the ACL file: %v", err)
	}
	// The broker runs as the same user as the test process: MCM writes
	// the files with mode 0600, so the broker must own them to read them
	// without running as root.
	uid := strconv.Itoa(os.Getuid())
	gid := strconv.Itoa(os.Getgid())
	name := fmt.Sprintf("mcm-deploy-it-%d", time.Now().UnixNano())
	cmd := exec.Command("docker", "run", "--detach",
		"--name", name,
		"--user", uid+":"+gid,
		"--publish", "127.0.0.1::1883",
		"--volume", dir+":/mosquitto/config",
		"eclipse-mosquitto:2.0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start the mosquitto container: %v: %s", err, string(out))
	}
	broker := &realBroker{name: name, configDir: dir}
	t.Cleanup(broker.stop)

	broker.addr = realBrokerHostPort(t, name, "1883/tcp")
	waitForRealBroker(t, broker.addr)
	return broker
}

// realBrokerHostPort returns the host address of a published port.
func realBrokerHostPort(t *testing.T, name, port string) string {
	t.Helper()
	out, err := exec.Command("docker", "port", name, port).CombinedOutput()
	if err != nil {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("read the published port of %s: %v: %s\ncontainer logs:\n%s", name, err, string(out), string(logs))
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		t.Fatalf("the container %s publishes no %s port", name, port)
	}
	return fields[0]
}

// waitForRealBroker waits until the broker accepts connections.
func waitForRealBroker(t *testing.T, addr string) {
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

// passwdBody renders a password file with one user.
func passwdBody(t *testing.T, username, password string) string {
	t.Helper()
	hash, err := mosquitto.HashPassword(password, mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("hash the password: %v", err)
	}
	body, err := mosquitto.RenderPasswdFile([]mosquitto.PasswdEntry{{Username: username, Hash: hash}})
	if err != nil {
		t.Fatalf("render the password file: %v", err)
	}
	return body
}

// aclBody renders an ACL file that grants one user access to one topic.
func aclBody(t *testing.T, username, topic string) string {
	t.Helper()
	body, err := mosquitto.RenderACLFile([]acl.Rule{{
		Principal:   username,
		TopicFilter: topic,
		Permission:  acl.PermissionReadWrite,
	}})
	if err != nil {
		t.Fatalf("render the ACL file: %v", err)
	}
	return body
}

// connect reports whether a client can connect with the given
// credentials.
func realConnect(t *testing.T, addr, username, password string) error {
	t.Helper()
	client := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID("mcm-deploy-it-" + username).
		SetUsername(username).
		SetPassword(password).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(false))
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return errors.New("the connect attempt timed out")
	}
	if err := token.Error(); err != nil {
		return err
	}
	client.Disconnect(100)
	return nil
}

// publishAndReceive publishes a message and waits for it on a
// subscription of the same client.
func publishAndReceive(t *testing.T, addr, username, password, topic string) error {
	t.Helper()
	client := mqtt.NewClient(mqtt.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID("mcm-deploy-it-roundtrip").
		SetUsername(username).
		SetPassword(password).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(false))
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return errors.New("the connect attempt timed out")
	}
	if err := token.Error(); err != nil {
		return err
	}
	defer client.Disconnect(100)

	received := make(chan string, 1)
	if token := client.Subscribe(topic, 1, func(_ mqtt.Client, message mqtt.Message) {
		received <- string(message.Payload())
	}); !token.WaitTimeout(10 * time.Second) {
		return errors.New("the subscription timed out")
	} else if err := token.Error(); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	if token := client.Publish(topic, 1, false, "deployed"); !token.WaitTimeout(10 * time.Second) {
		return errors.New("the publish timed out")
	} else if err := token.Error(); err != nil {
		return err
	}
	select {
	case payload := <-received:
		if payload != "deployed" {
			return fmt.Errorf("received %q, want %q", payload, "deployed")
		}
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("the message was not delivered")
	}
}

// TestDeployLifecycleAgainstARealBroker proves the deployment lifecycle
// of issue #294 against a real Mosquitto broker:
//
//   - alta: a new user is written into the password file and the broker
//     accepts it;
//   - apply: the ACL is written and the broker enforces it;
//   - reinicio: the broker is restarted and keeps serving the deployed
//     policy;
//   - recuperacion: a deploy that cannot reach the broker restores the
//     previous files, and the broker keeps serving the previous policy.
func TestDeployLifecycleAgainstARealBroker(t *testing.T) {
	broker := startRealBroker(t)

	aclPath := filepath.Join(broker.configDir, "acl")
	passwdPath := filepath.Join(broker.configDir, "passwd")

	applier := mosquitto.FileApplier{
		ACLPath:    aclPath,
		PasswdPath: passwdPath,
		// The production reload mechanism (issue #294): a command that
		// signals the broker out of band. No Docker socket is involved
		// in MCM itself; the test runs the command from the host.
		ReloadCommand: []string{"docker", "exec", broker.name, "kill", "-HUP", "1"},
		ReloadRunner:  mosquitto.ExecRunner{},
	}

	// --- alta + apply -----------------------------------------------------
	const (
		username = "deploy-user"
		password = "deploy-user-password"
		topic    = "deploy/allowed"
	)
	aclContent := aclBody(t, username, topic)
	passwdContent := passwdBody(t, username, password)
	if err := applier.Apply(context.Background(), mosquitto.ApplyRequest{
		ACLBody:        aclContent,
		ACLSnapshot:    "",
		PasswdBody:     passwdContent,
		PasswdSnapshot: "",
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The broker enforces the deployed files: the user authenticates and
	// the granted topic works.
	if err := realConnect(t, broker.addr, username, password); err != nil {
		t.Fatalf("the deployed user cannot connect: %v", err)
	}
	if err := publishAndReceive(t, broker.addr, username, password, topic); err != nil {
		t.Fatalf("the granted topic does not work: %v", err)
	}
	if err := realConnect(t, broker.addr, username, "wrong-password"); err == nil {
		t.Fatal("the broker accepted a wrong password")
	}

	// --- reinicio ---------------------------------------------------------
	if out, err := exec.Command("docker", "restart", broker.name).CombinedOutput(); err != nil {
		t.Fatalf("restart the broker: %v: %s", err, string(out))
	}
	// A dynamically published host port can be REASSIGNED when the
	// container restarts; re-resolve it instead of trusting the old one.
	broker.addr = realBrokerHostPort(t, broker.name, "1883/tcp")
	waitForRealBroker(t, broker.addr)
	if err := realConnect(t, broker.addr, username, password); err != nil {
		t.Fatalf("the deployed user cannot connect after the restart: %v", err)
	}
	if err := publishAndReceive(t, broker.addr, username, password, topic); err != nil {
		t.Fatalf("the granted topic does not work after the restart: %v", err)
	}

	// --- recuperacion -----------------------------------------------------
	// The broker is stopped: the deploy cannot signal it, so the applier
	// restores the previous files.
	if out, err := exec.Command("docker", "stop", broker.name).CombinedOutput(); err != nil {
		t.Fatalf("stop the broker: %v: %s", err, string(out))
	}
	newACL := aclBody(t, username, "deploy/other")
	newPasswd := passwdBody(t, username, "another-password")
	err := applier.Apply(context.Background(), mosquitto.ApplyRequest{
		ACLBody:        newACL,
		ACLSnapshot:    aclContent,
		PasswdBody:     newPasswd,
		PasswdSnapshot: passwdContent,
	})
	if err == nil {
		t.Fatal("apply: want an error when the broker cannot be signalled")
	}
	if !errors.Is(err, mosquitto.ErrApplyRestored) {
		t.Fatalf("apply error = %v, want ErrApplyRestored", err)
	}
	restoredACL, readErr := os.ReadFile(aclPath)
	if readErr != nil {
		t.Fatalf("read the restored ACL: %v", readErr)
	}
	if string(restoredACL) != aclContent {
		t.Errorf("the ACL file was not restored:\n%s", string(restoredACL))
	}
	restoredPasswd, readErr := os.ReadFile(passwdPath)
	if readErr != nil {
		t.Fatalf("read the restored password file: %v", readErr)
	}
	if string(restoredPasswd) != passwdContent {
		t.Error("the password file was not restored")
	}

	// The broker comes back and serves the previous policy.
	if out, err := exec.Command("docker", "start", broker.name).CombinedOutput(); err != nil {
		t.Fatalf("start the broker: %v: %s", err, string(out))
	}
	// Same dynamic-port caveat as the restart above.
	broker.addr = realBrokerHostPort(t, broker.name, "1883/tcp")
	waitForRealBroker(t, broker.addr)
	if err := realConnect(t, broker.addr, username, password); err != nil {
		t.Fatalf("the previous user cannot connect after the recovery: %v", err)
	}
	if err := realConnect(t, broker.addr, username, "another-password"); err == nil {
		t.Fatal("the broker accepted the password of the rolled-back deploy")
	}
}
