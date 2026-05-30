package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"nhooyr.io/websocket"

	"github.com/fgjcarlos/mcm/internal/alerting"
	"github.com/fgjcarlos/mcm/internal/backoff"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/metrics"
	"github.com/fgjcarlos/mcm/internal/schema"
	"github.com/fgjcarlos/mcm/internal/sparkplug"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const (
	maxPayloadPreviewBytes        = 1024
	maxBrokerLogBuffer            = 100
	brokerEventSubscriberCapacity = maxBrokerLogBuffer + 16
	brokerTrafficWindow           = 5 * time.Minute
	maxBrokerTrafficEvents        = 5000
)

// BrokerEvent is the JSON contract streamed to the frontend broker WebSocket.
type BrokerEvent struct {
	Type              string                    `json:"type"`
	Status            string                    `json:"status,omitempty"`
	Topic             string                    `json:"topic,omitempty"`
	PayloadPreview    string                    `json:"payload_preview,omitempty"`
	PayloadFormat     string                    `json:"payload_format,omitempty"`
	PayloadBytes      int                       `json:"payload_bytes,omitempty"`
	Truncated         bool                      `json:"truncated,omitempty"`
	Payload           *PayloadInspection        `json:"payload_inspection,omitempty"`
	SchemaValidation  *SchemaValidationResult   `json:"schema_validation,omitempty"`
	Sparkplug         *sparkplug.Metadata       `json:"sparkplug,omitempty"`
	SparkplugMetrics  *sparkplug.DecodedPayload `json:"sparkplug_metrics,omitempty"`
	Source            string                    `json:"source,omitempty"`
	Severity          string                    `json:"severity,omitempty"`
	Message           string                    `json:"message,omitempty"`
	ObservedAt        time.Time                 `json:"observed_at"`
}

// SchemaValidationResult summarizes JSON schema validation for an observed topic payload.
type SchemaValidationResult struct {
	SchemaID    int64    `json:"schema_id"`
	SchemaName  string   `json:"schema_name"`
	TopicFilter string   `json:"topic_filter"`
	Valid       bool     `json:"valid"`
	Errors      []string `json:"errors,omitempty"`
}

// PayloadInspection contains bounded, derived metadata for an MQTT payload.
// It intentionally excludes the unbounded raw payload; callers should expose
// PayloadPreview for operator-friendly snippets and keep persistence limited to
// safe metadata such as format, byte length, and truncation state.
type PayloadInspection struct {
	DetectedType      string   `json:"detected_type"`
	ByteLength        int      `json:"byte_length"`
	Truncated         bool     `json:"truncated"`
	JSONValid         bool     `json:"json_valid"`
	JSONTopLevelKeys  []string `json:"json_top_level_keys,omitempty"`
	JSONElementCount  int      `json:"json_element_count,omitempty"`
	JSONScalarSummary string   `json:"json_scalar_summary,omitempty"`
}

type BrokerEventHub struct {
	mu            sync.RWMutex
	subscribers   map[chan BrokerEvent]struct{}
	status        BrokerEvent
	logs          []BrokerEvent
	trafficEvents []BrokerEvent
	statusEvents  uint64
	topicMessages uint64
	lastMessageAt *time.Time
	store         *storage.Store
	retention     time.Duration
	metrics       *metrics.Registry
}

// BrokerEventSnapshot is a point-in-time, read-only view of broker stream state.
type BrokerEventSnapshot struct {
	Status           BrokerEvent
	EventSubscribers int
	StatusEvents     uint64
	TopicMessages    uint64
	LastMessageAt    *time.Time
	Traffic          BrokerTrafficMetrics
}

// BrokerTrafficMetrics summarizes recent topic activity for operator dashboards.
type BrokerTrafficMetrics struct {
	WindowSeconds        int                 `json:"window_seconds"`
	MessageCount         int                 `json:"message_count"`
	MessageRatePerMinute float64             `json:"message_rate_per_minute"`
	RatePoints           []BrokerRatePoint   `json:"rate_points"`
	TopTopics            []BrokerTrafficItem `json:"top_topics"`
	TopClients           []BrokerTrafficItem `json:"top_clients"`
	TopClientsAvailable  bool                `json:"top_clients_available"`
	TopClientsNote       string              `json:"top_clients_note"`
	Persistence          string              `json:"persistence"`
}

