package diagnostics

import (
	"net"
	"sync"
	"testing"
	"time"

	mqttserver "github.com/mochi-mqtt/server/v2"
	mqttauth "github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
)

// mochiCloser closes a mochi server exactly once even if the test code also closed it.
type mochiCloser struct {
	server *mqttserver.Server
	once   sync.Once
}

func (c *mochiCloser) Close() {
	c.once.Do(func() {
		_ = c.server.Close()
	})
}

// verifyBrokerACL describes the ACL rules applied to a test broker.
// Each user has a list of allowed topic filters (readwrite) and a list
// of denied topic filters (Deny). Subscriptions or publishes to any
// other topic are allowed by default — mochi's auth ledger does not
// implement default-deny at the topic level, so the test setup must
// explicitly deny the topics the verifier expects to be rejected.
type verifyBrokerACL struct {
	users map[string]verifyUserACL
}

type verifyUserACL struct {
	allowed []string
	denied  []string
}

// startVerifyBroker launches an in-process MQTT broker configured with
// the given ACL ledger. Returns the closer and the broker address
// (host:port).
func startVerifyBroker(t *testing.T, acl verifyBrokerACL) (*mochiCloser, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	server := mqttserver.New(&mqttserver.Options{InlineClient: true})

	// Build the auth ledger from the test ACL.
	users := mqttauth.Users{}
	for username, userACL := range acl.users {
		filters := mqttauth.Filters{}
		for _, f := range userACL.allowed {
			filters[mqttauth.RString(f)] = mqttauth.ReadWrite
		}
		for _, f := range userACL.denied {
			filters[mqttauth.RString(f)] = mqttauth.Deny
		}
		users[username] = mqttauth.UserRule{
			Username: mqttauth.RString(username),
			Password: mqttauth.RString("test-pass-" + username),
			ACL:      filters,
		}
	}
	if err := server.AddHook(new(mqttauth.Hook), &mqttauth.Options{
		Ledger: &mqttauth.Ledger{Users: users},
	}); err != nil {
		t.Fatalf("AddHook(auth): %v", err)
	}

	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "verify-tcp-" + addr, Address: addr})); err != nil {
		t.Fatalf("AddListener: %v", err)
	}

	go func() {
		_ = server.Serve()
	}()

	waitForVerifyBrokerReady(t, addr)
	closer := &mochiCloser{server: server}
	t.Cleanup(closer.Close)
	return closer, addr
}

func waitForVerifyBrokerReady(t *testing.T, addr string) {
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
	t.Fatalf("verify broker did not become ready at %s", addr)
}

// startNoACLBroker launches a mochi broker with NO auth hook — every
// connection is accepted regardless of username/password. Used to
// simulate the "broker is serving the OLD config" failure mode where
// the new passwd file has not yet been loaded.
func startNoACLBroker(t *testing.T) (*mochiCloser, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	server := mqttserver.New(&mqttserver.Options{InlineClient: true})
	// Intentionally NO auth hook — this simulates a broker that does not
	// enforce the rendered passwd file.
	if err := server.AddListener(listeners.NewTCP(listeners.Config{ID: "no-acl-tcp-" + addr, Address: addr})); err != nil {
		t.Fatalf("AddListener: %v", err)
	}
	go func() {
		_ = server.Serve()
	}()

	waitForVerifyBrokerReady(t, addr)
	closer := &mochiCloser{server: server}
	t.Cleanup(closer.Close)
	return closer, addr
}
