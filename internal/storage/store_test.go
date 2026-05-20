package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationsCreateBrokerMetricTables(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for _, table := range []string{"broker_metric_events", "broker_metric_samples"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s was not created: %v", table, err)
		}
	}

	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 2`).Scan(&version); err != nil {
		t.Fatalf("broker metrics migration was not recorded: %v", err)
	}
}

func TestRecordAndListBrokerMetricEventAndSample(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	observedAt := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	event, err := store.RecordBrokerMetricEvent(context.Background(), CreateBrokerMetricEventParams{
		Type:          "topic_message",
		Topic:         "factory/line1/temperature",
		PayloadFormat: "json",
		PayloadBytes:  42,
		Truncated:     true,
		ObservedAt:    observedAt,
	})
	if err != nil {
		t.Fatalf("RecordBrokerMetricEvent returned error: %v", err)
	}
	if event.ID == 0 || event.PayloadBytes != 42 || !event.Truncated {
		t.Fatalf("unexpected recorded event: %#v", event)
	}

	events, err := store.ListBrokerMetricEvents(context.Background(), BrokerMetricQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListBrokerMetricEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	if events[0].Topic != "factory/line1/temperature" || events[0].PayloadFormat != "json" || !events[0].ObservedAt.Equal(observedAt) {
		t.Fatalf("unexpected event: %#v", events[0])
	}

	samples, err := store.ListBrokerMetricSamples(context.Background(), BrokerMetricQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListBrokerMetricSamples returned error: %v", err)
	}
	if len(samples) != 1 {
		t.Fatalf("got %d samples, want 1", len(samples))
	}
	if samples[0].MessagesTotal != 1 || samples[0].PayloadBytesTotal != 42 {
		t.Fatalf("unexpected sample counters: %#v", samples[0])
	}
}

func TestPruneBrokerMetrics(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	oldTime := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	newTime := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	for _, observedAt := range []time.Time{oldTime, newTime} {
		if _, err := store.RecordBrokerMetricEvent(ctx, CreateBrokerMetricEventParams{Type: "broker_status", Status: "connected", ObservedAt: observedAt}); err != nil {
			t.Fatalf("RecordBrokerMetricEvent returned error: %v", err)
		}
	}

	eventsDeleted, samplesDeleted, err := store.PruneBrokerMetrics(ctx, time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("PruneBrokerMetrics returned error: %v", err)
	}
	if eventsDeleted != 1 || samplesDeleted != 1 {
		t.Fatalf("deleted events/samples = %d/%d, want 1/1", eventsDeleted, samplesDeleted)
	}

	events, err := store.ListBrokerMetricEvents(ctx, BrokerMetricQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListBrokerMetricEvents returned error: %v", err)
	}
	if len(events) != 1 || !events[0].ObservedAt.Equal(newTime) {
		t.Fatalf("unexpected remaining events: %#v", events)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "mcm.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	return store
}
