package storage

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestListenerStore_InsertAndGet(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	want := listenerSpecRow("listener-1", 1883, "127.0.0.1")
	want.Protocols = []string{"mqtt", "websockets"}
	want.Options = map[string]string{"allow_anonymous": "false", "mount_point": "sensors"}

	inserted, err := store.InsertListenerSpec(context.Background(), want)
	if err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}
	if inserted.CreatedAt.IsZero() || inserted.UpdatedAt.IsZero() {
		t.Fatalf("InsertListenerSpec timestamps = (%v, %v), want populated", inserted.CreatedAt, inserted.UpdatedAt)
	}

	got, err := store.GetListenerSpec(context.Background(), want.ID)
	if err != nil {
		t.Fatalf("GetListenerSpec returned error: %v", err)
	}
	if !reflect.DeepEqual(got, inserted) {
		t.Fatalf("GetListenerSpec = %#v, want %#v", got, inserted)
	}
}

func TestListenerStore_DuplicateIDRejected(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	if _, err := store.InsertListenerSpec(ctx, listenerSpecRow("listener-1", 1883, "0.0.0.0")); err != nil {
		t.Fatalf("first InsertListenerSpec returned error: %v", err)
	}
	if _, err := store.InsertListenerSpec(ctx, listenerSpecRow("listener-1", 1884, "0.0.0.0")); !errors.Is(err, ErrListenerConflict) {
		t.Fatalf("duplicate InsertListenerSpec error = %v, want ErrListenerConflict", err)
	}
	if _, err := store.InsertListenerSpec(ctx, listenerSpecRow("listener-2", 1883, "0.0.0.0")); !errors.Is(err, ErrListenerConflict) {
		t.Fatalf("duplicate port/bind InsertListenerSpec error = %v, want ErrListenerConflict", err)
	}
}

func TestListenerStore_ListSortedByPortBind(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	for _, row := range []ListenerSpecRow{
		listenerSpecRow("listener-1", 1884, "127.0.0.1"),
		listenerSpecRow("listener-2", 1885, "0.0.0.0"),
		listenerSpecRow("listener-3", 1883, "127.0.0.1"),
	} {
		if _, err := store.InsertListenerSpec(ctx, row); err != nil {
			t.Fatalf("InsertListenerSpec(%q) returned error: %v", row.ID, err)
		}
	}

	got, err := store.ListListenerSpecs(ctx)
	if err != nil {
		t.Fatalf("ListListenerSpecs returned error: %v", err)
	}
	if gotIDs := listenerSpecIDs(got); !reflect.DeepEqual(gotIDs, []string{"listener-2", "listener-3", "listener-1"}) {
		t.Fatalf("ListListenerSpecs IDs = %v, want [listener-2 listener-3 listener-1]", gotIDs)
	}
}

func TestListenerStore_UpdateViaCallback(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	createdAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	row := listenerSpecRow("listener-1", 1883, "0.0.0.0")
	row.CreatedAt = createdAt
	row.UpdatedAt = createdAt
	inserted, err := store.InsertListenerSpec(ctx, row)
	if err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}

	got, err := store.UpdateListenerSpec(ctx, row.ID, func(row *ListenerSpecRow) error {
		row.Port = 9001
		row.Options = map[string]string{"allow_anonymous": "false"}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateListenerSpec returned error: %v", err)
	}
	if got.Port != 9001 || !reflect.DeepEqual(got.Options, map[string]string{"allow_anonymous": "false"}) {
		t.Fatalf("updated row = %#v, want port and options changed", got)
	}
	if !got.CreatedAt.Equal(inserted.CreatedAt) {
		t.Fatalf("updated CreatedAt = %v, want %v", got.CreatedAt, inserted.CreatedAt)
	}
	if _, err := store.UpdateListenerSpec(ctx, "missing", func(*ListenerSpecRow) error { return nil }); !errors.Is(err, ErrListenerNotFound) {
		t.Fatalf("UpdateListenerSpec missing error = %v, want ErrListenerNotFound", err)
	}
}

func TestListenerStore_Delete(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	row := listenerSpecRow("listener-1", 1883, "0.0.0.0")
	if _, err := store.InsertListenerSpec(ctx, row); err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}
	if err := store.DeleteListenerSpec(ctx, row.ID); err != nil {
		t.Fatalf("DeleteListenerSpec returned error: %v", err)
	}
	if err := store.DeleteListenerSpec(ctx, row.ID); !errors.Is(err, ErrListenerNotFound) {
		t.Fatalf("DeleteListenerSpec missing error = %v, want ErrListenerNotFound", err)
	}
}

func TestListenerStore_ReplaceAll(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	original := listenerSpecRow("original", 1883, "0.0.0.0")
	if _, err := store.InsertListenerSpec(ctx, original); err != nil {
		t.Fatalf("InsertListenerSpec returned error: %v", err)
	}
	if err := store.ReplaceAllListenerSpecs(ctx, nil); err != nil {
		t.Fatalf("ReplaceAllListenerSpecs(empty) returned error: %v", err)
	}
	rows, err := store.ListListenerSpecs(ctx)
	if err != nil {
		t.Fatalf("ListListenerSpecs after clear returned error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("ListListenerSpecs after clear = %#v, want empty", rows)
	}

	if err := store.ReplaceAllListenerSpecs(ctx, []ListenerSpecRow{original}); err != nil {
		t.Fatalf("ReplaceAllListenerSpecs(seed) returned error: %v", err)
	}
	duplicates := []ListenerSpecRow{
		listenerSpecRow("duplicate-1", 9001, "127.0.0.1"),
		listenerSpecRow("duplicate-2", 9001, "127.0.0.1"),
	}
	if err := store.ReplaceAllListenerSpecs(ctx, duplicates); !errors.Is(err, ErrListenerConflict) {
		t.Fatalf("ReplaceAllListenerSpecs(duplicates) error = %v, want ErrListenerConflict", err)
	}
	rows, err = store.ListListenerSpecs(ctx)
	if err != nil {
		t.Fatalf("ListListenerSpecs after duplicate ReplaceAll returned error: %v", err)
	}
	if gotIDs := listenerSpecIDs(rows); !reflect.DeepEqual(gotIDs, []string{"original"}) {
		t.Fatalf("rows after duplicate ReplaceAll = %v, want [original]", gotIDs)
	}
}

func TestListenerStore_SchemaMigrationApplied(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	var table string
	if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'listener_specs'`).Scan(&table); err != nil {
		t.Fatalf("listener_specs table was not created: %v", err)
	}
}

func listenerSpecRow(id string, port int, bind string) ListenerSpecRow {
	return ListenerSpecRow{
		ID:        id,
		Port:      port,
		Bind:      bind,
		Protocols: []string{"mqtt"},
		Options:   map[string]string{"allow_anonymous": "true"},
	}
}

func listenerSpecIDs(rows []ListenerSpecRow) []string {
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}
