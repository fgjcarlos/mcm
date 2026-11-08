package deploy

import (
	"bytes"
	"context"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	mqtt "github.com/mochi-mqtt/server/v2"
	mqttauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// staleConfigAuthHook serves the previous configuration until the reload
// settles.
//
// Mosquitto processes SIGHUP asynchronously in its event loop, so a
// verifier that runs right after the signal can still meet the previous
// policy. The hook models that race: the broker stays reachable the whole
// time, but it keeps serving the previous configuration until the reload
// settles.
type staleConfigAuthHook struct {
	mqtt.HookBase
	mu       sync.Mutex
	previous *mqttauth.Ledger
	current  *mqttauth.Ledger
	settled  bool
}

// ID returns the ID of the hook.
func (h *staleConfigAuthHook) ID() string { return "stale-config-auth" }

// Provides reports the hook methods the broker calls.
func (h *staleConfigAuthHook) Provides(b byte) bool {
	return bytes.Contains([]byte{mqtt.OnConnectAuthenticate, mqtt.OnACLCheck}, []byte{b})
}

// Init configures the hook. The ledgers are injected by the test.
func (h *staleConfigAuthHook) Init(_ any) error { return nil }

// SettleReload marks the reload as processed: the broker serves the new
// configuration from now on.
func (h *staleConfigAuthHook) SettleReload() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.settled = true
}

// OnConnectAuthenticate authenticates the client against the
// configuration the broker serves at this moment.
func (h *staleConfigAuthHook) OnConnectAuthenticate(cl *mqtt.Client, pk packets.Packet) bool {
	ledger := h.ledgerForAttempt()
	_, ok := ledger.AuthOk(cl, pk)
	return ok
}

// OnACLCheck authorizes the topic against the configuration the broker
// serves at this moment.
func (h *staleConfigAuthHook) OnACLCheck(cl *mqtt.Client, topic string, write bool) bool {
	ledger := h.ledgerForAttempt()
	_, ok := ledger.ACLOk(cl, topic, write)
	return ok
}

// ledgerForAttempt returns the ledger the broker serves at this moment.
func (h *staleConfigAuthHook) ledgerForAttempt() *mqttauth.Ledger {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.settled {
		return h.previous
	}
	return h.current
}

// staleConfigBroker is an in-process MQTT broker that serves the previous
// configuration until the reload settles.
type staleConfigBroker struct {
	hook   *staleConfigAuthHook
	server *mqtt.Server
	addr   string
}

// startStaleConfigBroker launches the broker and returns it together with
// its address.
func startStaleConfigBroker(t *testing.T, previous, current *mqttauth.Ledger) *staleConfigBroker {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	server := mqtt.New(&mqtt.Options{InlineClient: true, SysTopicResendInterval: 3600})
	hook := &staleConfigAuthHook{previous: previous, current: current}
	if err := server.AddHook(hook, nil); err != nil {
		t.Fatalf("AddHook: %v", err)
	}
	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "stale-tcp-" + addr, Address: addr})); err != nil {
		t.Fatalf("AddListener: %v", err)
	}
	go func() {
		_ = server.Serve()
	}()
	waitForStaleConfigBroker(t, addr)

	t.Cleanup(func() {
		time.Sleep(50 * time.Millisecond)
		_ = server.Close()
	})
	return &staleConfigBroker{hook: hook, server: server, addr: addr}
}

// waitForStaleConfigBroker waits until the broker accepts connections.
func waitForStaleConfigBroker(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the broker did not become ready at %s", addr)
}

// settlingVerifier runs the real verifier and settles the reload of the
// test broker once the first attempt has observed the previous
// configuration.
//
// The settle happens between two attempts of the bounded retry loop, like
// a broker that processes the reload signal while MCM waits for the next
// attempt.
type settlingVerifier struct {
	inner    diagnostics.VerifierFunc
	hook     *staleConfigAuthHook
	mu       sync.Mutex
	attempts int
}

