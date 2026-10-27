//go:build integration

// Package dynsec_test contains the integration tests of the dynamic
// security surface.
//
// The tests run against a real Mosquitto broker with the dynamic
// security plugin, because only the broker can prove that the access
// entries MCM writes are the ones the broker enforces, and that the
// management channel is really closed to the clients that must not reach
// it. A static audit cannot show that.
//
// The broker is started by the test itself. It uses the `mosquitto`
// binary when MOSQUITTO_BIN points at one, and the eclipse-mosquitto
// container image otherwise. The test is skipped when neither is
// available, or when the broker does not ship the dynamic security
// plugin.
package dynsec_test

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

	"github.com/fgjcarlos/mcm/internal/dynsec"
)

// adminUser is the account the test administers the plugin with.
const (
	adminUser     = "mcm-admin"
	adminPassword = "mcm-admin-password"
)

// broker is a running Mosquitto instance with the dynamic security
// plugin.
type broker struct {
	// addr is the address of the listener of the broker.
	addr string
	// container is the name of the container that runs the broker. It is
	// empty when the broker runs as a local process.
	container string
	// cmd is the local process that runs the broker.
	cmd *exec.Cmd
	// cancel stops the local process.
	cancel context.CancelFunc
}

// logs returns the output of the broker.
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

// readDatabase returns the content of the database of the plugin.
//
// The plugin writes the file with the permissions it wants, which the
// user of the test may not be allowed to read. The container is asked
// for the content in that case.
func (b *broker) readDatabase(t *testing.T, path string) string {
	t.Helper()
	if b.container != "" {
		out, err := exec.Command("docker", "exec", b.container, "cat", "/mosquitto/config/dynamic-security.json").CombinedOutput()
		if err != nil {
			t.Fatalf("read the database of the plugin: %v: %s", err, string(out))
		}
		return string(out)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the database of the plugin: %v", err)
	}
	return string(body)
}

// stop ends the broker.
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

// pluginModule is the path of the plugin inside the container image.
const pluginModule = "/usr/lib/mosquitto_dynamic_security.so"

// startBroker starts a Mosquitto broker with the dynamic security plugin.
//
// The plugin needs a database to start with. The test writes one that
// holds the administration account, exactly like the adoption does.
func startBroker(t *testing.T, dir string, port int) *broker {
	t.Helper()

	hash, salt, err := dynsec.HashPassword(adminPassword)
	if err != nil {
		t.Fatalf("hash the administration password: %v", err)
	}
	database := fmt.Sprintf(`{
	"clients": [{
		"username": %q,
		"textname": "MCM administration account",
		"password": %q,
		"salt": %q,
		"iterations": %d,
		"roles": [{"rolename": "mcm-management"}]
	}],
	"groups": [],
	"roles": [{
		"rolename": "mcm-management",
		"acls": [
			{"acltype": "publishClientSend", "topic": "$CONTROL/dynamic-security/#", "allow": true},
			{"acltype": "publishClientReceive", "topic": "$CONTROL/dynamic-security/#", "allow": true},
			{"acltype": "subscribePattern", "topic": "$CONTROL/dynamic-security/#", "allow": true}
		]
	}],
	"defaultACLAccess": {
		"publishClientSend": false,
		"publishClientReceive": true,
		"subscribe": false,
		"unsubscribe": true
	}
}
`, adminUser, hash, salt, dynsec.PasswordIterations)
	if err := os.WriteFile(filepath.Join(dir, "dynamic-security.json"), []byte(database), 0o644); err != nil {
		t.Fatalf("write the dynamic security database: %v", err)
	}

	body := fmt.Sprintf(`listener %d 127.0.0.1
allow_anonymous false
persistence false
plugin %s
plugin_opt_config_file /mosquitto/config/dynamic-security.json
`, port, pluginModule)

	if bin := strings.TrimSpace(os.Getenv("MOSQUITTO_BIN")); bin != "" {
		return startLocalBroker(t, dir, port, body, bin)
	}
	if _, err := exec.LookPath("docker"); err != nil {
		requireBroker(t)
		t.Skip("neither MOSQUITTO_BIN nor docker is available: the integration test needs a real broker")
	}
	return startDockerBroker(t, dir, port, body)
}

