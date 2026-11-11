package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/brokerlog"
	"github.com/fgjcarlos/mcm/internal/tlsutil"
)

const (
	brokerLogPollInterval = 5 * time.Second
	brokerProbeTimeout    = 2 * time.Second
	brokerLogMaxReturned  = 200
)

type brokerSysMetric struct {
	Value      string    `json:"value"`
	ObservedAt time.Time `json:"observed_at"`
}

type brokerDiagnosticsResponse struct {
	GeneratedAt time.Time          `json:"generated_at"`
	Broker      diagnosticsBroker  `json:"broker"`
	Sources     diagnosticsSources `json:"sources"`
	BrokerSys   diagnosticsSys     `json:"broker_sys"`
	Diagnostics diagnosticsChecks  `json:"diagnostics"`
}
type diagnosticsBroker struct {
	Status  string               `json:"status"`
	Target  brokerTargetResponse `json:"target"`
	Monitor diagnosticsMonitor   `json:"monitor"`
}
type diagnosticsMonitor struct {
	ClientID      string     `json:"client_id"`
	Subscriptions []string   `json:"subscriptions"`
	ConnectedAt   *time.Time `json:"connected_at"`
	Reconnects    uint64     `json:"reconnects"`
}
type diagnosticsSources struct {
	ObservedTraffic BrokerTrafficMetrics       `json:"observed_traffic"`
	MCMEvents       diagnosticsMCMEvents       `json:"mcm_events"`
	BrokerLogs      diagnosticsBrokerLogs      `json:"broker_logs"`
	InternalMetrics diagnosticsInternalMetrics `json:"internal_metrics"`
}
type diagnosticsMCMEvents struct {
	StatusEvents     uint64     `json:"status_events"`
	TopicMessages    uint64     `json:"topic_messages"`
	EventSubscribers int        `json:"event_subscribers"`
	LastMessageAt    *time.Time `json:"last_message_at"`
	Note             string     `json:"note"`
}
type diagnosticsBrokerLogs struct {
	Available      bool                 `json:"available"`
	Source         string               `json:"source"`
	Path           string               `json:"path"`
	Reason         string               `json:"reason"`
	Entries        []brokerlog.Entry    `json:"entries"`
	SeverityCounts map[string]int       `json:"severity_counts"`
	Limits         diagnosticsLogLimits `json:"limits"`
	Note           string               `json:"note"`
}
type diagnosticsLogLimits struct {
	MaxEntries        int `json:"max_entries"`
	MaxEntriesPerRead int `json:"max_entries_per_read"`
	MaxMessageBytes   int `json:"max_message_bytes"`
	MaxAgeSeconds     int `json:"max_age_seconds"`
}
type diagnosticsInternalMetrics struct {
	Endpoint string             `json:"endpoint"`
	Format   string             `json:"format"`
	Note     string             `json:"note"`
	Counters map[string]float64 `json:"counters"`
}
type diagnosticsSys struct {
	Available     bool                       `json:"available"`
	Reason        string                     `json:"reason"`
	Subscriptions []string                   `json:"subscriptions"`
	Metrics       map[string]brokerSysMetric `json:"metrics"`
	Note          string                     `json:"note"`
}
type diagnosticsChecks struct {
	TCP  diagnosticsTCP  `json:"tcp"`
	TLS  diagnosticsTLS  `json:"tls"`
	MQTT diagnosticsMQTT `json:"mqtt"`
}
type diagnosticsTCP struct {
	Status    string  `json:"status"`
	Address   string  `json:"address"`
	LatencyMS float64 `json:"latency_ms"`
	Error     string  `json:"error"`
}
type diagnosticsTLS struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Cipher  string `json:"cipher"`
	Error   string `json:"error"`
}
type diagnosticsMQTT struct {
	Status        string   `json:"status"`
	ClientID      string   `json:"client_id"`
	Subscriptions []string `json:"subscriptions"`
	Error         string   `json:"error"`
}

