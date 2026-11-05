package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/brokerlog"
)

func TestBrokerDiagnosticsDisconnectedContract(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "diagnostics-auditor", "diagnostics-password", auth.RoleAuditor)
	token := loginAs(t, app, "diagnostics-auditor", "diagnostics-password")
	app.mosquitto.Host, app.mosquitto.Port = "127.0.0.1", 1
	rec := diagnosticsRequest(t, app, token, "/api/v1/broker/diagnostics")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var got brokerDiagnosticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if got.Broker.Status != "disconnected" || got.Diagnostics.MQTT.Status != "disconnected" || got.Diagnostics.MQTT.Error == "" {
		t.Fatalf("disconnected state not reported truthfully: %+v", got.Diagnostics.MQTT)
	}
	if got.BrokerSys.Available || got.BrokerSys.Reason == "" {
		t.Fatalf("$SYS unavailable state has no reason: %+v", got.BrokerSys)
	}
	if got.Sources.BrokerLogs.Available || got.Sources.BrokerLogs.Reason == "" {
		t.Fatalf("unconfigured log source has no reason: %+v", got.Sources.BrokerLogs)
	}
	if got.Sources.InternalMetrics.Endpoint != "/metrics" || got.Sources.InternalMetrics.Counters == nil {
		t.Fatalf("internal metrics contract = %+v", got.Sources.InternalMetrics)
	}
}

func TestBrokerDiagnosticsReportsSysSubscriptionRefusal(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "diagnostics-sys", "diagnostics-password", auth.RoleAuditor)
	token := loginAs(t, app, "diagnostics-sys", "diagnostics-password")
	app.brokerEvents.Publish(BrokerStatusEvent("connected"))
	app.setMonitorSession("mcm-ui-test", []string{"#"}, true, "")
	app.setSysSubscription(false, "not authorized")
	rec := diagnosticsRequest(t, app, token, "/api/v1/broker/diagnostics")
	var got brokerDiagnosticsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode diagnostics: %v", err)
	}
	if rec.Code != http.StatusOK || got.BrokerSys.Available || got.BrokerSys.Reason != "not authorized" {
		t.Fatalf("refused subscription was not reported as degraded: status=%d sys=%+v", rec.Code, got.BrokerSys)
	}
	if got.Broker.Status != "connected" || got.Diagnostics.MQTT.Status != "connected" || len(got.Diagnostics.MQTT.Subscriptions) != 1 || got.Diagnostics.MQTT.Subscriptions[0] != "#" {
		t.Fatalf("$SYS refusal incorrectly took down the # monitor: broker=%+v mqtt=%+v", got.Broker, got.Diagnostics.MQTT)
	}
}

func TestBrokerDiagnosticsRejectsInvalidFilters(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "diagnostics-filter", "diagnostics-password", auth.RoleAuditor)
	token := loginAs(t, app, "diagnostics-filter", "diagnostics-password")
	for _, query := range []string{"severity=verbose", "limit=0", "limit=201", "limit=nope"} {
		t.Run(query, func(t *testing.T) {
			rec := diagnosticsRequest(t, app, token, "/api/v1/broker/diagnostics?"+query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
			}
			var body errorResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error: %v", err)
			}
			if body.Error == "" {
				t.Fatal("missing errorResponse.error")
			}
		})
	}
}

func TestBrokerDiagnosticsDropsExpiredIngestedLogs(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	app.brokerLogEntries = []brokerlog.Entry{{ObservedAt: time.Now().Add(-16 * time.Minute), Severity: brokerlog.SeverityError, Message: "expired"}}
	entries, _, _ := app.diagnosticLogs(brokerlog.SeverityDebug, 10)
	if len(entries) != 0 || len(app.brokerLogEntries) != 0 {
		t.Fatalf("expired broker log entry was retained: %+v", entries)
	}
}

func TestBrokerSysMetricDoesNotCountAsObservedTraffic(t *testing.T) {
	hub := NewBrokerEventHub()
	hub.Publish(BrokerEvent{Type: "broker_sys_metric", Source: "mosquitto_sys", Topic: "$SYS/broker/clients/connected", PayloadPreview: "3"})
	snapshot := hub.Snapshot()
	if snapshot.TopicMessages != 0 || snapshot.Traffic.MessageCount != 0 {
		t.Fatalf("$SYS event entered observed traffic: %+v", snapshot)
	}
	hub.mu.RLock()
	metric, ok := hub.sysMetrics["$SYS/broker/clients/connected"]
	hub.mu.RUnlock()
	if !ok || metric.Value != "3" {
		t.Fatalf("$SYS metric not retained: %+v, present=%v", metric, ok)
	}
}

func diagnosticsRequest(t *testing.T, app *App, token, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)
	return rec
}