// requireBroker fails the test when no broker is available.
//
// The test skips itself when the environment has neither a mosquitto
// binary nor docker, so a developer without them is not blocked. CI sets
// MCM_REQUIRE_BROKER: there a missing broker is a broken pipeline, not a
// green run that proved nothing.
func requireBroker(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("MCM_REQUIRE_BROKER")) == "" {
		return
	}
	t.Fatal("no broker is available: set MOSQUITTO_BIN or make docker available, or unset MCM_REQUIRE_BROKER to skip")
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
// The container uses the host network, so the test reaches the broker on
// the loopback of the host.
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

	name := fmt.Sprintf("mcm-dynsec-it-%d", time.Now().UnixNano())
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

// adapterFor opens the control channel of the plugin as the
// administration account.
func adapterFor(t *testing.T, addr string) *dynsec.Adapter {
	t.Helper()
	adapter := dynsec.NewAdapter(dynsec.Options{
		Address:  addr,
		Username: adminUser,
		Password: adminPassword,
		ClientID: fmt.Sprintf("mcm-dynsec-it-%d", time.Now().UnixNano()),
		Timeout:  15 * time.Second,
	})
	if err := adapter.Connect(context.Background()); err != nil {
		t.Fatalf("connect to the control channel: %v", err)
	}
	return adapter
}

// subscribeAllowed subscribes to a topic and reports whether the broker
// granted the subscription.
//
// The broker refuses a subscription with the return code 0x80, which the
// client library reports per topic and not as an error of the call.
func subscribeAllowed(t *testing.T, client mqtt.Client, topic string) bool {
	t.Helper()
	token := client.Subscribe(topic, 1, nil)
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatalf("the subscription to %s timed out", topic)
	}
	if err := token.Error(); err != nil {
		t.Fatalf("subscribe to %s: %v", topic, err)
	}
	sub, ok := token.(*mqtt.SubscribeToken)
	if !ok {
		t.Fatalf("the subscription to %s returned an unexpected token", topic)
	}
	code, found := sub.Result()[topic]
	if !found {
		return false
	}
	return code != 0x80
}

// connect opens an MQTT connection to the broker.
func connect(t *testing.T, addr, username, password string) (mqtt.Client, error) {
	t.Helper()
	options := mqtt.NewClientOptions().
		AddBroker("tcp://" + addr).
		SetClientID(fmt.Sprintf("mcm-dynsec-it-%d", time.Now().UnixNano())).
		SetConnectTimeout(10 * time.Second)
	if username != "" {
		options.SetUsername(username)
		options.SetPassword(password)
	}
	client := mqtt.NewClient(options)
	token := client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return nil, fmt.Errorf("the connection to %s timed out", addr)
	}
	if err := token.Error(); err != nil {
		return nil, err
	}
	return client, nil
}

// managementRole is the role that keeps the management channel reachable by
// the administration account.
func managementRole() dynsec.Role {
	return dynsec.Role{
		RoleName: "mcm-management",
		TextName: "Management channel of MCM",
		ACLs: []dynsec.ACL{
			{Type: dynsec.ACLPublishClientSend, Topic: "$CONTROL/dynamic-security/#", Allow: true},
			{Type: dynsec.ACLPublishClientReceive, Topic: "$CONTROL/dynamic-security/#", Allow: true},
			{Type: dynsec.ACLSubscribePattern, Topic: "$CONTROL/dynamic-security/#", Allow: true},
		},
	}
}

// managementClient is the administration account of the desired state. It
// has to keep its role, otherwise nobody could administer the plugin any
// more.
func managementClient() dynsec.Client {
	return dynsec.Client{
		Username: adminUser,
		Roles:    []dynsec.RoleRef{{RoleName: "mcm-management"}},
	}
}