// VerifyActive runs the real verifier and settles the reload after the
// first attempt.
func (v *settlingVerifier) VerifyActive(ctx context.Context, opts diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	result := v.inner(ctx, opts)
	v.mu.Lock()
	v.attempts++
	first := v.attempts == 1
	v.mu.Unlock()
	if first {
		v.hook.SettleReload()
	}
	return result
}

// callCount returns how many attempts the verifier ran.
func (v *settlingVerifier) callCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.attempts
}

// ledgerWithUser builds an authentication ledger with one user and its
// topic permissions.
func ledgerWithUser(username, password string, allowed, denied []string) *mqttauth.Ledger {
	filters := mqttauth.Filters{}
	for _, topic := range allowed {
		filters[mqttauth.RString(topic)] = mqttauth.ReadWrite
	}
	for _, topic := range denied {
		filters[mqttauth.RString(topic)] = mqttauth.Deny
	}
	return &mqttauth.Ledger{
		Users: mqttauth.Users{
			username: mqttauth.UserRule{
				Username: mqttauth.RString(username),
				Password: mqttauth.RString(password),
				ACL:      filters,
			},
		},
	}
}

// TestApply_ReachesActiveVerifiedWhenTheBrokerServesThePreviousConfigurationUntilTheReloadSettles
// covers issue #293 acceptance criterion 4 with a real broker: the broker
// is reachable the whole time, but it serves the previous configuration
// until the reload settles. The deploy must not declare the change active
// on the first observation, and the bounded retries must recover once the
// new configuration is served.
func TestApply_ReachesActiveVerifiedWhenTheBrokerServesThePreviousConfigurationUntilTheReloadSettles(t *testing.T) {
	t.Parallel()

	// The previous configuration knows another user only: the probe
	// identity of the new configuration does not exist yet.
	previous := ledgerWithUser("old-user", "old-pass", []string{"old/topic"}, nil)
	current := ledgerWithUser("probe-user", "probe-secret",
		[]string{"sensors/temperature"}, []string{"sensors/forbidden"})
	broker := startStaleConfigBroker(t, previous, current)

	host, port := splitStaleConfigHostPort(t, broker.addr)

	deployCfg, aclPath, passwdPath := enabledDeployCfg(t)
	writeFile(t, aclPath, "old acl")
	writeFile(t, passwdPath, "olduser:legacy-hash\n")

	hash, err := mosquitto.HashPassword("probe-secret", mosquitto.DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	aclStore := &fakeACLStore{rules: []acl.Rule{
		{Principal: "probe-user", TopicFilter: "sensors/temperature", Permission: acl.PermissionReadWrite},
		{Principal: "probe-user", TopicFilter: "sensors/forbidden", Permission: acl.PermissionDeny},
	}}
	mqttStore := &fakeMQTTUserLister{users: []storage.MQTTUser{
		{Username: "probe-user", PasswordHash: hash},
	}}
	lookup := newFakePasswordLookup()
	lookup.remember("probe-user", "probe-secret")

	verifier := &settlingVerifier{
		inner: diagnostics.VerifierFunc(diagnostics.VerifyActive),
		hook:  broker.hook,
	}
	svc := NewService(
		&fakeApplier{},
		aclStore,
		mqttStore,
		newFakeDeploymentStore(),
		verifier,
		lookup,
		config.MosquittoConfig{Host: host, Port: port},
		deployCfg,
		noAudit,
	)

	deployment, err := applyPreview(t, svc, context.Background(), "operator")
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if deployment.Status != "active_verified" {
		t.Fatalf("status = %q, want active_verified (message: %s)", deployment.Status, deployment.Message)
	}
	if verifier.callCount() != 2 {
		t.Errorf("the verifier ran %d times, want 2 (one attempt on the previous configuration, one after the reload settled)", verifier.callCount())
	}
	if !containsText(deployment.Message, "verified after 2 attempts") {
		t.Errorf("message = %q, want it to report that the verification needed the retry", deployment.Message)
	}
}

// splitStaleConfigHostPort splits the address of the test broker.
func splitStaleConfigHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return host, port
}

// containsText reports whether the text holds the needle.
func containsText(text, needle string) bool {
	return bytes.Contains([]byte(text), []byte(needle))
}
