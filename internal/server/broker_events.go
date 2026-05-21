package server

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fgjcarlos/mcm/internal/config"
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
	Type           string    `json:"type"`
	Status         string    `json:"status,omitempty"`
	Topic          string    `json:"topic,omitempty"`
	PayloadPreview string    `json:"payload_preview,omitempty"`
	PayloadFormat  string    `json:"payload_format,omitempty"`
	PayloadBytes   int       `json:"payload_bytes,omitempty"`
	Truncated      bool      `json:"truncated,omitempty"`
	Source         string    `json:"source,omitempty"`
	Severity       string    `json:"severity,omitempty"`
	Message        string    `json:"message,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
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
	for ch := range h.subscribers {
		select {
		case ch <- event:
		default:
		}
	}
	h.mu.Unlock()

	persistBrokerEvent(store, retention, event)
}

func (h *BrokerEventHub) Snapshot() BrokerEventSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()

	var lastMessageAt *time.Time
	if h.lastMessageAt != nil {
		value := *h.lastMessageAt
		lastMessageAt = &value
	}

	return BrokerEventSnapshot{
		Status:           h.status,
		EventSubscribers: len(h.subscribers),
		StatusEvents:     h.statusEvents,
		TopicMessages:    h.topicMessages,
		LastMessageAt:    lastMessageAt,
		Traffic:          h.trafficMetricsLocked(time.Now().UTC()),
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

func (h *BrokerEventHub) trafficMetricsLocked(now time.Time) BrokerTrafficMetrics {
	events := append([]BrokerEvent(nil), h.trafficEvents...)
	store := h.store
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
	a.brokerEvents.Publish(BrokerStatusEvent(status))
	if strings.TrimSpace(logMessage) != "" {
		a.brokerEvents.Publish(BrokerLogEvent("broker", logSeverity, logMessage))
	}
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

	truncated := len(payload) > limit
	previewBytes := payload
	if truncated {
		previewBytes = payload[:limit]
	}

	preview := string(previewBytes)
	format := "text"
	var formatted any
	if json.Valid(payload) && json.Unmarshal(payload, &formatted) == nil {
		format = "json"
		if data, err := json.MarshalIndent(formatted, "", "  "); err == nil {
			preview = string(data)
			if len(preview) > limit {
				preview = preview[:limit]
				truncated = true
			}
		}
	}

	return BrokerEvent{
		Type:           "topic_message",
		Topic:          topic,
		PayloadPreview: preview,
		PayloadFormat:  format,
		PayloadBytes:   len(payload),
		Truncated:      truncated,
		ObservedAt:     time.Now().UTC(),
	}
}

func (a *App) handleBrokerEvents(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketRequest(r) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "websocket upgrade required"})
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "websocket upgrade unsupported"})
		return
	}

	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()

	accept, err := websocketAcceptKey(r.Header.Get("Sec-WebSocket-Key"))
	if err != nil {
		_, _ = rw.WriteString("HTTP/1.1 400 Bad Request\r\n\r\n")
		_ = rw.Flush()
		return
	}

	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	_, _ = rw.WriteString("Upgrade: websocket\r\n")
	_, _ = rw.WriteString("Connection: Upgrade\r\n")
	_, _ = rw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n\r\n")
	if err := rw.Flush(); err != nil {
		return
	}

	events, unsubscribe := a.brokerEvents.Subscribe()
	defer unsubscribe()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if err := writeWebSocketTextFrame(rw, data); err != nil {
				return
			}
		}
	}
}

func isWebSocketRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") && strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

func websocketAcceptKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("missing Sec-WebSocket-Key")
	}
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:]), nil
}

func writeWebSocketTextFrame(rw interface {
	Write([]byte) (int, error)
	Flush() error
}, payload []byte) error {
	frame := []byte{0x81}
	length := len(payload)
	switch {
	case length < 126:
		frame = append(frame, byte(length))
	case length <= 65535:
		frame = append(frame, 126, 0, 0)
		binary.BigEndian.PutUint16(frame[len(frame)-2:], uint16(length))
	default:
		frame = append(frame, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(frame[len(frame)-8:], uint64(length))
	}
	frame = append(frame, payload...)
	if _, err := rw.Write(frame); err != nil {
		return err
	}
	return rw.Flush()
}

func (a *App) StartBrokerMonitor(ctx context.Context, cfg config.MosquittoConfig) {
	for {
		if err := a.streamMQTTBroker(ctx, cfg); err != nil {
			if ctx.Err() != nil {
				return
			}
			a.publishBrokerStatus("disconnected", "warning", fmt.Sprintf("Broker disconnected: %v", err))
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (a *App) streamMQTTBroker(ctx context.Context, cfg config.MosquittoConfig) error {
	address := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	defer conn.Close()

	if cfg.TLS.Enabled {
		serverName := cfg.Host
		if net.ParseIP(serverName) != nil {
			serverName = ""
		}
		tlsConn := tls.Client(conn, &tls.Config{ //nolint:gosec // MCM honors the user-controlled Mosquitto TLS diagnostic option.
			ServerName:         serverName,
			InsecureSkipVerify: cfg.TLS.InsecureSkipVerify,
		})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		conn = tlsConn
	}

	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.Write(buildMQTTConnectPacket(cfg)); err != nil {
		return err
	}
	packetType, payload, err := readMQTTPacket(conn)
	if err != nil {
		return err
	}
	if packetType != 2 || len(payload) < 2 || payload[1] != 0 {
		return fmt.Errorf("MQTT broker rejected connection")
	}
	if _, err := conn.Write(buildMQTTSubscribePacket("#")); err != nil {
		return err
	}
	packetType, _, err = readMQTTPacket(conn)
	if err != nil {
		return err
	}
	if packetType != 9 {
		return fmt.Errorf("unexpected MQTT SUBACK packet type %d", packetType)
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return err
	}

	a.publishBrokerStatus("connected", "info", fmt.Sprintf("Broker connected to %s", address))
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		packetType, payload, err := readMQTTPacket(conn)
		if err != nil {
			return err
		}
		if packetType == 3 {
			topic, message, ok := parseMQTTPublish(payload)
			if ok {
				a.brokerEvents.Publish(TopicEvent(topic, message, maxPayloadPreviewBytes))
			}
		}
	}
}

func buildMQTTConnectPacket(cfg config.MosquittoConfig) []byte {
	clientID := fmt.Sprintf("mcm-ui-%d", time.Now().UnixNano())
	flags := byte(0x02)
	if strings.TrimSpace(cfg.Username) != "" {
		flags |= 0x80 | 0x40
	}
	variableHeader := []byte{0, 4, 'M', 'Q', 'T', 'T', 4, flags, 0, 30}
	payload := appendMQTTString(nil, clientID)
	if strings.TrimSpace(cfg.Username) != "" {
		payload = appendMQTTString(payload, strings.TrimSpace(cfg.Username))
		payload = appendMQTTString(payload, cfg.Password)
	}
	packet := []byte{0x10}
	packet = append(packet, encodeMQTTRemainingLength(len(variableHeader)+len(payload))...)
	packet = append(packet, variableHeader...)
	packet = append(packet, payload...)
	return packet
}

func buildMQTTSubscribePacket(topic string) []byte {
	variableHeader := []byte{0, 1}
	payload := appendMQTTString(nil, topic)
	payload = append(payload, 0) // QoS 0
	packet := []byte{0x82}
	packet = append(packet, encodeMQTTRemainingLength(len(variableHeader)+len(payload))...)
	packet = append(packet, variableHeader...)
	packet = append(packet, payload...)
	return packet
}

func readMQTTPacket(r io.Reader) (byte, []byte, error) {
	fixed := make([]byte, 1)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return 0, nil, err
	}
	remainingLength, err := readMQTTRemainingLength(r)
	if err != nil {
		return 0, nil, err
	}
	payload := make([]byte, remainingLength)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return fixed[0] >> 4, payload, nil
}

func readMQTTRemainingLength(r io.Reader) (int, error) {
	multiplier := 1
	value := 0
	for i := 0; i < 4; i++ {
		encoded := make([]byte, 1)
		if _, err := io.ReadFull(r, encoded); err != nil {
			return 0, err
		}
		value += int(encoded[0]&127) * multiplier
		if encoded[0]&128 == 0 {
			return value, nil
		}
		multiplier *= 128
	}
	return 0, fmt.Errorf("malformed MQTT remaining length")
}

func parseMQTTPublish(payload []byte) (string, []byte, bool) {
	if len(payload) < 2 {
		return "", nil, false
	}
	topicLength := int(binary.BigEndian.Uint16(payload[:2]))
	if len(payload) < 2+topicLength {
		return "", nil, false
	}
	return string(payload[2 : 2+topicLength]), payload[2+topicLength:], true
}

func appendMQTTString(packet []byte, value string) []byte {
	packet = append(packet, byte(len(value)>>8), byte(len(value)))
	packet = append(packet, value...)
	return packet
}

func encodeMQTTRemainingLength(length int) []byte {
	encoded := make([]byte, 0, 4)
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		encoded = append(encoded, digit)
		if length == 0 {
			return encoded
		}
	}
}