// TestAdapterAppliesTheSecurityDatabase proves that the adapter reaches
// the plugin and that the answers are verified.
func TestAdapterAppliesTheSecurityDatabase(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)

	adapter := adapterFor(t, b.addr)
	defer adapter.Close()

	service := dynsec.NewService(adapter)
	ctx := context.Background()

	// The administration account is the one the database starts with.
	current, err := service.Read(ctx)
	if err != nil {
		t.Fatalf("read the security database: %v", err)
	}
	if _, ok := current.FindClient(adminUser); !ok {
		t.Fatalf("the administration account is missing: %+v", current.Clients)
	}

	desired := dynsec.Document{
		Roles: []dynsec.Role{
			managementRole(),
			{
				RoleName: "reader",
				TextName: "Read the sensors",
				ACLs: []dynsec.ACL{
					{Type: dynsec.ACLPublishClientReceive, Topic: "sensors/#", Allow: true},
					{Type: dynsec.ACLSubscribePattern, Topic: "sensors/#", Allow: true},
				},
			},
		},
		Clients: []dynsec.Client{
			{
				Username: adminUser,
				Roles:    []dynsec.RoleRef{{RoleName: "mcm-management"}},
			},
			{
				Username: "alice",
				Password: "alice-password",
				Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
			},
		},
		Groups: []dynsec.Group{{
			GroupName: "team",
			Roles:     []dynsec.RoleRef{{RoleName: "reader"}},
			Clients:   []dynsec.ClientRef{{Username: "alice"}},
		}},
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
	}

	desired.Normalize()
	plan := dynsec.BuildPlan(current, desired)
	if plan.Empty() {
		t.Fatal("the plan does not change anything")
	}
	actual, err := service.Apply(ctx, plan, desired)
	if err != nil {
		t.Fatalf("apply the plan: %v", err)
	}

	client, ok := actual.FindClient("alice")
	if !ok {
		t.Fatal("the client alice is missing after the change")
	}
	if len(client.Roles) != 1 || client.Roles[0].RoleName != "reader" {
		t.Fatalf("the roles of alice are %+v", client.Roles)
	}
	role, ok := actual.FindRole("reader")
	if !ok {
		t.Fatal("the role reader is missing after the change")
	}
	if len(role.ACLs) != 2 {
		t.Fatalf("the access entries of the role are %+v", role.ACLs)
	}
	group, ok := actual.FindGroup("team")
	if !ok {
		t.Fatal("the group team is missing after the change")
	}
	if len(group.Clients) != 1 || group.Clients[0].Username != "alice" {
		t.Fatalf("the clients of the group are %+v", group.Clients)
	}
}

// TestTheBrokerEnforcesTheAccessEntries proves that the access entries
// MCM writes are the ones the broker enforces: alice may read the
// sensors and may not reach the management channel.
func TestTheBrokerEnforcesTheAccessEntries(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)

	adapter := adapterFor(t, b.addr)
	defer adapter.Close()

	service := dynsec.NewService(adapter)
	ctx := context.Background()

	current, err := service.Read(ctx)
	if err != nil {
		t.Fatalf("read the security database: %v", err)
	}
	desired := dynsec.Document{
		Roles: []dynsec.Role{
			managementRole(),
			{
				RoleName: "reader",
				ACLs: []dynsec.ACL{
					{Type: dynsec.ACLPublishClientReceive, Topic: "sensors/#", Allow: true},
					{Type: dynsec.ACLSubscribePattern, Topic: "sensors/#", Allow: true},
				},
			},
			{
				RoleName: "writer",
				ACLs: []dynsec.ACL{
					{Type: dynsec.ACLPublishClientSend, Topic: "sensors/#", Allow: true},
				},
			},
		},
		Clients: []dynsec.Client{
			{
				Username: adminUser,
				Roles:    []dynsec.RoleRef{{RoleName: "mcm-management"}},
			},
			{
				Username: "alice",
				Password: "alice-password",
				Roles:    []dynsec.RoleRef{{RoleName: "reader"}},
			},
			{
				Username: "bob",
				Password: "bob-password",
				Roles:    []dynsec.RoleRef{{RoleName: "writer"}},
			},
		},
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
	}
	desired.Normalize()
	plan := dynsec.BuildPlan(current, desired)
	if _, err := service.Apply(ctx, plan, desired); err != nil {
		t.Fatalf("apply the plan: %v", err)
	}

	// Alice may subscribe to the sensors.
	alice, err := connect(t, b.addr, "alice", "alice-password")
	if err != nil {
		t.Fatalf("alice cannot connect: %v", err)
	}
	defer alice.Disconnect(250)

	token := alice.Subscribe("sensors/#", 1, nil)
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatal("the subscription of alice timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("alice cannot subscribe to the sensors: %v", err)
	}

	// Alice may not reach the management channel.
	if subscribeAllowed(t, alice, dynsec.ControlTopic+"/response") {
		t.Fatal("alice may reach the management channel of the plugin")
	}

	// Bob may publish to the sensors but may not subscribe to them.
	bob, err := connect(t, b.addr, "bob", "bob-password")
	if err != nil {
		t.Fatalf("bob cannot connect: %v", err)
	}
	defer bob.Disconnect(250)

	token = bob.Publish("sensors/one", 1, false, "value")
	if !token.WaitTimeout(10 * time.Second) {
		t.Fatal("the publish of bob timed out")
	}
	if err := token.Error(); err != nil {
		t.Fatalf("bob cannot publish to the sensors: %v", err)
	}

	if subscribeAllowed(t, bob, "sensors/#") {
		t.Fatal("bob may subscribe to the sensors")
	}

	// Bob may not reach the management channel either.
	if subscribeAllowed(t, bob, dynsec.ControlTopic+"/response") {
		t.Fatal("bob may reach the management channel of the plugin")
	}
}