// BrokerTrafficItem is a count and percentage for a traffic hotspot.
type BrokerTrafficItem struct {
	Name       string  `json:"name"`
	Count      int     `json:"count"`
	Percentage float64 `json:"percentage"`
}

// BrokerRatePoint is a one-minute message-rate bucket.
type BrokerRatePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Count     int       `json:"count"`
}

func NewBrokerEventHub() *BrokerEventHub {
	return &BrokerEventHub{
		subscribers: make(map[chan BrokerEvent]struct{}),
		status: BrokerEvent{
			Type:       "broker_status",
			Status:     "disconnected",
			ObservedAt: time.Now().UTC(),
		},
	}
}

func (h *BrokerEventHub) SetPersistence(store *storage.Store, retention time.Duration) {
	h.mu.Lock()
	h.store = store
	h.retention = retention
	h.mu.Unlock()
}

// SetMetrics attaches a Prometheus registry to the hub so Publish can increment
// broker_* counters as events are observed. Safe to call once during construction.
func (h *BrokerEventHub) SetMetrics(reg *metrics.Registry) {
	h.mu.Lock()
	h.metrics = reg
	h.mu.Unlock()
}

func (h *BrokerEventHub) Subscribe() (<-chan BrokerEvent, func()) {
	ch := make(chan BrokerEvent, brokerEventSubscriberCapacity)

	h.mu.Lock()
	ch <- h.status
	for _, event := range h.logs {
		ch <- event
	}
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, unsubscribe
}

func (h *BrokerEventHub) Publish(event BrokerEvent) {
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}

	h.mu.Lock()
	previousStatus := h.status.Status
	if event.Type == "broker_status" {
		h.status = event
		h.statusEvents++
	} else if event.Type == "broker_log" {
		h.logs = append(h.logs, event)
		if len(h.logs) > maxBrokerLogBuffer {
			h.logs = append([]BrokerEvent(nil), h.logs[len(h.logs)-maxBrokerLogBuffer:]...)
		}
	} else if event.Type == "topic_message" {
		h.topicMessages++
		observedAt := event.ObservedAt
		h.lastMessageAt = &observedAt
		h.trafficEvents = append(h.trafficEvents, event)
		h.pruneTrafficEventsLocked(observedAt)
	}
	store := h.store
	retention := h.retention
	reg := h.metrics
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
	h.mu.Unlock()

	if reg != nil {
		switch event.Type {
		case "broker_status":
			if event.Status == "connected" {
				reg.BrokerStatus.Set(1)
			} else {
				reg.BrokerStatus.Set(0)
				if previousStatus == "connected" {
					reg.BrokerReconnects.Inc()
				}
			}
		case "topic_message":
			reg.BrokerMessages.Inc()
			reg.BrokerPayloadBytes.Add(float64(event.PayloadBytes))
		}
	}

	persistBrokerEvent(store, retention, event)
}

func (h *BrokerEventHub) Snapshot() BrokerEventSnapshot {
	// Copy in-memory state under the read lock, then release it BEFORE touching
	// the store. The traffic metrics query can block on SQLite, and holding the
	// lock during it would stall Publish (which needs the write lock) on the
	// hot message path.
	h.mu.RLock()
	status := h.status
	subscribers := len(h.subscribers)
	statusEvents := h.statusEvents
	topicMessages := h.topicMessages
	var lastMessageAt *time.Time
	if h.lastMessageAt != nil {
		value := *h.lastMessageAt
		lastMessageAt = &value
	}
	events := append([]BrokerEvent(nil), h.trafficEvents...)
	store := h.store
	h.mu.RUnlock()

	return BrokerEventSnapshot{
		Status:           status,
		EventSubscribers: subscribers,
		StatusEvents:     statusEvents,
		TopicMessages:    topicMessages,
		LastMessageAt:    lastMessageAt,
		Traffic:          trafficMetrics(events, store, time.Now().UTC()),
	}
}

func (h *BrokerEventHub) pruneTrafficEventsLocked(now time.Time) {
	cutoff := now.Add(-brokerTrafficWindow)
	start := 0
	for start < len(h.trafficEvents) && h.trafficEvents[start].ObservedAt.Before(cutoff) {
		start++
	}
	if start > 0 {
		h.trafficEvents = append([]BrokerEvent(nil), h.trafficEvents[start:]...)
	}
	if len(h.trafficEvents) > maxBrokerTrafficEvents {
		h.trafficEvents = append([]BrokerEvent(nil), h.trafficEvents[len(h.trafficEvents)-maxBrokerTrafficEvents:]...)
	}
}