func (a *App) setMonitorSession(clientID string, subscriptions []string, connected bool, reason string) {
	h := a.brokerEvents
	h.mu.Lock()
	h.clientID = clientID
	h.sessionError = reason
	h.subscriptions = append([]string(nil), subscriptions...)
	if connected {
		now := time.Now().UTC()
		h.connectedAt = &now
	} else if h.status.Status == "connected" {
		h.reconnects++
	}
	h.mu.Unlock()
}

func (a *App) setSysSubscription(available bool, reason string) {
	h := a.brokerEvents
	h.mu.Lock()
	h.sysAvailable, h.sysReason = available, reason
	h.mu.Unlock()
}

func initialBrokerLogReason(path string) string {
	if strings.TrimSpace(path) == "" {
		return "broker log path is not configured"
	}
	return "broker log source has not been polled"
}

// StartBrokerLogPoller isolates broker-owned file errors from monitor health.
func (a *App) StartBrokerLogPoller(ctx context.Context) {
	limits := brokerlog.DefaultLimits()
	a.brokerLogStateMu.Lock()
	a.brokerLogLimits = limits
	a.brokerLogAvailable = false
	a.brokerLogReason = "broker log source has not been polled"
	a.brokerLogStateMu.Unlock()
	path := strings.TrimSpace(a.mosquitto.LogPath)
	if path == "" {
		a.setBrokerLogStatus(false, "broker log path is not configured")
		return
	}
	source, err := brokerlog.NewFileSource(path, limits)
	if err != nil {
		a.setBrokerLogStatus(false, err.Error())
		return
	}
	poll := func() {
		result, err := source.Read(ctx, brokerlog.Filter{MinimumSeverity: brokerlog.SeverityDebug})
		if err != nil {
			a.setBrokerLogStatus(false, err.Error())
			return
		}
		if !result.Available {
			a.setBrokerLogStatus(false, "broker log file does not exist")
			return
		}
		a.setBrokerLogStatus(true, "")
		for _, entry := range result.Entries {
			if !a.retainBrokerLog(entry) {
				continue
			}
			a.brokerEvents.Publish(BrokerEvent{Type: "broker_log_entry", Source: "mosquitto_log", Severity: string(entry.Severity), Kind: entry.Kind, ClientID: entry.ClientID, Username: entry.Username, Message: entry.Message, Topic: entry.Topic, ObservedAt: entry.ObservedAt})
		}
	}
	poll()
	ticker := time.NewTicker(brokerLogPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			poll()
		}
	}
}

func (a *App) setBrokerLogStatus(available bool, reason string) {
	a.brokerLogStateMu.Lock()
	a.brokerLogAvailable, a.brokerLogReason = available, reason
	a.brokerLogStateMu.Unlock()
}

func (a *App) retainBrokerLog(entry brokerlog.Entry) bool {
	a.brokerLogStateMu.Lock()
	defer a.brokerLogStateMu.Unlock()
	now := time.Now().UTC()
	kept := a.brokerLogEntries[:0]
	for _, existing := range a.brokerLogEntries {
		if now.Sub(existing.ObservedAt) <= 15*time.Minute {
			kept = append(kept, existing)
		}
	}
	a.brokerLogEntries = kept
	for _, existing := range kept {
		if existing.ObservedAt.Equal(entry.ObservedAt) && existing.Severity == entry.Severity && existing.Message == entry.Message {
			return false
		}
	}
	if len(kept) == maxBrokerIngestedEntries {
		copy(kept, kept[1:])
		kept = kept[:len(kept)-1]
	}
	kept = append(kept, entry)
	a.brokerLogEntries = kept
	return true
}