// TestTheManagementChannelIsClosedToForeignClients proves that the guard
// of MCM finds a client that may reach the management channel, and that
// the broker refuses the subscription of that client.
func TestTheManagementChannelIsClosedToForeignClients(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)

	adapter := adapterFor(t, b.addr)
	defer adapter.Close()

	service := dynsec.NewService(adapter)
	ctx := context.Background()

	current, err := service.Read(ctx)
	if err != nil {
		t.Fatalf("read the security database: %v", err)
	}

	// A role that opens the management channel to every client.
	desired := dynsec.Document{
		Roles: []dynsec.Role{
			managementRole(),
			{
				RoleName: "open",
				ACLs: []dynsec.ACL{
					{Type: dynsec.ACLPublishClientSend, Topic: dynsec.ControlTopicPrefix + "#", Allow: true},
					{Type: dynsec.ACLSubscribePattern, Topic: dynsec.ControlTopicPrefix + "#", Allow: true},
				},
			},
		},
		Clients: []dynsec.Client{
			{
				Username: adminUser,
				Roles:    []dynsec.RoleRef{{RoleName: "mcm-management"}},
			},
			{
				Username: "intruder",
				Password: "intruder-password",
				Roles:    []dynsec.RoleRef{{RoleName: "open"}},
			},
		},
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
	}
	desired.Normalize()
	plan := dynsec.BuildPlan(current, desired)
	if _, err := service.Apply(ctx, plan, desired); err != nil {
		t.Fatalf("apply the plan: %v", err)
	}

	// The guard of MCM finds the client.
	actual, err := service.Read(ctx)
	if err != nil {
		t.Fatalf("read the security database: %v", err)
	}
	report := dynsec.CheckControlChannel(actual, adminUser)
	if report.OK {
		t.Fatal("the guard did not find the client that may reach the management channel")
	}
	if len(report.Violations) != 1 || report.Violations[0] != "intruder" {
		t.Fatalf("violations = %v, want [intruder]", report.Violations)
	}

	// The broker really lets that client reach the channel, which is why
	// the guard matters.
	intruder, err := connect(t, b.addr, "intruder", "intruder-password")
	if err != nil {
		t.Fatalf("the intruder cannot connect: %v", err)
	}
	defer intruder.Disconnect(250)
	if !subscribeAllowed(t, intruder, dynsec.ResponseTopic) {
		t.Fatal("the intruder cannot reach the management channel")
	}
}

