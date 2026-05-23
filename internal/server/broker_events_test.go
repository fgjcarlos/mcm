package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

func TestBrokerEventsWebSocketRejectsMissingBearerToken(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	response := dialTestWebSocketRaw(t, server.URL, "/api/v1/broker/events", "")

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("websocket status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "protected_websocket_access_failed",
		Reason:   "missing_bearer_token",
		Method:   http.MethodGet,
		Path:     "/api/v1/broker/events",
	})
}

func TestBrokerEventsWebSocketRejectsInvalidBearerToken(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	response := dialTestWebSocketRaw(t, server.URL, "/api/v1/broker/events", "mcm.v1, Bearer.not-a-valid-token")

	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("websocket status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}

	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "protected_websocket_access_failed",
		Reason:   "invalid_bearer_token",
		Method:   http.MethodGet,
		Path:     "/api/v1/broker/events",
	})
}

func TestExtractWebSocketBearer(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{"empty", "", "", false},
		{"only subprotocol", "mcm.v1", "", false},
		{"empty bearer", "mcm.v1, Bearer.", "", false},
		{"valid", "mcm.v1, Bearer.abc.def.ghi", "abc.def.ghi", true},
		{"case insensitive prefix", "MCM.v1, bearer.abc", "abc", true},
		{"token with padding stripped", " mcm.v1 , Bearer.abc ", "abc", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := extractWebSocketBearer(tc.header)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("extractWebSocketBearer(%q) = (%q,%v), want (%q,%v)", tc.header, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestBrokerEventsWebSocketSendsStatusAndTopicEvents(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn := openAuthorizedTestWebSocket(t, ctx, app, server.URL, "/api/v1/broker/events")
	defer conn.CloseNow()

	initialFrame := readTestWebSocketFrame(t, ctx, conn)
	if !strings.Contains(initialFrame, `"type":"broker_status"`) || !strings.Contains(initialFrame, `"status":"disconnected"`) {
		t.Fatalf("initial websocket frame = %s, want disconnected broker status", initialFrame)
	}

	app.brokerEvents.Publish(TopicEvent("factory/line1/temperature", []byte(`{"temperature":21.5,"unit":"c"}`), 256))

	topicFrame := readTestWebSocketFrame(t, ctx, conn)
	if !strings.Contains(topicFrame, `"type":"topic_message"`) {
		t.Fatalf("topic websocket frame = %s, want topic message event", topicFrame)
	}
	if !strings.Contains(topicFrame, `"topic":"factory/line1/temperature"`) {
		t.Fatalf("topic websocket frame missing topic: %s", topicFrame)
	}
	if !strings.Contains(topicFrame, `"payload_format":"json"`) || !strings.Contains(topicFrame, "temperature") {
		t.Fatalf("topic websocket frame missing formatted JSON payload: %s", topicFrame)
	}
}

func TestTopicEventTruncatesLargePayloads(t *testing.T) {
	event := TopicEvent("factory/large", []byte("1234567890"), 6)
	if !event.Truncated {
		t.Fatal("TopicEvent truncated=false, want true")
	}
	if event.PayloadPreview != "123456" {
		t.Fatalf("PayloadPreview = %q, want %q", event.PayloadPreview, "123456")
	}
	if event.PayloadBytes != 10 {
		t.Fatalf("PayloadBytes = %d, want 10", event.PayloadBytes)
	}
	if event.Payload == nil || !event.Payload.Truncated || event.Payload.ByteLength != 10 {
		t.Fatalf("Payload inspection = %+v, want truncated metadata for 10 bytes", event.Payload)
	}
	if event.Sparkplug != nil {
		t.Fatalf("Sparkplug = %+v, want nil for generic MQTT topic", event.Sparkplug)
	}
}

func TestTopicEventInspectsJSONObject(t *testing.T) {
	event := TopicEvent("factory/json", []byte(`{"unit":"c","temperature":21.5,"line":"one"}`), 256)
	if event.PayloadFormat != "json" || event.Payload == nil {
		t.Fatalf("event payload metadata = format %q inspection %+v, want json inspection", event.PayloadFormat, event.Payload)
	}
	if event.Payload.DetectedType != "json_object" || !event.Payload.JSONValid {
		t.Fatalf("inspection = %+v, want valid json object", event.Payload)
	}
	wantKeys := []string{"line", "temperature", "unit"}
	if fmt.Sprint(event.Payload.JSONTopLevelKeys) != fmt.Sprint(wantKeys) {
		t.Fatalf("top-level keys = %v, want %v", event.Payload.JSONTopLevelKeys, wantKeys)
	}
	if !strings.Contains(event.PayloadPreview, "temperature") {
		t.Fatalf("PayloadPreview = %q, want formatted JSON preview", event.PayloadPreview)
	}
}

func TestTopicEventInspectsJSONArray(t *testing.T) {
	event := TopicEvent("factory/array", []byte(`[1,{"ok":true},3]`), 256)
	if event.Payload == nil || event.Payload.DetectedType != "json_array" || event.Payload.JSONElementCount != 3 {
		t.Fatalf("inspection = %+v, want json array element count 3", event.Payload)
	}
}

func TestTopicEventInspectsJSONScalar(t *testing.T) {
	event := TopicEvent("factory/scalar", []byte(`"active"`), 256)
	if event.Payload == nil || event.Payload.DetectedType != "json_scalar" || !strings.Contains(event.Payload.JSONScalarSummary, "active") {
		t.Fatalf("inspection = %+v, want json scalar summary", event.Payload)
	}
}

func TestTopicEventKeepsInvalidJSONAsSafeTextPreview(t *testing.T) {
	event := TopicEvent("factory/invalid-json", []byte(`{"unit":"c"`), 256)
	if event.PayloadFormat != "text" || event.Payload == nil || event.Payload.JSONValid {
		t.Fatalf("event = %+v inspection %+v, want invalid JSON treated as text", event, event.Payload)
	}
	if event.PayloadPreview != `{"unit":"c"` {
		t.Fatalf("PayloadPreview = %q, want existing text preview", event.PayloadPreview)
	}
}

func TestTopicEventClassifiesPlainText(t *testing.T) {
	event := TopicEvent("factory/text", []byte("pump started"), 256)
	if event.PayloadFormat != "text" || event.Payload == nil || event.Payload.DetectedType != "text" || event.Payload.JSONValid {
		t.Fatalf("event = %+v inspection %+v, want text metadata", event, event.Payload)
	}
}

func TestTopicEventOmitsBinaryLikeRawPayload(t *testing.T) {
	event := TopicEvent("factory/binary", []byte{0x00, 0xff, 's', 'e', 'c', 'r', 'e', 't'}, 256)
	if event.PayloadFormat != "binary" || event.Payload == nil || event.Payload.DetectedType != "binary" {
		t.Fatalf("event = %+v inspection %+v, want binary metadata", event, event.Payload)
	}
	if strings.Contains(event.PayloadPreview, "secret") || !strings.Contains(event.PayloadPreview, "binary payload omitted") {
		t.Fatalf("PayloadPreview = %q, want bounded binary omission without raw bytes", event.PayloadPreview)
	}
}

func TestTopicEventAddsSparkplugMetadataForSparkplugTopics(t *testing.T) {
	event := TopicEvent("spBv1.0/PlantA/DDATA/Line1/Motor7", []byte("protobuf bytes"), 256)
	if event.Sparkplug == nil {
		t.Fatal("Sparkplug = nil, want metadata")
	}
	if event.Sparkplug.Namespace != "spBv1.0" || event.Sparkplug.GroupID != "PlantA" || event.Sparkplug.MessageType != "DDATA" || event.Sparkplug.EdgeNodeID != "Line1" || event.Sparkplug.DeviceID != "Motor7" {
		t.Fatalf("Sparkplug metadata = %+v, want parsed DDATA metadata", event.Sparkplug)
	}
	if event.PayloadPreview == "" || event.PayloadBytes == 0 || event.Payload == nil {
		t.Fatalf("TopicEvent lost payload preview/inspection metadata: %+v", event)
	}
}

func TestBrokerEventHubPersistsBrokerMetrics(t *testing.T) {
	app, store := newTestApp(t)
	defer store.Close()

	app.brokerEvents.Publish(TopicEvent("factory/line1", []byte("hello"), 256))

	events, err := store.ListBrokerMetricEvents(context.Background(), storage.BrokerMetricQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListBrokerMetricEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d persisted broker metric events, want 1", len(events))
	}
	if events[0].Topic != "factory/line1" || events[0].PayloadBytes != 5 || events[0].PayloadFormat != "text" {
		t.Fatalf("unexpected persisted broker metric event: %#v", events[0])
	}
	if events[0].PayloadFormat == "hello" {
		t.Fatal("raw payload was persisted")
	}
}

func TestComputeBrokerTrafficMetricsAggregatesRecentTopicHotspots(t *testing.T) {
	now := time.Date(2026, 5, 21, 14, 5, 30, 0, time.UTC)
	events := []BrokerEvent{
		{Type: "topic_message", Topic: "factory/line1/temp", ObservedAt: now.Add(-30 * time.Second)},
		{Type: "topic_message", Topic: "factory/line1/temp", ObservedAt: now.Add(-90 * time.Second)},
		{Type: "topic_message", Topic: "factory/line2/temp", ObservedAt: now.Add(-2 * time.Minute)},
		{Type: "topic_message", Topic: "factory/old", ObservedAt: now.Add(-10 * time.Minute)},
		{Type: "broker_log", Message: "ignored", ObservedAt: now.Add(-time.Minute)},
	}

	metrics := computeBrokerTrafficMetrics(events, now, 5*time.Minute, 5)

	if metrics.MessageCount != 3 {
		t.Fatalf("MessageCount = %d, want 3", metrics.MessageCount)
	}
	if metrics.MessageRatePerMinute != 0.6 {
		t.Fatalf("MessageRatePerMinute = %v, want 0.6", metrics.MessageRatePerMinute)
	}
	if len(metrics.TopTopics) != 2 {
		t.Fatalf("TopTopics length = %d, want 2", len(metrics.TopTopics))
	}
	if metrics.TopTopics[0].Name != "factory/line1/temp" || metrics.TopTopics[0].Count != 2 {
		t.Fatalf("top topic = %+v, want factory/line1/temp count 2", metrics.TopTopics[0])
	}
	if metrics.TopClientsAvailable || metrics.TopClientsNote == "" {
		t.Fatalf("top client availability = %v note %q, want unavailable with note", metrics.TopClientsAvailable, metrics.TopClientsNote)
	}
	if len(metrics.RatePoints) == 0 {
		t.Fatal("RatePoints is empty")
	}
}

func TestBrokerEventHubSnapshotIncludesPersistedTrafficMetrics(t *testing.T) {
	app, store := newTestApp(t)
	defer store.Close()

	now := time.Now().UTC()
	app.brokerEvents.Publish(BrokerEvent{Type: "topic_message", Topic: "factory/persisted", ObservedAt: now.Add(-time.Minute)})

	restarted := NewBrokerEventHub()
	restarted.SetPersistence(store, time.Hour)
	snapshot := restarted.Snapshot()

	if snapshot.Traffic.MessageCount != 1 {
		t.Fatalf("persisted traffic message count = %d, want 1", snapshot.Traffic.MessageCount)
	}
	if len(snapshot.Traffic.TopTopics) != 1 || snapshot.Traffic.TopTopics[0].Name != "factory/persisted" {
		t.Fatalf("persisted traffic top topics = %+v, want factory/persisted", snapshot.Traffic.TopTopics)
	}
	if !strings.Contains(snapshot.Traffic.Persistence, "persisted") {
		t.Fatalf("traffic persistence note = %q, want persisted note", snapshot.Traffic.Persistence)
	}
}

func TestBrokerEventHubFansOutLogEvents(t *testing.T) {
	hub := NewBrokerEventHub()
	first, unsubscribeFirst := hub.Subscribe()
	defer unsubscribeFirst()
	second, unsubscribeSecond := hub.Subscribe()
	defer unsubscribeSecond()

	drainInitialStatus(t, first)
	drainInitialStatus(t, second)

	hub.Publish(BrokerLogEvent("broker", "info", "Broker connected"))

	for name, ch := range map[string]<-chan BrokerEvent{"first": first, "second": second} {
		event := readBrokerEvent(t, ch)
		if event.Type != "broker_log" {
			t.Fatalf("%s subscriber event type = %q, want broker_log", name, event.Type)
		}
		if event.Source != "broker" || event.Severity != "info" || event.Message != "Broker connected" {
			t.Fatalf("%s subscriber log event = %+v, want broker/info/Broker connected", name, event)
		}
	}
}

func TestBrokerEventHubReplaysBoundedLogBuffer(t *testing.T) {
	hub := NewBrokerEventHub()
	for i := 0; i < maxBrokerLogBuffer+5; i++ {
		hub.Publish(BrokerLogEvent("broker", "debug", fmt.Sprintf("log-%03d", i)))
	}

	events, unsubscribe := hub.Subscribe()
	defer unsubscribe()
	drainInitialStatus(t, events)

	count := 0
	var firstLog BrokerEvent
	for {
		select {
		case event := <-events:
			if event.Type != "broker_log" {
				t.Fatalf("replayed event type = %q, want broker_log", event.Type)
			}
			if count == 0 {
				firstLog = event
			}
			count++
		case <-time.After(25 * time.Millisecond):
			if count != maxBrokerLogBuffer {
				t.Fatalf("replayed log count = %d, want %d", count, maxBrokerLogBuffer)
			}
			if firstLog.Message != "log-005" {
				t.Fatalf("first replayed log = %q, want oldest retained log-005", firstLog.Message)
			}
			return
		}
	}
}

func drainInitialStatus(t *testing.T, ch <-chan BrokerEvent) {
	t.Helper()
	event := readBrokerEvent(t, ch)
	if event.Type != "broker_status" || event.Status != "disconnected" {
		t.Fatalf("initial event = %+v, want disconnected broker status", event)
	}
}

func readBrokerEvent(t *testing.T, ch <-chan BrokerEvent) BrokerEvent {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timed out waiting for broker event")
	}
	return BrokerEvent{}
}

// dialTestWebSocketRaw performs a raw HTTP WebSocket upgrade request and returns the
// HTTP response without completing the WebSocket handshake. This is used for auth
// rejection tests where the server returns a non-101 response before any upgrade.
func dialTestWebSocketRaw(t *testing.T, serverURL string, path string, protocolHeader string) *http.Response {
	t.Helper()

	addr := strings.TrimPrefix(serverURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}
	defer conn.Close()

	request := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
	if protocolHeader != "" {
		request += "Sec-WebSocket-Protocol: " + protocolHeader + "\r\n"
	}
	request += "\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("Write handshake returned error: %v", err)
	}

	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("ReadResponse returned error: %v", err)
	}
	return response
}

