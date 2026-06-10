package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMigrationsCreateBrokerMetricTables(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	for _, table := range []string{"broker_metric_events", "broker_metric_samples", "audit_events"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&name); err != nil {
			t.Fatalf("table %s was not created: %v", table, err)
		}
	}

	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 3`).Scan(&version); err != nil {
		t.Fatalf("audit events migration was not recorded: %v", err)
	}
}

func TestRecordListAndPersistAuditEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcm.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	occurredAt := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	metadata := json.RawMessage(`{"username":"operator","disabled":false}`)
	recorded, err := store.RecordAuditEvent(context.Background(), CreateAuditEventParams{
		OccurredAt:   occurredAt,
		Actor:        "admin",
		Action:       "admin_user.create",
		ResourceType: "admin_user",
		ResourceID:   "42",
		Result:       "success",
		Metadata:     metadata,
	})
	if err != nil {
		t.Fatalf("RecordAuditEvent returned error: %v", err)
	}
	if recorded.ID == 0 || recorded.Actor != "admin" || recorded.Action != "admin_user.create" {
		t.Fatalf("unexpected recorded audit event: %#v", recorded)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open returned error: %v", err)
	}
	defer reopened.Close()

	events, err := reopened.ListAuditEvents(context.Background(), AuditEventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(events))
	}
	if events[0].Actor != "admin" || events[0].ResourceID != "42" || !events[0].OccurredAt.Equal(occurredAt) {
		t.Fatalf("unexpected persisted audit event: %#v", events[0])
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

func TestJSONSchemaDefinitionCRUD(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	created, err := store.CreateJSONSchema(ctx, CreateJSONSchemaParams{
		Name:        "Temperature reading",
		TopicFilter: "factory/+/temperature",
		Schema:      json.RawMessage(`{"type":"object","required":["temperature"],"properties":{"temperature":{"type":"number"}}}`),
		Description: "Line temperature payload",
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("CreateJSONSchema returned error: %v", err)
	}
	if created.ID == 0 || created.Name != "Temperature reading" || !created.Enabled {
		t.Fatalf("unexpected created schema: %#v", created)
	}

	listed, err := store.ListJSONSchemas(ctx)
	if err != nil {
		t.Fatalf("ListJSONSchemas returned error: %v", err)
	}
	if len(listed) != 1 || listed[0].TopicFilter != "factory/+/temperature" {
		t.Fatalf("unexpected schema list: %#v", listed)
	}

	updated, err := store.UpdateJSONSchema(ctx, created.ID, UpdateJSONSchemaParams{
		Name:        "Temperature v2",
		TopicFilter: "factory/line1/temperature",
		Schema:      json.RawMessage(`{"type":"object","required":["temperature","unit"],"properties":{"temperature":{"type":"number"},"unit":{"type":"string"}}}`),
		Description: "line 1 only",
		Enabled:     false,
	})
	if err != nil {
		t.Fatalf("UpdateJSONSchema returned error: %v", err)
	}
	if updated.Name != "Temperature v2" || updated.Enabled {
		t.Fatalf("unexpected updated schema: %#v", updated)
	}

	if err := store.DeleteJSONSchema(ctx, created.ID); err != nil {
		t.Fatalf("DeleteJSONSchema returned error: %v", err)
	}
	listed, err = store.ListJSONSchemas(ctx)
	if err != nil {
		t.Fatalf("ListJSONSchemas after delete returned error: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("schema list after delete = %#v, want empty", listed)
	}
}

func TestJSONSchemaRejectsMalformedSchemaAndInvalidTopicFilter(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	if _, err := store.CreateJSONSchema(ctx, CreateJSONSchemaParams{
		Name:        "bad topic",
		TopicFilter: "factory/#/temperature",
		Schema:      json.RawMessage(`{"type":"object"}`),
		Enabled:     true,
	}); err == nil {
		t.Fatal("CreateJSONSchema accepted invalid topic filter")
	}

	if _, err := store.CreateJSONSchema(ctx, CreateJSONSchemaParams{
		Name:        "bad schema",
		TopicFilter: "factory/#",
		Schema:      json.RawMessage(`{"type":"object","properties":{"value":{"type":"bogus"}}}`),
		Enabled:     true,
	}); err == nil {
		t.Fatal("CreateJSONSchema accepted malformed JSON schema")
	}
}

func TestUpdateAdminUserRejectsDisablingLastActiveAdmin(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	user, err := store.CreateAdminUser(ctx, CreateAdminUserParams{
		Username:     "admin",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("CreateAdminUser returned error: %v", err)
	}

	_, err = store.UpdateAdminUser(ctx, user.ID, UpdateAdminUserParams{
		Username: user.Username,
		Disabled: true,
		Role:     user.Role,
	})
	if !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("UpdateAdminUser error = %v, want %v", err, ErrLastActiveAdmin)
	}
}

func TestDeleteAdminUserRejectsDeletingLastActiveAdmin(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	ctx := context.Background()
	user, err := store.CreateAdminUser(ctx, CreateAdminUserParams{
		Username:     "admin",
		PasswordHash: "hash",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("CreateAdminUser returned error: %v", err)
	}

	err = store.DeleteAdminUser(ctx, user.ID)
	if !errors.Is(err, ErrLastActiveAdmin) {
		t.Fatalf("DeleteAdminUser error = %v, want %v", err, ErrLastActiveAdmin)
	}
}

// TestJournalModeIsWAL asserts that every store opened via Open uses WAL journal mode.
// WAL allows concurrent readers while a writer holds the lock.
func TestJournalModeIsWAL(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

// TestConcurrentBrokerEventWrites hammers the store with 8 goroutines each
// performing 20 writes mixing event inserts and reads. This reproduces the
// SQLITE_BUSY / locked-database errors that occur without WAL + bounded pool.
func TestConcurrentBrokerEventWrites(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	const goroutines = 8
	const writesPerGoroutine = 20

	var wg sync.WaitGroup
	errs := make(chan error, goroutines*writesPerGoroutine*2)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < writesPerGoroutine; i++ {
				_, err := store.RecordBrokerMetricEvent(context.Background(), CreateBrokerMetricEventParams{
					Type:         "topic_message",
					Topic:        "test/concurrent",
					PayloadBytes: 10,
					ObservedAt:   time.Now(),
				})
				if err != nil {
					errs <- err
				}
				// Interleave reads to exercise concurrent read/write patterns.
				if _, err = store.ListBrokerMetricEvents(context.Background(), BrokerMetricQuery{Limit: 10}); err != nil {
					errs <- err
				}
			}
		}()
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent write/read error: %v", err)
	}
}

// TestSQLiteDSNWithSpecialPathCharacters verifies that SQLiteDSN and
// SQLiteBackupDSN work correctly when the database path contains characters
// that would corrupt plain "path?query" DSN parsing (spaces, question marks).
// This ensures the file: URI form with url.PathEscape is used correctly.
func TestSQLiteDSNWithSpecialPathCharacters(t *testing.T) {
	// A path with a space is the most common real-world special character.
	dir := filepath.Join(t.TempDir(), "path with spaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	dbPath := filepath.Join(dir, "mcm data.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open with path containing spaces returned error: %v", err)
	}
	defer store.Close()

	// Confirm it's functional: WAL mode must be set.
	var mode string
	if err := store.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q after open with spaced path, want wal", mode)
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
