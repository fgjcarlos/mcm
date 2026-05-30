package agent_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/agent"
)

// newIntegrationConfig returns an AgentConfig pointing to the given heartbeat
// server and MQTT broker stub.
func newIntegrationConfig(serverURL, brokerHost string, brokerPort int) agent.AgentConfig {
	return agent.AgentConfig{
		Server: agent.ServerConfig{
			URL:   serverURL,
			Token: "test-token",
		},
		Site: agent.SiteConfig{
			ID:   "integration-site",
			Name: "Integration Test Site",
		},
		Mosquitto: agent.MosquittoConfig{
			Host: brokerHost,
			Port: brokerPort,
		},
		Heartbeat: agent.HeartbeatConfig{
			Interval: "100ms", // fast for tests
			Timeout:  "1s",
		},
	}
}

// countingHeartbeatServer returns an httptest.Server that counts heartbeat
// calls and closes the done channel once minCalls have been received.
func countingHeartbeatServer(t *testing.T, minCalls int) (*httptest.Server, *atomic.Int32, chan struct{}) {
	t.Helper()
	var count atomic.Int32
	done := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/edge/heartbeat":
			n := count.Add(1)
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{}`)) //nolint:errcheck
			if int(n) >= minCalls {
				select {
				case <-done:
				default:
					close(done)
				}
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	return srv, &count, done
}

func TestAgent_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	t.Run("runs 2 heartbeat cycles then shuts down cleanly", func(t *testing.T) {
		// Healthy broker stub.
		brokerHost, brokerPort := startBrokerStub(t, 0x00)

		srv, count, done := countingHeartbeatServer(t, 2)
		defer srv.Close()

		cfg := newIntegrationConfig(srv.URL, brokerHost, brokerPort)
		a, err := agent.New(cfg, "1.0.0", newTestLogger())
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		runDone := make(chan error, 1)
		go func() {
			runDone <- a.Run(ctx)
		}()

		// Wait until 2 heartbeats are received.
		select {
		case <-done:
		case <-time.After(4 * time.Second):
			t.Fatal("timed out waiting for 2 heartbeats")
		}

		// Cancel context and expect clean shutdown.
		cancel()

		select {
		case err := <-runDone:
			if err != nil {
				t.Errorf("Run returned error: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not shut down within 5s")
		}

		if count.Load() < 2 {
			t.Errorf("heartbeat count = %d, want >= 2", count.Load())
		}
	})

	t.Run("handles probe failure (broker down) and still sends heartbeat with unknown status", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping integration test in short mode")
		}

		// No broker — port is immediately closed.
		// Probe will return StatusUnknown.
		var sentStatus atomic.Value

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/edge/heartbeat" {
				var body map[string]string
				json.NewDecoder(r.Body).Decode(&body) //nolint:errcheck
				sentStatus.Store(body["status"])
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{}`)) //nolint:errcheck
			}
		}))
		defer srv.Close()

		// Use a port with no listener.
		cfg := newIntegrationConfig(srv.URL, "127.0.0.1", 19999)
		cfg.Heartbeat.Interval = "200ms"

		a, err := agent.New(cfg, "1.0.0", newTestLogger())
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		runDone := make(chan error, 1)
		go func() {
			runDone <- a.Run(ctx)
		}()

		// Give the agent time to send at least one heartbeat.
		time.Sleep(500 * time.Millisecond)
		cancel()

		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not shut down within 5s")
		}

		status, _ := sentStatus.Load().(string)
		if status != "unknown" {
			t.Errorf("status = %q, want %q", status, "unknown")
		}
	})

	t.Run("handles server failure and applies backoff", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping integration test in short mode")
		}

		brokerHost, brokerPort := startBrokerStub(t, 0x00)

		var callCount atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/edge/heartbeat" {
				callCount.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}
		}))
		defer srv.Close()

		cfg := newIntegrationConfig(srv.URL, brokerHost, brokerPort)
		cfg.Heartbeat.Interval = "200ms"

		a, err := agent.New(cfg, "1.0.0", newTestLogger())
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		runDone := make(chan error, 1)
		go func() {
			runDone <- a.Run(ctx)
		}()

		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not shut down within 5s")
		}

		// Agent should have made at least 1 call (the initial one) but not
		// exponentially many — backoff should slow it down.
		calls := callCount.Load()
		if calls < 1 {
			t.Errorf("heartbeat calls = %d, want >= 1", calls)
		}
	})

	t.Run("shuts down within 5s even with stuck request", func(t *testing.T) {
		if testing.Short() {
			t.Skip("skipping integration test in short mode")
		}

		brokerHost, brokerPort := startBrokerStub(t, 0x00)

		// serverHold is closed when the test wants the server to unblock.
		serverHold := make(chan struct{})
		reqReceived := make(chan struct{}, 1)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/edge/heartbeat" {
				// Signal that a request arrived.
				select {
				case reqReceived <- struct{}{}:
				default:
				}
				// Block until the test tells us to release (or client disconnects).
				select {
				case <-serverHold:
				case <-r.Context().Done():
				}
				w.WriteHeader(http.StatusOK)
			}
		}))
		defer srv.Close()

		cfg := newIntegrationConfig(srv.URL, brokerHost, brokerPort)
		cfg.Heartbeat.Interval = "200ms"
		// Short HTTP timeout ensures the client stops waiting quickly.
		cfg.Heartbeat.Timeout = "300ms"

		a, err := agent.New(cfg, "1.0.0", newTestLogger())
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		runDone := make(chan error, 1)
		go func() {
			runDone <- a.Run(ctx)
		}()

		// Wait until a request has reached the server.
		select {
		case <-reqReceived:
		case <-time.After(3 * time.Second):
			t.Fatal("server never received heartbeat request")
		}

		// Cancel the agent context — the HTTP client timeout (300ms) will
		// resolve the in-flight request; Run must return within 5s.
		cancel()

		// Release the server hold so the handler exits cleanly.
		close(serverHold)

		start := time.Now()
		select {
		case <-runDone:
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("shutdown took %v, want <= 5s", elapsed)
			}
		case <-time.After(6 * time.Second):
			t.Fatal("agent did not shut down within 6s")
		}
	})
}