// openAuthorizedTestWebSocket dials a WebSocket using nhooyr.io/websocket with a valid
// bearer token in the Sec-WebSocket-Protocol header. It asserts the handshake succeeds
// and the negotiated subprotocol matches webSocketSubprotocol.
func openAuthorizedTestWebSocket(t *testing.T, ctx context.Context, app *App, serverURL string, path string) *websocket.Conn {
	t.Helper()

	token, _, err := app.tokens.Issue(1, "ws-test", auth.RoleAdmin, app.now().UTC())
	if err != nil {
		t.Fatalf("Issue token returned error: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + path
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{webSocketSubprotocol},
		HTTPHeader: http.Header{
			"Sec-WebSocket-Protocol": []string{"mcm.v1, Bearer." + token},
		},
	})
	if err != nil {
		t.Fatalf("websocket.Dial returned error: %v", err)
	}
	if got := conn.Subprotocol(); got != webSocketSubprotocol {
		conn.CloseNow()
		t.Fatalf("negotiated subprotocol = %q, want %q", got, webSocketSubprotocol)
	}
	return conn
}

// readTestWebSocketFrame reads a single text message from the WebSocket connection and
// asserts it is valid JSON. It returns the payload as a string.
func readTestWebSocketFrame(t *testing.T, ctx context.Context, conn *websocket.Conn) string {
	t.Helper()

	msgType, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("conn.Read returned error: %v", err)
	}
	if msgType != websocket.MessageText {
		t.Fatalf("message type = %v, want text frame", msgType)
	}
	if !json.Valid(payload) {
		t.Fatalf("websocket frame payload is not JSON: %s", string(payload))
	}
	return string(payload)
}
