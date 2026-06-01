package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/fgjcarlos/mcm/internal/metrics"
)

// expectedMetricNames is the public observability contract: dashboards and alerts
// reference these names verbatim. Renaming one breaks production monitoring
// silently, so this list is the regression guard for that contract.
var expectedMetricNames = []string{
	"mcm_http_requests_total",
	"mcm_http_request_duration_seconds",
	"mcm_broker_status",
	"mcm_broker_reconnects_total",
	"mcm_broker_messages_total",
	"mcm_broker_payload_bytes_total",
	"mcm_login_attempts_total",
	"mcm_audit_events_total",
	"mcm_security_events_total",
}

// recordOnce touches every metric so each family is observable. *Vec families
// (CounterVec, HistogramVec) are absent from Gather()/exposition until at least
// one labeled series has been recorded — only plain Counters/Gauges emit a zero
// series on their own — so the contract assertions below must prime them first.
func recordOnce(reg *metrics.Registry) {
	reg.HTTPRequests.WithLabelValues("GET", "/api/clients", "200").Inc()
	reg.HTTPRequestDuration.WithLabelValues("GET", "/api/clients").Observe(0.42)
	reg.BrokerStatus.Set(1)
	reg.BrokerReconnects.Inc()
	reg.BrokerMessages.Add(3)
	reg.BrokerPayloadBytes.Add(128)
	reg.LoginAttempts.WithLabelValues("failure").Inc()
	reg.AuditEvents.WithLabelValues("success").Inc()
	reg.SecurityEvents.WithLabelValues("login_lockout").Inc()
}

func TestRegistry_AllMetricFamiliesExposed(t *testing.T) {
	reg := metrics.New()
	recordOnce(reg)

	families, err := reg.Gatherer().Gather()
	if err != nil {
		t.Fatalf("gather metric families: %v", err)
	}

	got := make(map[string]bool, len(families))
	for _, fam := range families {
		got[fam.GetName()] = true
	}

	for _, name := range expectedMetricNames {
		if !got[name] {
			t.Errorf("metric %q not exposed; dashboards depending on it would break", name)
		}
	}
}

func TestRegistry_RecordedValuesAreObservable(t *testing.T) {
	reg := metrics.New()

	t.Run("plain counters and gauge", func(t *testing.T) {
		reg.BrokerStatus.Set(1)
		reg.BrokerReconnects.Inc()
		reg.BrokerMessages.Add(3)
		reg.BrokerPayloadBytes.Add(128)

		if got := testutil.ToFloat64(reg.BrokerStatus); got != 1 {
			t.Errorf("BrokerStatus = %v, want 1", got)
		}
		if got := testutil.ToFloat64(reg.BrokerReconnects); got != 1 {
			t.Errorf("BrokerReconnects = %v, want 1", got)
		}
		if got := testutil.ToFloat64(reg.BrokerMessages); got != 3 {
			t.Errorf("BrokerMessages = %v, want 3", got)
		}
		if got := testutil.ToFloat64(reg.BrokerPayloadBytes); got != 128 {
			t.Errorf("BrokerPayloadBytes = %v, want 128", got)
		}
	})

	t.Run("labeled counters", func(t *testing.T) {
		reg.HTTPRequests.WithLabelValues("GET", "/api/clients", "200").Inc()
		reg.LoginAttempts.WithLabelValues("failure").Inc()
		reg.AuditEvents.WithLabelValues("success").Inc()
		reg.SecurityEvents.WithLabelValues("login_lockout").Inc()

		if got := testutil.ToFloat64(reg.HTTPRequests.WithLabelValues("GET", "/api/clients", "200")); got != 1 {
			t.Errorf("HTTPRequests{GET,/api/clients,200} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(reg.LoginAttempts.WithLabelValues("failure")); got != 1 {
			t.Errorf("LoginAttempts{failure} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(reg.AuditEvents.WithLabelValues("success")); got != 1 {
			t.Errorf("AuditEvents{success} = %v, want 1", got)
		}
		if got := testutil.ToFloat64(reg.SecurityEvents.WithLabelValues("login_lockout")); got != 1 {
			t.Errorf("SecurityEvents{login_lockout} = %v, want 1", got)
		}
	})

	t.Run("duration histogram records observations", func(t *testing.T) {
		reg.HTTPRequestDuration.WithLabelValues("GET", "/api/clients").Observe(0.42)

		if got := testutil.CollectAndCount(reg.HTTPRequestDuration); got != 1 {
			t.Errorf("HTTPRequestDuration series count = %d, want 1", got)
		}
	})
}

func TestHandler_ServesPrometheusExposition(t *testing.T) {
	reg := metrics.New()
	recordOnce(reg)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	reg.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}

	body := rec.Body.String()
	for _, name := range expectedMetricNames {
		if !strings.Contains(body, name) {
			t.Errorf("exposition output missing %q", name)
		}
	}
	if !strings.Contains(body, "mcm_broker_messages_total 3") {
		t.Errorf("expected recorded value in exposition, got:\n%s", body)
	}
}

// TestNew_RegistriesAreIsolated locks in the documented reason New() uses a fresh
// prometheus.Registry instead of the global default: two instances must not share
// state. Without isolation, parallel tests (or an embedding process) would see
// each other's counts.
func TestNew_RegistriesAreIsolated(t *testing.T) {
	a := metrics.New()
	b := metrics.New()

	a.BrokerReconnects.Inc()

	if got := testutil.ToFloat64(a.BrokerReconnects); got != 1 {
		t.Errorf("registry A BrokerReconnects = %v, want 1", got)
	}
	if got := testutil.ToFloat64(b.BrokerReconnects); got != 0 {
		t.Errorf("registry B BrokerReconnects = %v, want 0 (registries must be isolated)", got)
	}
}
