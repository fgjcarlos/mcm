package alerting

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestWebhookAlerterDisabledDropsAlerts(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer server.Close()

	alerter := NewWebhookAlerter(config.AlertingConfig{Enabled: false, EndpointURL: server.URL, Timeout: "1s"}, nil)
	alerter.Enqueue(WebhookAlert{Type: "broker_status", Severity: "warning", Source: "broker", Message: "down"})

	time.Sleep(50 * time.Millisecond)
	if called {
		t.Fatal("disabled alerter delivered webhook")
	}
}

func TestWebhookAlerterDeliverPayloadAndSignature(t *testing.T) {
	secret := "test-signing-secret"
	observedAt := time.Date(2026, 5, 21, 7, 30, 0, 0, time.UTC)
	received := make(chan WebhookAlert, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		if got := r.Header.Get("X-MCM-Event"); got != "broker_status" {
			t.Errorf("X-MCM-Event = %q, want broker_status", got)
		}
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("decode raw body: %v", err)
		}
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(raw)
		wantSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if got := r.Header.Get("X-MCM-Signature"); got != wantSig {
			t.Errorf("signature = %q, want %q", got, wantSig)
		}
		var payload WebhookAlert
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	alerter := NewWebhookAlerter(config.AlertingConfig{Enabled: true, EndpointURL: server.URL, Timeout: "1s", SigningSecret: secret}, nil)
	t.Cleanup(func() { _ = alerter.Shutdown(context.Background()) })
	err := alerter.deliver(context.Background(), WebhookAlert{
		ID:         "alert-1",
		Type:       "broker_status",
		Severity:   "warning",
		Source:     "broker",
		Message:    "Broker disconnected: EOF",
		ObservedAt: observedAt,
		Details: map[string]any{
			"status": "disconnected",
		},
	})
	if err != nil {
		t.Fatalf("deliver returned error: %v", err)
	}

	select {
	case payload := <-received:
		if payload.ID != "alert-1" || payload.Type != "broker_status" || payload.Severity != "warning" || payload.Source != "broker" {
			t.Fatalf("unexpected payload envelope: %+v", payload)
		}
		if payload.Message != "Broker disconnected: EOF" {
			t.Fatalf("message = %q", payload.Message)
		}
		if payload.ObservedAt != observedAt {
			t.Fatalf("observed_at = %s, want %s", payload.ObservedAt, observedAt)
		}
		if payload.Details["status"] != "disconnected" {
			t.Fatalf("details.status = %#v", payload.Details["status"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for webhook")
	}
}

func TestWebhookAlerterFailedDeliveryReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer server.Close()

	alerter := NewWebhookAlerter(config.AlertingConfig{Enabled: true, EndpointURL: server.URL, Timeout: "1s"}, nil)
	t.Cleanup(func() { _ = alerter.Shutdown(context.Background()) })
	err := alerter.deliver(context.Background(), WebhookAlert{Type: "broker_status", ObservedAt: time.Now().UTC()})
	if err == nil {
		t.Fatal("deliver returned nil error for 500 response")
	}
}

func TestWebhookAlerterShutdownDrainsQueuedAlerts(t *testing.T) {
	received := make(chan WebhookAlert, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookAlert
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- payload
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	alerter := NewWebhookAlerter(config.AlertingConfig{Enabled: true, EndpointURL: server.URL, Timeout: "1s"}, nil)
	for i := 0; i < cap(received); i++ {
		alerter.Enqueue(WebhookAlert{Type: "broker_status", Severity: "warning", Source: "broker", Message: "queued"})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := alerter.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	for i := 0; i < cap(received); i++ {
		select {
		case <-received:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for delivered alert %d", i+1)
		}
	}

	alerter.Enqueue(WebhookAlert{Type: "broker_status"})
}

func TestWebhookAlerterShutdownHonorsContext(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()

	alerter := NewWebhookAlerter(config.AlertingConfig{Enabled: true, EndpointURL: server.URL, Timeout: "100ms"}, nil)
	alerter.Enqueue(WebhookAlert{Type: "broker_status", Severity: "warning", Source: "broker", Message: "queued"})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for webhook delivery to start")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := alerter.Shutdown(ctx); err == nil {
		t.Fatal("Shutdown returned nil error after context deadline")
	}
	close(release)
}

func TestSignWebhookPayload(t *testing.T) {
	got := signWebhookPayload("secret", []byte(`{"type":"broker_status"}`))
	if got != "sha256=e89e86ff24ff918441d1a909c6fb8f2aa04d13f0a996e61e6ab2953b7568b4a6" {
		t.Fatalf("signature = %q", got)
	}
}
