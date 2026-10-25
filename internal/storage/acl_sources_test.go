package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

// TestACLSourceMigrationCreatesTable pins that migration 22 creates the
// acl_sources table and is recorded in schema_migrations.
func TestACLSourceMigrationCreatesTable(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	var name string
	if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'acl_sources'`).Scan(&name); err != nil {
		t.Fatalf("acl_sources table was not created: %v", err)
	}
	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 22`).Scan(&version); err != nil {
		t.Fatalf("migration 22 was not recorded: %v", err)
	}
}

// TestSaveACLSourceRoundTrip pins that the adopted body is stored byte-exact
// and that the content hash is the SHA-256 of that body.
func TestSaveACLSourceRoundTrip(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	body := "# managed by ops\nuser alice\ntopic read sensors/#\n"
	saved, err := store.SaveACLSource(context.Background(), "/etc/mosquitto/acl", body)
	if err != nil {
		t.Fatalf("save acl source: %v", err)
	}
	if saved.Path != "/etc/mosquitto/acl" {
		t.Errorf("path = %q, want /etc/mosquitto/acl", saved.Path)
	}

	got, err := store.GetACLSource(context.Background(), "/etc/mosquitto/acl")
	if err != nil {
		t.Fatalf("get acl source: %v", err)
	}
	if got.Body != body {
		t.Errorf("body = %q, want %q", got.Body, body)
	}
	sum := sha256.Sum256([]byte(body))
	if got.ContentHash != hex.EncodeToString(sum[:]) {
		t.Errorf("content hash = %q, want %q", got.ContentHash, hex.EncodeToString(sum[:]))
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps must be set: %+v", got)
	}
}

// TestSaveACLSourceUpdatesSamePath pins that saving the same path twice
// updates the row instead of duplicating it.
func TestSaveACLSourceUpdatesSamePath(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if _, err := store.SaveACLSource(context.Background(), "/etc/mosquitto/acl", "first"); err != nil {
		t.Fatalf("save acl source: %v", err)
	}
	if _, err := store.SaveACLSource(context.Background(), "/etc/mosquitto/acl", "second"); err != nil {
		t.Fatalf("save acl source again: %v", err)
	}

	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM acl_sources`).Scan(&count); err != nil {
		t.Fatalf("count acl_sources: %v", err)
	}
	if count != 1 {
		t.Fatalf("acl_sources rows = %d, want 1", count)
	}

	got, err := store.GetACLSource(context.Background(), "/etc/mosquitto/acl")
	if err != nil {
		t.Fatalf("get acl source: %v", err)
	}
	if got.Body != "second" {
		t.Errorf("body = %q, want second", got.Body)
	}
}

// TestACLSourceNotFound pins the sentinel returned for an unknown path.
func TestACLSourceNotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if _, err := store.GetACLSource(context.Background(), "/etc/mosquitto/missing"); !errors.Is(err, ErrACLSourceNotFound) {
		t.Errorf("get err = %v, want ErrACLSourceNotFound", err)
	}
	if err := store.DeleteACLSource(context.Background(), "/etc/mosquitto/missing"); !errors.Is(err, ErrACLSourceNotFound) {
		t.Errorf("delete err = %v, want ErrACLSourceNotFound", err)
	}
}

// TestDeleteACLSource pins that a stored source is removed.
func TestDeleteACLSource(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	if _, err := store.SaveACLSource(context.Background(), "/etc/mosquitto/acl", "body"); err != nil {
		t.Fatalf("save acl source: %v", err)
	}
	if err := store.DeleteACLSource(context.Background(), "/etc/mosquitto/acl"); err != nil {
		t.Fatalf("delete acl source: %v", err)
	}
	if _, err := store.GetACLSource(context.Background(), "/etc/mosquitto/acl"); !errors.Is(err, ErrACLSourceNotFound) {
		t.Errorf("get err = %v, want ErrACLSourceNotFound", err)
	}
}