func (a *App) handleBrokerDiagnostics(w http.ResponseWriter, r *http.Request) {
	severity := brokerlog.Severity(strings.ToLower(strings.TrimSpace(r.URL.Query().Get("severity"))))
	if severity == "" {
		severity = brokerlog.SeverityInfo
	}
	if !validBrokerSeverity(severity) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "severity must be one of: debug, info, warning, error"})
		return
	}
	limit := brokerLogMaxReturned
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > brokerLogMaxReturned {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "limit must be between 1 and 200"})
			return
		}
		limit = parsed
	}
	snapshot := a.brokerEvents.Snapshot()
	address := net.JoinHostPort(a.mosquitto.Host, strconv.Itoa(a.mosquitto.Port))
	h := a.brokerEvents
	h.mu.RLock()
	monitor := diagnosticsMonitor{ClientID: h.clientID, Subscriptions: append([]string{}, h.subscriptions...), ConnectedAt: h.connectedAt, Reconnects: h.reconnects}
	mqttError := h.sessionError
	sys := diagnosticsSys{Available: h.sysAvailable, Reason: h.sysReason, Subscriptions: []string{}, Metrics: make(map[string]brokerSysMetric), Note: "Broker-published $SYS metrics; this is not a complete broker inventory."}
	if h.sysAvailable {
		sys.Subscriptions = []string{"$SYS/#"}
	}
	for key, metric := range h.sysMetrics {
		sys.Metrics[strings.TrimPrefix(key, "$SYS/")] = metric
	}
	h.mu.RUnlock()
	logs, logAvailable, logReason := a.diagnosticLogs(severity, limit)
	a.brokerLogStateMu.RLock()
	limits := a.brokerLogLimits
	a.brokerLogStateMu.RUnlock()
	if limits.MaxEntries == 0 {
		limits = brokerlog.DefaultLimits()
	}
	response := brokerDiagnosticsResponse{
		GeneratedAt: time.Now().UTC(),
		Broker:      diagnosticsBroker{Status: snapshot.Status.Status, Target: brokerTargetResponse{Host: a.mosquitto.Host, Port: a.mosquitto.Port, Address: address, TLSEnabled: a.mosquitto.TLS.Enabled}, Monitor: monitor},
		Sources:     diagnosticsSources{ObservedTraffic: snapshot.Traffic, MCMEvents: diagnosticsMCMEvents{StatusEvents: snapshot.StatusEvents, TopicMessages: snapshot.TopicMessages, EventSubscribers: snapshot.EventSubscribers, LastMessageAt: snapshot.LastMessageAt, Note: "Events produced by MCM itself. They are not broker-sourced."}, BrokerLogs: diagnosticsBrokerLogs{Available: logAvailable, Source: "mosquitto_log", Path: a.mosquitto.LogPath, Reason: logReason, Entries: logs, SeverityCounts: countLogSeverities(logs), Limits: diagnosticsLogLimits{limits.MaxEntries, limits.MaxEntriesPerRead, limits.MaxMessageBytes, int(limits.MaxAge.Seconds())}, Note: "Broker-owned log file entries are bounded and are not a complete broker inventory."}, InternalMetrics: diagnosticsInternalMetrics{Endpoint: "/metrics", Format: "prometheus", Note: "MCM's own process metrics. They are not broker-sourced.", Counters: a.brokerMetricCounters()}},
		BrokerSys:   sys, Diagnostics: diagnosticsChecks{TCP: probeTCP(address), TLS: a.probeTLS(address), MQTT: diagnosticsMQTT{Status: snapshot.Status.Status, ClientID: monitor.ClientID, Subscriptions: monitor.Subscriptions, Error: func() string {
			if snapshot.Status.Status != "connected" {
				if mqttError != "" {
					return mqttError
				}
				return "monitor session is disconnected"
			}
			return ""
		}()}},
	}
	writeJSON(w, http.StatusOK, response)
}

func validBrokerSeverity(value brokerlog.Severity) bool {
	return value == brokerlog.SeverityDebug || value == brokerlog.SeverityInfo || value == brokerlog.SeverityWarning || value == brokerlog.SeverityError
}