// TestARefusedCommandIsReported proves that a command the plugin refuses
// is reported instead of being assumed to have worked.
func TestARefusedCommandIsReported(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)

	adapter := adapterFor(t, b.addr)
	defer adapter.Close()

	ctx := context.Background()
	responses, err := adapter.Do(ctx, dynsec.Command{Name: dynsec.CommandCreateClient, Username: "alice"})
	if err != nil {
		t.Fatalf("send the command: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("the plugin answered %d times", len(responses))
	}
	if responses[0].Failed() {
		t.Fatalf("the plugin refused the first client: %s", responses[0].Error)
	}

	// The second client with the same name is refused by the plugin.
	responses, err = adapter.Do(ctx, dynsec.Command{Name: dynsec.CommandCreateClient, Username: "alice"})
	if err != nil {
		t.Fatalf("send the command: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("the plugin answered %d times", len(responses))
	}
	if !responses[0].Failed() {
		t.Fatal("the refusal of the plugin was not reported")
	}
	if responses[0].Err() == nil {
		t.Fatal("the refusal carries no error")
	}
}

// TestThePluginWritesItsDatabase proves that the plugin keeps its own
// database: a change that goes through the control channel is written
// back to the file.
func TestThePluginWritesItsDatabase(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)

	adapter := adapterFor(t, b.addr)
	defer adapter.Close()

	service := dynsec.NewService(adapter)
	ctx := context.Background()

	current, err := service.Read(ctx)
	if err != nil {
		t.Fatalf("read the security database: %v", err)
	}
	desired := dynsec.Document{
		Roles: []dynsec.Role{managementRole()},
		Clients: []dynsec.Client{
			{
				Username: adminUser,
				Roles:    []dynsec.RoleRef{{RoleName: "mcm-management"}},
			},
			{
				Username: "alice",
				Password: "alice-password",
			},
		},
		DefaultAccess: current.DefaultAccess,
	}
	desired.Normalize()
	plan := dynsec.BuildPlan(current, desired)
	if _, err := service.Apply(ctx, plan, desired); err != nil {
		t.Fatalf("apply the plan: %v", err)
	}

	path := filepath.Join(dir, "dynamic-security.json")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if strings.Contains(b.readDatabase(t, path), "alice") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin did not write the client to %s", path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestTheClientIsAddedToItsGroupOnlyOnce proves that the plan of a new
// client that belongs to a group does not add it to the group twice.
//
// The plugin refuses a client that is added to a group it already
// belongs to, so a plan that creates the client with its group and adds
// it to the group afterwards is refused by the broker.
func TestTheClientIsAddedToItsGroupOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	port := freePort(t)
	b := startBroker(t, dir, port)
	defer b.stop(t)

	adapter := adapterFor(t, b.addr)
	defer adapter.Close()

	service := dynsec.NewService(adapter)
	ctx := context.Background()

	current, err := service.Read(ctx)
	if err != nil {
		t.Fatalf("read the security database: %v", err)
	}
	management, ok := current.FindRole("mcm-management")
	if !ok {
		t.Fatal("the management role is missing")
	}
	admin, ok := current.FindClient(adminUser)
	if !ok {
		t.Fatal("the administration account is missing")
	}

	desired := dynsec.Document{
		Roles: []dynsec.Role{
			management,
			{
				RoleName: "field-reader",
				TextName: "Read the sensors of the field",
				ACLs: []dynsec.ACL{
					{Type: dynsec.ACLSubscribePattern, Topic: "sensors/+/state", Allow: true},
				},
			},
		},
		Clients: []dynsec.Client{
			admin,
			{Username: "sensor-b", Password: "sensor-b-password"},
		},
		Groups: []dynsec.Group{{
			GroupName: "field-team",
			TextName:  "The field team",
			Roles:     []dynsec.RoleRef{{RoleName: "field-reader"}},
			Clients:   []dynsec.ClientRef{{Username: "sensor-b"}},
		}},
		DefaultAccess: dynsec.DefaultAccess{
			PublishClientSend:    false,
			PublishClientReceive: true,
			Subscribe:            false,
			Unsubscribe:          true,
		},
	}
	desired.Normalize()

	plan := dynsec.BuildPlan(current, desired)
	if plan.Empty() {
		t.Fatal("the plan does not change anything")
	}
	// The membership of the group is set by one command only.
	var adds int
	for _, change := range plan.Changes {
		for _, command := range change.Commands {
			if command.Name == dynsec.CommandAddGroupClient {
				adds++
			}
			if command.Name == dynsec.CommandCreateClient && len(command.Groups) > 0 {
				t.Errorf("the client must not be created with its groups: %+v", command)
			}
		}
	}
	if adds != 1 {
		t.Errorf("the client is added to the group %d times, want 1: %v", adds, plan.Summaries())
	}

	actual, err := service.Apply(ctx, plan, desired)
	if err != nil {
		t.Fatalf("apply the plan: %v", err)
	}
	group, ok := actual.FindGroup("field-team")
	if !ok {
		t.Fatal("the group field-team is missing after the change")
	}
	if len(group.Clients) != 1 || group.Clients[0].Username != "sensor-b" {
		t.Fatalf("the clients of the group are %+v", group.Clients)
	}
}
