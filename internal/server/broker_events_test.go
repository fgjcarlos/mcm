package server

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/storage"
)

func TestBrokerEventsWebSocketSendsStatusAndTopicEvents(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	server := httptest.NewServer(app.Handler())
	t.Cleanup(server.Close)

	conn, reader := openTestWebSocket(t, server.URL, "/api/v1/broker/events")
	t.Cleanup(func() { _ = conn.Close() })

	initialFrame := readTestWebSocketFrame(t, reader)
	if !strings.Contains(initialFrame, `"type":"broker_status"`) || !strings.Contains(initialFrame, `"status":"disconnected"`) {
		t.Fatalf("initial websocket frame = %s, want disconnected broker status", initialFrame)
	}

	app.brokerEvents.Publish(TopicEvent("factory/line1/temperature", []byte(`{"temperature":21.5,"unit":"c"}`), 256))

	topicFrame := readTestWebSocketFrame(t, reader)
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

func openTestWebSocket(t *testing.T, serverURL string, path string) (net.Conn, *bufio.Reader) {
	t.Helper()

	addr := strings.TrimPrefix(serverURL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("Dial returned error: %v", err)
	}

	request := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + addr + "\r\n" +
		"Connection: Upgrade\r\n" +
		"Upgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("Write handshake returned error: %v", err)
	}

	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("ReadResponse returned error: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("websocket status = %d, want %d", response.StatusCode, http.StatusSwitchingProtocols)
	}
	return conn, reader
}

func readTestWebSocketFrame(t *testing.T, reader *bufio.Reader) string {
	t.Helper()

	first, err := reader.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte opcode returned error: %v", err)
	}
	if first != 0x81 {
		t.Fatalf("frame first byte = %#x, want text frame", first)
	}
	lengthByte, err := reader.ReadByte()
	if err != nil {
		t.Fatalf("ReadByte length returned error: %v", err)
	}
	length := int(lengthByte & 0x7f)
	if length == 126 {
		var value uint16
		if err := binary.Read(reader, binary.BigEndian, &value); err != nil {
			t.Fatalf("Read extended length returned error: %v", err)
		}
		length = int(value)
	} else if length == 127 {
		var value uint64
		if err := binary.Read(reader, binary.BigEndian, &value); err != nil {
			t.Fatalf("Read extended length returned error: %v", err)
		}
		length = int(value)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(reader, payload); err != nil {
		t.Fatalf("Read payload returned error: %v", err)
	}
	if !json.Valid(payload) {
		t.Fatalf("websocket frame payload is not JSON: %s", string(payload))
	}
	return string(payload)
}