func (a *App) diagnosticLogs(minimum brokerlog.Severity, limit int) ([]brokerlog.Entry, bool, string) {
	a.brokerLogStateMu.Lock()
	defer a.brokerLogStateMu.Unlock()
	maxAge := a.brokerLogLimits.MaxAge
	if maxAge <= 0 {
		maxAge = 15 * time.Minute
	}
	now := time.Now().UTC()
	kept := a.brokerLogEntries[:0]
	for _, entry := range a.brokerLogEntries {
		if now.Sub(entry.ObservedAt) <= maxAge {
			kept = append(kept, entry)
		}
	}
	for i := len(kept); i < len(a.brokerLogEntries); i++ {
		a.brokerLogEntries[i] = brokerlog.Entry{}
	}
	a.brokerLogEntries = kept
	out := make([]brokerlog.Entry, 0, min(limit, len(a.brokerLogEntries)))
	for i := len(a.brokerLogEntries) - 1; i >= 0 && len(out) < limit; i-- {
		e := a.brokerLogEntries[i]
		if brokerSeverityRank(e.Severity) >= brokerSeverityRank(minimum) {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, a.brokerLogAvailable, a.brokerLogReason
}
func brokerSeverityRank(s brokerlog.Severity) int {
	switch s {
	case brokerlog.SeverityDebug:
		return 0
	case brokerlog.SeverityInfo:
		return 1
	case brokerlog.SeverityWarning:
		return 2
	case brokerlog.SeverityError:
		return 3
	default:
		return -1
	}
}
func countLogSeverities(entries []brokerlog.Entry) map[string]int {
	counts := map[string]int{"error": 0, "warning": 0, "info": 0, "debug": 0}
	for _, e := range entries {
		counts[string(e.Severity)]++
	}
	return counts
}

func probeTCP(address string) diagnosticsTCP {
	start := time.Now()
	conn, err := net.DialTimeout("tcp", address, brokerProbeTimeout)
	result := diagnosticsTCP{Address: address, LatencyMS: float64(time.Since(start).Microseconds()) / 1000}
	if err != nil {
		result.Status = "unreachable"
		result.Error = err.Error()
		return result
	}
	_ = conn.Close()
	result.Status = "reachable"
	return result
}
func (a *App) probeTLS(address string) diagnosticsTLS {
	if !a.mosquitto.TLS.Enabled {
		return diagnosticsTLS{Status: "disabled"}
	}
	config, err := tlsutil.BuildMosquittoTLSConfig(a.mosquitto)
	if err != nil {
		return diagnosticsTLS{Status: "failed", Error: err.Error()}
	}
	dialer := net.Dialer{Timeout: brokerProbeTimeout}
	conn, err := tls.DialWithDialer(&dialer, "tcp", address, config)
	if err != nil {
		return diagnosticsTLS{Status: "failed", Error: err.Error()}
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(brokerProbeTimeout)); err != nil {
		return diagnosticsTLS{Status: "failed", Error: err.Error()}
	}
	state := conn.ConnectionState()
	return diagnosticsTLS{Status: "connected", Version: tlsVersionName(state.Version), Cipher: tls.CipherSuiteName(state.CipherSuite)}
}
func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS1.0"
	case tls.VersionTLS11:
		return "TLS1.1"
	case tls.VersionTLS12:
		return "TLS1.2"
	case tls.VersionTLS13:
		return "TLS1.3"
	default:
		return fmt.Sprintf("0x%04x", version)
	}
}

func (a *App) brokerMetricCounters() map[string]float64 {
	result := make(map[string]float64, 4)
	families, err := a.metrics.Gatherer().Gather()
	if err != nil {
		return result
	}
	allowed := map[string]bool{"mcm_broker_status": true, "mcm_broker_reconnects_total": true, "mcm_broker_messages_total": true, "mcm_broker_payload_bytes_total": true}
	for _, family := range families {
		name := family.GetName()
		if !allowed[name] {
			continue
		}
		var sum float64
		for _, metric := range family.Metric {
			if metric.Gauge != nil {
				sum += metric.Gauge.GetValue()
			} else if metric.Counter != nil {
				sum += metric.Counter.GetValue()
			}
		}
		result[name] = sum
	}
	return result
}

func boundedPayload(payload []byte, limit int) string {
	if len(payload) > limit {
		payload = payload[:limit]
	}
	return strings.ToValidUTF8(string(payload), "�")
}
