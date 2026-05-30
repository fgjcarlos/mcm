package agent_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/agent"
)

// heartbeatRequest is the JSON body sent to the heartbeat endpoint.
type heartbeatRequest struct {
	SiteID  string `json:"site_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// loginRequest is the JSON body sent to the auth/login endpoint.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// newTestLogger returns a no-op logger for tests.
func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// heartbeatServerStub is a configurable test HTTP server that handles
// heartbeat and auth/login requests.
type heartbeatServerStub struct {
	heartbeatCode    int
	heartbeatCalls   int
	heartbeatBody    *heartbeatRequest
	heartbeatHeaders http.Header

	loginCode  int
	loginCalls int
	loginBody  *loginRequest
	loginToken string

	// After firstHeartbeatCode is served, serve heartbeatCode for subsequent calls.
	firstHeartbeatCode int
	callCount          int
}

func (s *heartbeatServerStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/v1/edge/heartbeat":
		s.callCount++
		s.heartbeatCalls++
		s.heartbeatHeaders = r.Header.Clone()

		body, _ := io.ReadAll(r.Body)
		var req heartbeatRequest
		_ = json.Unmarshal(body, &req)
		s.heartbeatBody = &req

		code := s.heartbeatCode
		if s.firstHeartbeatCode != 0 && s.callCount == 1 {
			code = s.firstHeartbeatCode
		}

		w.WriteHeader(code)
		if code == http.StatusOK {
			w.Write([]byte(`{}`)) //nolint:errcheck
		}

	case "/api/v1/auth/login":
		s.loginCalls++

		body, _ := io.ReadAll(r.Body)
		var req loginRequest
		_ = json.Unmarshal(body, &req)
		s.loginBody = &req

		w.WriteHeader(s.loginCode)
		if s.loginCode == http.StatusOK {
			resp := map[string]string{"token": s.loginToken}
			json.NewEncoder(w).Encode(resp) //nolint:errcheck
		} else {
			w.Write([]byte(`{"error":"invalid credentials"}`)) //nolint:errcheck
		}

	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func newHeartbeatConfig(serverURL string) agent.AgentConfig {
	return agent.AgentConfig{
		Server: agent.ServerConfig{
			URL:      serverURL,
			Username: "admin",
			Password: "secret",
		},
		Site: agent.SiteConfig{
			ID:   "site-001",
			Name: "Test Site",
		},
		Mosquitto: agent.MosquittoConfig{
			Host: "127.0.0.1",
			Port: 1883,
		},
		Heartbeat: agent.HeartbeatConfig{
			Interval: "60s",
			Timeout:  "5s",
		},
	}
}

func TestHeartbeatClient_Send(t *testing.T) {
	t.Run("send succeeds with 200 - verify body and headers", func(t *testing.T) {
		stub := &heartbeatServerStub{
			heartbeatCode: http.StatusOK,
			loginToken:    "tok-abc",
			loginCode:     http.StatusOK,
		}
		srv := httptest.NewServer(stub)
		defer srv.Close()

		cfg := newHeartbeatConfig(srv.URL)
		// Pre-set token so no initial login needed.
		cfg.Server.Token = "static-token"
		cfg.Server.Username = ""
		cfg.Server.Password = ""

		client := agent.NewHeartbeatClient(cfg, "1.2.3", newTestLogger())
		result := agent.ProbeResult{
			Status:  agent.StatusHealthy,
			Message: "broker is up",
		}

		err := client.Send(context.Background(), result)
		if err != nil {
			t.Fatalf("Send: unexpected error: %v", err)
		}

		if stub.heartbeatCalls != 1 {
			t.Errorf("heartbeat calls = %d, want 1", stub.heartbeatCalls)
		}

		// Verify Authorization header.
		auth := stub.heartbeatHeaders.Get("Authorization")
		if auth != "Bearer static-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer static-token")
		}

		// Verify Content-Type.
		ct := stub.heartbeatHeaders.Get("Content-Type")
		if !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		// Verify request body fields.
		if stub.heartbeatBody == nil {
			t.Fatal("heartbeat body not captured")
		}
		if stub.heartbeatBody.SiteID != "site-001" {
			t.Errorf("site_id = %q, want %q", stub.heartbeatBody.SiteID, "site-001")
		}
		if stub.heartbeatBody.Name != "Test Site" {
			t.Errorf("name = %q, want %q", stub.heartbeatBody.Name, "Test Site")
		}
		if stub.heartbeatBody.Version != "1.2.3" {
			t.Errorf("version = %q, want %q", stub.heartbeatBody.Version, "1.2.3")
		}
		if stub.heartbeatBody.Status != "healthy" {
			t.Errorf("status = %q, want %q", stub.heartbeatBody.Status, "healthy")
		}
		if stub.heartbeatBody.Message != "broker is up" {
			t.Errorf("message = %q, want %q", stub.heartbeatBody.Message, "broker is up")
		}
	})

	t.Run("send re-auths on 401 then retries successfully", func(t *testing.T) {
		stub := &heartbeatServerStub{
			// First call returns 401, subsequent calls return 200.
			firstHeartbeatCode: http.StatusUnauthorized,
			heartbeatCode:      http.StatusOK,
			loginCode:          http.StatusOK,
			loginToken:         "new-token",
		}
		srv := httptest.NewServer(stub)
		defer srv.Close()

		cfg := newHeartbeatConfig(srv.URL)
		// No static token — must use username/password.
		cfg.Server.Token = ""

		client := agent.NewHeartbeatClient(cfg, "1.0.0", newTestLogger())
		result := agent.ProbeResult{Status: agent.StatusHealthy, Message: "ok"}

		err := client.Send(context.Background(), result)
		if err != nil {
			t.Fatalf("Send: unexpected error after re-auth: %v", err)
		}

		if stub.loginCalls < 1 {
			t.Errorf("login calls = %d, want >= 1", stub.loginCalls)
		}
		if stub.heartbeatCalls != 2 {
			t.Errorf("heartbeat calls = %d, want 2 (first 401 + retry)", stub.heartbeatCalls)
		}
	})

	t.Run("send with static token does NOT re-auth on 401 - returns error", func(t *testing.T) {
		stub := &heartbeatServerStub{
			heartbeatCode: http.StatusUnauthorized,
			loginCode:     http.StatusOK,
			loginToken:    "new-token",
		}
		srv := httptest.NewServer(stub)
		defer srv.Close()

		cfg := newHeartbeatConfig(srv.URL)
		// Static token only — no username/password for re-auth.
		cfg.Server.Token = "static-only-token"
		cfg.Server.Username = ""
		cfg.Server.Password = ""

		client := agent.NewHeartbeatClient(cfg, "1.0.0", newTestLogger())
		result := agent.ProbeResult{Status: agent.StatusHealthy, Message: "ok"}

		err := client.Send(context.Background(), result)
		if err == nil {
			t.Fatal("Send: expected error for 401 with static token, got nil")
		}

		// Must NOT have called login.
		if stub.loginCalls != 0 {
			t.Errorf("login calls = %d, want 0 (no re-auth with static token)", stub.loginCalls)
		}
	})

	t.Run("send returns error on 500", func(t *testing.T) {
		stub := &heartbeatServerStub{
			heartbeatCode: http.StatusInternalServerError,
		}
		srv := httptest.NewServer(stub)
		defer srv.Close()

		cfg := newHeartbeatConfig(srv.URL)
		cfg.Server.Token = "tok"
		cfg.Server.Username = ""
		cfg.Server.Password = ""

		client := agent.NewHeartbeatClient(cfg, "1.0.0", newTestLogger())
		result := agent.ProbeResult{Status: agent.StatusHealthy, Message: "ok"}

		err := client.Send(context.Background(), result)
		if err == nil {
			t.Fatal("Send: expected error for 500, got nil")
		}
	})

	t.Run("send returns error on network failure", func(t *testing.T) {
		// Start a server then immediately close it.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		url := srv.URL
		srv.Close()

		cfg := newHeartbeatConfig(url)
		cfg.Server.Token = "tok"
		cfg.Server.Username = ""
		cfg.Server.Password = ""

		client := agent.NewHeartbeatClient(cfg, "1.0.0", newTestLogger())
		result := agent.ProbeResult{Status: agent.StatusHealthy, Message: "ok"}

		err := client.Send(context.Background(), result)
		if err == nil {
			t.Fatal("Send: expected error for closed server, got nil")
		}
	})
}

func TestHeartbeatClient_Authenticate(t *testing.T) {
	t.Run("authenticate succeeds and caches token", func(t *testing.T) {
		stub := &heartbeatServerStub{
			loginCode:     http.StatusOK,
			loginToken:    "cached-token",
			heartbeatCode: http.StatusOK,
		}
		srv := httptest.NewServer(stub)
		defer srv.Close()

		cfg := newHeartbeatConfig(srv.URL)
		cfg.Server.Token = "" // No static token — must authenticate.

		client := agent.NewHeartbeatClient(cfg, "1.0.0", newTestLogger())

		err := client.Authenticate(context.Background())
		if err != nil {
			t.Fatalf("Authenticate: unexpected error: %v", err)
		}

		if stub.loginCalls != 1 {
			t.Errorf("login calls = %d, want 1", stub.loginCalls)
		}

		// Confirm the token is now used in heartbeat requests.
		result := agent.ProbeResult{Status: agent.StatusHealthy, Message: "ok"}
		if err := client.Send(context.Background(), result); err != nil {
			t.Fatalf("Send after Authenticate: %v", err)
		}

		auth := stub.heartbeatHeaders.Get("Authorization")
		if auth != "Bearer cached-token" {
			t.Errorf("Authorization = %q, want Bearer cached-token", auth)
		}
	})

	t.Run("authenticate fails with bad credentials", func(t *testing.T) {
		stub := &heartbeatServerStub{
			loginCode: http.StatusUnauthorized,
		}
		srv := httptest.NewServer(stub)
		defer srv.Close()

		cfg := newHeartbeatConfig(srv.URL)
		cfg.Server.Token = ""

		client := agent.NewHeartbeatClient(cfg, "1.0.0", newTestLogger())

		err := client.Authenticate(context.Background())
		if err == nil {
			t.Fatal("Authenticate: expected error for bad credentials, got nil")
		}
	})
}
