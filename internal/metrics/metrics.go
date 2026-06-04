// Package metrics defines and registers the Prometheus metrics exposed by MCM at
// /metrics. Labels are kept low-cardinality on purpose: HTTP route uses the path
// pattern set by ServeMux (never the raw URL) and topic/payload data is never used
// as a label.
package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry isolates MCM's metrics from the global default registry so tests can
// observe them deterministically and so any other process embedding MCM does not
// pollute its own metrics namespace.
type Registry struct {
	reg *prometheus.Registry

	HTTPRequests        *prometheus.CounterVec
	HTTPRequestDuration *prometheus.HistogramVec

	BrokerStatus       prometheus.Gauge
	BrokerReconnects   prometheus.Counter
	BrokerMessages     prometheus.Counter
	BrokerPayloadBytes prometheus.Counter

	LoginAttempts  *prometheus.CounterVec
	AuditEvents    *prometheus.CounterVec
	SecurityEvents *prometheus.CounterVec
}

// New constructs and registers all MCM metrics on a fresh prometheus.Registry.
func New() *Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(prometheus.NewGoCollector())
	reg.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	r := &Registry{
		reg: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcm_http_requests_total",
			Help: "HTTP requests handled by MCM, labeled by method, route path pattern, and three-digit status code.",
		}, []string{"method", "route", "status"}),
		HTTPRequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mcm_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, labeled by method and route path pattern.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		BrokerStatus: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mcm_broker_status",
			Help: "1 when the configured Mosquitto broker is connected, 0 otherwise.",
		}),
		BrokerReconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mcm_broker_reconnects_total",
			Help: "Number of broker disconnect transitions observed (each one followed by an auto-reconnect attempt).",
		}),
		BrokerMessages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mcm_broker_messages_total",
			Help: "MQTT topic messages observed by MCM. Topic names are intentionally not used as labels.",
		}),
		BrokerPayloadBytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mcm_broker_payload_bytes_total",
			Help: "Total MQTT payload bytes observed by MCM.",
		}),
		LoginAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcm_login_attempts_total",
			Help: "Admin login attempts, labeled by result (success|failure).",
		}, []string{"result"}),
		AuditEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcm_audit_events_total",
			Help: "Administrative audit events recorded, labeled by result.",
		}, []string{"result"}),
		SecurityEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcm_security_events_total",
			Help: "Security events recorded, labeled by category.",
		}, []string{"category"}),
	}

	reg.MustRegister(
		r.HTTPRequests,
		r.HTTPRequestDuration,
		r.BrokerStatus,
		r.BrokerReconnects,
		r.BrokerMessages,
		r.BrokerPayloadBytes,
		r.LoginAttempts,
		r.AuditEvents,
		r.SecurityEvents,
	)
	return r
}

// Handler returns an http.Handler that serves the Prometheus text exposition format.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{Registry: r.reg})
}

// Gatherer exposes the underlying prometheus.Gatherer for tests that want to read
// metric families directly without going through the HTTP exposition path.
func (r *Registry) Gatherer() prometheus.Gatherer {
	return r.reg
}