// trafficMetrics computes traffic metrics from the supplied in-memory events,
// preferring persisted broker metric events when a store is available. Callers
// MUST pass an already-copied events slice and the store pointer captured under
// the lock, and MUST NOT hold h.mu: the store query can block and is run here,
// outside any lock, to keep the message-publish path unblocked.
func trafficMetrics(events []BrokerEvent, store *storage.Store, now time.Time) BrokerTrafficMetrics {
	if store != nil {
		persisted, err := store.ListBrokerMetricEvents(context.Background(), storage.BrokerMetricQuery{
			Since: now.Add(-brokerTrafficWindow),
			Until: now,
			Limit: maxBrokerTrafficEvents,
		})
		if err == nil {
			events = make([]BrokerEvent, 0, len(persisted))
			for i := len(persisted) - 1; i >= 0; i-- {
				event := persisted[i]
				if event.Type != "topic_message" {
					continue
				}
				events = append(events, BrokerEvent{
					Type:       event.Type,
					Topic:      event.Topic,
					ObservedAt: event.ObservedAt,
				})
			}
		}
	}
	metrics := computeBrokerTrafficMetrics(events, now, brokerTrafficWindow, 5)
	if store != nil {
		metrics.Persistence = "persisted broker metric events are used when available"
	} else {
		metrics.Persistence = "in-memory only; metrics reset when the server restarts"
	}
	return metrics
}

func computeBrokerTrafficMetrics(events []BrokerEvent, now time.Time, window time.Duration, limit int) BrokerTrafficMetrics {
	if window <= 0 {
		window = brokerTrafficWindow
	}
	if limit <= 0 {
		limit = 5
	}
	cutoff := now.Add(-window)
	topicCounts := make(map[string]int)
	buckets := make(map[time.Time]int)
	messageCount := 0
	for _, event := range events {
		if event.Type != "topic_message" || strings.TrimSpace(event.Topic) == "" {
			continue
		}
		observedAt := event.ObservedAt.UTC()
		if observedAt.IsZero() || observedAt.Before(cutoff) || observedAt.After(now) {
			continue
		}
		messageCount++
		topicCounts[event.Topic]++
		bucket := observedAt.Truncate(time.Minute)
		buckets[bucket]++
	}

	points := make([]BrokerRatePoint, 0, int(window/time.Minute)+1)
	start := cutoff.Truncate(time.Minute)
	end := now.Truncate(time.Minute)
	for cursor := start; !cursor.After(end); cursor = cursor.Add(time.Minute) {
		points = append(points, BrokerRatePoint{Timestamp: cursor, Count: buckets[cursor]})
	}

	metrics := BrokerTrafficMetrics{
		WindowSeconds:        int(window.Seconds()),
		MessageCount:         messageCount,
		MessageRatePerMinute: float64(messageCount) / window.Minutes(),
		RatePoints:           points,
		TopTopics:            topTrafficItems(topicCounts, messageCount, limit),
		TopClientsAvailable:  false,
		TopClientsNote:       "Client identity is not included in MQTT application messages observed via wildcard subscriptions. Enable broker-side client metrics or log ingestion to populate this widget in a future release.",
	}
	return metrics
}

