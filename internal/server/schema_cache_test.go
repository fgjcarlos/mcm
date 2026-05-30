package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// TestJSONSchemaCacheLoadsOnceAndInvalidates proves the cache loads from the
// store once and does not observe direct store changes until invalidated.
func TestJSONSchemaCacheLoadsOnceAndInvalidates(t *testing.T) {
	_, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	mustCreateSchema(t, store, "first", "factory/+/temp")

	cache := &jsonSchemaCache{}

	got, err := cache.get(ctx, store)
	if err != nil {
		t.Fatalf("first get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("first get len = %d, want 1", len(got))
	}

	// A schema added directly to the store must NOT appear until invalidation.
	mustCreateSchema(t, store, "second", "factory/+/humidity")
	got, err = cache.get(ctx, store)
	if err != nil {
		t.Fatalf("cached get: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("cached get len = %d, want 1 (cache must not see the new schema yet)", len(got))
	}

	cache.invalidate()
	got, err = cache.get(ctx, store)
	if err != nil {
		t.Fatalf("get after invalidate: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("get after invalidate len = %d, want 2", len(got))
	}
}

// TestJSONSchemaCacheInvalidatedByHTTPHandlers proves that creating, updating,
// and deleting schemas through the HTTP API invalidates the cache so the MQTT
// hot path (TopicEvent) reflects the change.
func TestJSONSchemaCacheInvalidatedByHTTPHandlers(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "op", "password-1234", auth.RoleOperator)
	token := loginAs(t, app, "op", "password-1234")
	handler := app.Handler()

	const topic = "factory/line1/temp"
	payload := []byte(`{"temperature":21.5}`)

	// Warm the cache while empty: no schema yet, so no validation.
	if ev := app.TopicEvent(topic, payload, 256); ev.SchemaValidation != nil {
		t.Fatalf("pre-create SchemaValidation = %+v, want nil", ev.SchemaValidation)
	}

	// Create a matching schema via the HTTP API — must invalidate the cache.
	createBody := `{"name":"temp","topic_filter":"factory/+/temp","schema":{"type":"object"},"enabled":true}`
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/json-schemas", createBody, token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	var created storage.JSONSchemaDefinition
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}

	if ev := app.TopicEvent(topic, payload, 256); ev.SchemaValidation == nil {
		t.Fatal("post-create SchemaValidation = nil, want a result (cache not invalidated on create)")
	}

	// Update the schema to no longer match the topic — must invalidate.
	updateBody := `{"name":"temp","topic_filter":"other/+/temp","schema":{"type":"object"},"enabled":true}`
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodPut, "/api/v1/json-schemas/"+strconv.FormatInt(created.ID, 10), updateBody, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if ev := app.TopicEvent(topic, payload, 256); ev.SchemaValidation != nil {
		t.Fatalf("post-update SchemaValidation = %+v, want nil (cache not invalidated on update)", ev.SchemaValidation)
	}

	// Point it back at the topic, then delete — delete must invalidate too.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodPut, "/api/v1/json-schemas/"+strconv.FormatInt(created.ID, 10), createBody, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-point status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if ev := app.TopicEvent(topic, payload, 256); ev.SchemaValidation == nil {
		t.Fatal("post-re-point SchemaValidation = nil, want a result")
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/json-schemas/"+strconv.FormatInt(created.ID, 10), "", token))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204, body = %s", rec.Code, rec.Body.String())
	}
	if ev := app.TopicEvent(topic, payload, 256); ev.SchemaValidation != nil {
		t.Fatalf("post-delete SchemaValidation = %+v, want nil (cache not invalidated on delete)", ev.SchemaValidation)
	}
}

func mustCreateSchema(t *testing.T, store *storage.Store, name, topicFilter string) {
	t.Helper()
	if _, err := store.CreateJSONSchema(context.Background(), storage.CreateJSONSchemaParams{
		Name:        name,
		TopicFilter: topicFilter,
		Schema:      json.RawMessage(`{"type":"object"}`),
		Enabled:     true,
	}); err != nil {
		t.Fatalf("CreateJSONSchema(%q): %v", name, err)
	}
}
