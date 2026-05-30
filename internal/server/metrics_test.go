package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/logging"
)

func TestMetricsEndpointReturnsPrometheusFormat(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	// Non-vector metrics always emit, so they make a stable smoke check for the registration.
	// CounterVec / HistogramVec families only emit a TYPE line after at least one labeled series
	// has been recorded; those are exercised by the per-metric tests below.
	for _, want := range []string{
		"# TYPE mcm_broker_status gauge",
		"# TYPE mcm_broker_reconnects_total counter",
		"# TYPE mcm_broker_messages_total counter",
		"# TYPE mcm_broker_payload_bytes_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics body missing %q\nbody=%s", want, body)
		}
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("Content-Type = %q, want text/plain", contentType)
	}
}

func TestHTTPRequestsCounterIncrementsOnAccess(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	// Drive a request through the access-log middleware so the counter increments.
	handler := withRequestLogging(app.Handler(), logging.Discard(), app.metrics, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	metricsRec := httptest.NewRecorder()
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.Handler().ServeHTTP(metricsRec, metricsReq)
	body := metricsRec.Body.String()

	if !strings.Contains(body, `mcm_http_requests_total{method="GET",route="GET /healthz",status="200"}`) {
		t.Fatalf("metrics output missing healthz counter; body=%s", body)
	}
}

func TestBrokerStatusGaugeReflectsLatestEvent(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	app.brokerEvents.Publish(BrokerEvent{Type: "broker_status", Status: "connected"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.Handler().ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "mcm_broker_status 1") {
		t.Fatalf("metrics body missing mcm_broker_status 1; body=%s", rec.Body.String())
	}

	app.brokerEvents.Publish(BrokerEvent{Type: "broker_status", Status: "disconnected"})

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "mcm_broker_status 0") {
		t.Fatalf("metrics body missing mcm_broker_status 0; body=%s", body)
	}
	if !strings.Contains(body, "mcm_broker_reconnects_total 1") {
		t.Fatalf("metrics body missing mcm_broker_reconnects_total 1; body=%s", body)
	}
}

func TestSecurityEventCounterIncrementsOn401(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events", nil)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	metricsRec := httptest.NewRecorder()
	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	app.Handler().ServeHTTP(metricsRec, metricsReq)
	if !strings.Contains(metricsRec.Body.String(), `mcm_security_events_total{category="protected_api_access_failed"} 1`) {
		t.Fatalf("metrics missing protected_api_access_failed counter; body=%s", metricsRec.Body.String())
	}
}