func topTrafficItems(counts map[string]int, total int, limit int) []BrokerTrafficItem {
	items := make([]BrokerTrafficItem, 0, len(counts))
	for name, count := range counts {
		percentage := 0.0
		if total > 0 {
			percentage = float64(count) * 100 / float64(total)
		}
		items = append(items, BrokerTrafficItem{Name: name, Count: count, Percentage: percentage})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Count == items[j].Count {
			return items[i].Name < items[j].Name
		}
		return items[i].Count > items[j].Count
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items
}

func BrokerStatusEvent(status string) BrokerEvent {
	return BrokerEvent{Type: "broker_status", Status: status, ObservedAt: time.Now().UTC()}
}

func BrokerLogEvent(source string, severity string, message string) BrokerEvent {
	return BrokerEvent{
		Type:       "broker_log",
		Source:     strings.TrimSpace(source),
		Severity:   strings.TrimSpace(severity),
		Message:    strings.TrimSpace(message),
		ObservedAt: time.Now().UTC(),
	}
}

func (a *App) publishBrokerStatus(status string, logSeverity string, logMessage string) {
	previousStatus := a.brokerEvents.Snapshot().Status.Status
	event := BrokerStatusEvent(status)
	a.brokerEvents.Publish(event)
	if strings.TrimSpace(logMessage) != "" {
		a.brokerEvents.Publish(BrokerLogEvent("broker", logSeverity, logMessage))
	}
	if previousStatus == status {
		return
	}
	a.alerts.Enqueue(alerting.WebhookAlert{
		Type:       "broker_status",
		Severity:   strings.TrimSpace(logSeverity),
		Source:     "broker",
		Message:    strings.TrimSpace(logMessage),
		ObservedAt: event.ObservedAt,
		Details: map[string]any{
			"status": status,
		},
	})
}

func persistBrokerEvent(store *storage.Store, retention time.Duration, event BrokerEvent) {
	if store == nil {
		return
	}
	ctx := context.Background()
	_, _ = store.RecordBrokerMetricEvent(ctx, storage.CreateBrokerMetricEventParams{
		Type:          event.Type,
		Status:        event.Status,
		Topic:         event.Topic,
		PayloadFormat: event.PayloadFormat,
		PayloadBytes:  event.PayloadBytes,
		Truncated:     event.Truncated,
		ObservedAt:    event.ObservedAt,
	})
	if retention > 0 {
		_, _, _ = store.PruneBrokerMetrics(ctx, time.Now().UTC().Add(-retention))
	}
}

func TopicEvent(topic string, payload []byte, limit int) BrokerEvent {
	if limit <= 0 {
		limit = maxPayloadPreviewBytes
	}

	preview, format, truncated, inspection := inspectPayload(payload, limit)

	return BrokerEvent{
		Type:           "topic_message",
		Topic:          topic,
		PayloadPreview: preview,
		PayloadFormat:  format,
		PayloadBytes:   len(payload),
		Truncated:      truncated,
		Payload:        &inspection,
		Sparkplug:      sparkplug.ClassifyTopic(topic),
		ObservedAt:     time.Now().UTC(),
	}
}

func (a *App) TopicEvent(topic string, payload []byte, limit int) BrokerEvent {
	event := TopicEvent(topic, payload, limit)
	if a == nil {
		return event
	}

	// Decode Sparkplug B payload when enabled and topic is Sparkplug.
	if a.mosquitto.SparkplugPayloadDecode && event.Sparkplug != nil {
		maxMetrics := a.mosquitto.SparkplugMaxMetrics
		if maxMetrics <= 0 {
			maxMetrics = 50
		}
		decoded, err := sparkplug.DecodePayload(payload, maxMetrics)
		if err == nil {
			event.SparkplugMetrics = decoded
		}
	}

	if event.PayloadFormat != "json" || a.store == nil {
		return event
	}
	schemas, err := a.schemaCache.get(context.Background(), a.store)
	if err != nil {
		return event
	}
	for _, definition := range schemas {
		if !definition.Enabled || !schema.TopicFilterMatches(definition.TopicFilter, topic) {
			continue
		}
		result, err := schema.ValidateJSONPayload(definition.Schema, payload)
		if err != nil {
			event.SchemaValidation = &SchemaValidationResult{
				SchemaID:    definition.ID,
				SchemaName:  definition.Name,
				TopicFilter: definition.TopicFilter,
				Valid:       false,
				Errors:      []string{truncateStringUTF8(err.Error(), 200)},
			}
			return event
		}
		event.SchemaValidation = &SchemaValidationResult{
			SchemaID:    definition.ID,
			SchemaName:  definition.Name,
			TopicFilter: definition.TopicFilter,
			Valid:       result.Valid,
			Errors:      result.Errors,
		}
		return event
	}
	return event
}

func inspectPayload(payload []byte, limit int) (string, string, bool, PayloadInspection) {
	inspection := PayloadInspection{
		DetectedType: "text",
		ByteLength:   len(payload),
	}

	if !isLikelyText(payload) {
		inspection.DetectedType = "binary"
		preview := fmt.Sprintf("<binary payload omitted: %d bytes>", len(payload))
		inspection.Truncated = len(preview) > limit
		if inspection.Truncated {
			preview = truncateStringUTF8(preview, limit)
		}
		return preview, "binary", inspection.Truncated, inspection
	}

	var formatted any
	if json.Valid(payload) && json.Unmarshal(payload, &formatted) == nil {
		inspection.JSONValid = true
		inspection.DetectedType = jsonDetectedType(formatted)
		populateJSONInspection(&inspection, formatted)

		preview := string(payload)
		if data, err := json.MarshalIndent(formatted, "", "  "); err == nil {
			preview = string(data)
		}
		preview, inspection.Truncated = boundedPreview(preview, limit)
		return preview, "json", inspection.Truncated, inspection
	}

	preview, truncated := boundedPreview(string(payload), limit)
	inspection.Truncated = truncated
	return preview, "text", truncated, inspection
}

func isLikelyText(payload []byte) bool {
	if !utf8.Valid(payload) {
		return false
	}
	for _, r := range string(payload) {
		if r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		if r < 0x20 {
			return false
		}
	}
	return true
}

func jsonDetectedType(value any) string {
	switch value.(type) {
	case map[string]any:
		return "json_object"
	case []any:
		return "json_array"
	default:
		return "json_scalar"
	}
}

func populateJSONInspection(inspection *PayloadInspection, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, truncateStringUTF8(key, 64))
		}
		sort.Strings(keys)
		if len(keys) > 20 {
			keys = keys[:20]
		}
		inspection.JSONTopLevelKeys = keys
	case []any:
		inspection.JSONElementCount = len(typed)
	default:
		inspection.JSONScalarSummary = summarizeJSONScalar(typed)
	}
}

func summarizeJSONScalar(value any) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case string:
		preview, truncated := boundedPreview(typed, 80)
		if truncated {
			return fmt.Sprintf("string(len=%d, preview=%q, truncated)", len(typed), preview)
		}
		return fmt.Sprintf("string(len=%d, value=%q)", len(typed), preview)
	default:
		return fmt.Sprintf("%T", value)
	}
}

func boundedPreview(value string, limit int) (string, bool) {
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	return truncateStringUTF8(value, limit), true
}

func truncateStringUTF8(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.ValidString(value[:limit]) {
		limit--
	}
	return value[:limit]
}

func (a *App) handleBrokerEvents(w http.ResponseWriter, r *http.Request) {
	// Extract and verify bearer token from Sec-WebSocket-Protocol BEFORE upgrade
	token, ok := extractWebSocketBearer(r.Header.Get("Sec-WebSocket-Protocol"))
	if !ok {
		a.recordSecurityFailure(r, "protected_websocket_access_failed", "missing_bearer_token", "")
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	if _, err := a.tokens.VerifyAt(token, a.now().UTC()); err != nil {
		a.recordSecurityFailure(r, "protected_websocket_access_failed", "invalid_bearer_token", "")
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	// Accept the WebSocket upgrade, negotiating the subprotocol
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		Subprotocols: []string{webSocketSubprotocol},
	})
	if err != nil {
		return // nhooyr already wrote the error response
	}
	defer conn.CloseNow()

	events, unsubscribe := a.brokerEvents.Subscribe()
	defer unsubscribe()

	ctx := conn.CloseRead(r.Context())
	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case event, ok := <-events:
			if !ok {
				conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
				return
			}
		}
	}
}

const (
	webSocketSubprotocol  = "mcm.v1"
	webSocketBearerPrefix = "Bearer."
)

// extractWebSocketBearer reads the bearer token offered via Sec-WebSocket-Protocol.
// Clients advertise "mcm.v1, Bearer.<token>" so the JWT never lands in URL query logs.
func extractWebSocketBearer(header string) (string, bool) {
	if strings.TrimSpace(header) == "" {
		return "", false
	}
	for _, part := range strings.Split(header, ",") {
		trimmed := strings.TrimSpace(part)
		if len(trimmed) <= len(webSocketBearerPrefix) {
			continue
		}
		if !strings.EqualFold(trimmed[:len(webSocketBearerPrefix)], webSocketBearerPrefix) {
			continue
		}
		token := strings.TrimSpace(trimmed[len(webSocketBearerPrefix):])
		if token == "" {
			continue
		}
		return token, true
	}
	return "", false
}

// StartBrokerMonitor opens an MQTT subscription to "#" and bridges every received
// message into the broker event hub. The paho client handles reconnect, keepalive,
// and TLS; this function only translates its callbacks into BrokerEvent publishes
// and runs until ctx is cancelled.
func (a *App) StartBrokerMonitor(ctx context.Context, cfg config.MosquittoConfig) {
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	client := buildMQTTClient(cfg, address, a)

	a.logger.Info("broker_monitor_starting",
		slog.String("broker_address", address),
		slog.Bool("tls_enabled", cfg.TLS.Enabled),
		slog.Bool("authenticated", strings.TrimSpace(cfg.Username) != ""),
	)

	if token := client.Connect(); token.WaitTimeout(5*time.Second) && token.Error() != nil {
		a.logger.Warn("broker_initial_connect_failed",
			slog.String("broker_address", address),
			slog.String("error", token.Error().Error()),
		)
		a.publishBrokerStatus("disconnected", "warning", fmt.Sprintf("Broker initial connect failed: %v", token.Error()))
	}

	<-ctx.Done()
	a.logger.Info("broker_monitor_stopping", slog.String("broker_address", address))
	client.Disconnect(250)
}

func buildMQTTClient(cfg config.MosquittoConfig, address string, a *App) mqtt.Client {
	scheme := "tcp"
	if cfg.TLS.Enabled {
		scheme = "ssl"
	}

	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("%s://%s", scheme, address)).
		SetClientID(fmt.Sprintf("mcm-ui-%d", time.Now().UnixNano())).
		SetCleanSession(true).
		SetKeepAlive(30 * time.Second).
		SetPingTimeout(10 * time.Second).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(60 * time.Second).
		SetConnectRetry(true).
		SetConnectRetryInterval(1 * time.Second)

	if cfg.TLS.Enabled {
		serverName := cfg.Host
		if net.ParseIP(serverName) != nil {
			serverName = ""
		}
		opts.SetTLSConfig(&tls.Config{ //nolint:gosec // MCM honors the user-controlled Mosquitto TLS diagnostic option.
			ServerName:         serverName,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		})
	}
	if strings.TrimSpace(cfg.Username) != "" {
		opts.SetUsername(strings.TrimSpace(cfg.Username))
		opts.SetPassword(cfg.Password)
	}

	opts.SetDefaultPublishHandler(func(_ mqtt.Client, msg mqtt.Message) {
		a.brokerEvents.Publish(a.TopicEvent(msg.Topic(), msg.Payload(), maxPayloadPreviewBytes))
	})

	reconnectBackoff := backoff.New(1*time.Second, 60*time.Second, 0.25)
	const reconnectLogThreshold = 5

	opts.SetOnConnectHandler(func(client mqtt.Client) {
		attempts := reconnectBackoff.Attempt()
		reconnectBackoff.Reset()
		if token := client.Subscribe("#", 0, nil); token.WaitTimeout(5*time.Second) && token.Error() != nil {
			a.logger.Warn("broker_subscribe_failed",
				slog.String("broker_address", address),
				slog.String("error", token.Error().Error()),
			)
			a.publishBrokerStatus("disconnected", "warning", fmt.Sprintf("Broker subscribe failed: %v", token.Error()))
			return
		}
		if attempts > reconnectLogThreshold {
			a.logger.Info("broker_reconnected",
				slog.String("broker_address", address),
				slog.Int("suppressed_attempts", attempts-reconnectLogThreshold),
			)
		} else {
			a.logger.Info("broker_connected", slog.String("broker_address", address))
		}
		a.publishBrokerStatus("connected", "info", fmt.Sprintf("Broker connected to %s", address))
	})

	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		attempt := reconnectBackoff.Attempt()
		nextDelay := reconnectBackoff.Next()
		message := "Broker disconnected"
		if err != nil {
			message = fmt.Sprintf("Broker disconnected: %v", err)
		}
		if attempt < reconnectLogThreshold {
			a.logger.Warn("broker_disconnected",
				slog.String("broker_address", address),
				slog.String("error", fmt.Sprint(err)),
				slog.String("next_retry_in", nextDelay.Truncate(time.Millisecond).String()),
			)
		} else if attempt == reconnectLogThreshold {
			a.logger.Warn("broker_reconnect_logs_suppressed",
				slog.String("broker_address", address),
				slog.String("reason", "repeated disconnects; further attempts logged on reconnect"),
			)
		}
		a.publishBrokerStatus("disconnected", "warning", message)
	})

	return mqtt.NewClient(opts)
}
