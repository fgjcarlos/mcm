package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestBrokerConfigRevisionRoundTrip(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	created := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	rev := &BrokerConfigRevision{
		ID:               "rev-1",
		Actor:            "admin",
		BaseConfHash:     "basehash",
		RenderedConfHash: "renderedhash",
		BasePath:         "/mosquitto/config/mosquitto.conf",
		ConfRendered:     "listener 1883 0.0.0.0\nallow_anonymous false\n",
		IncludeSnapshot:  `{"files":[]}`,
		CreatedAt:        created,
	}
	if err := store.InsertBrokerConfigRevision(context.Background(), rev); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := store.GetBrokerConfigRevision(context.Background(), "rev-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Actor != "admin" || got.BaseConfHash != "basehash" || got.ConfRendered != rev.ConfRendered {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(created) {
		t.Fatalf("created_at = %s, want %s", got.CreatedAt, created)
	}

	appliedAt := created.Add(time.Minute)
	if err := store.ConsumeBrokerConfigRevision(context.Background(), "rev-1", appliedAt); err != nil {
		t.Fatalf("consume: %v", err)
	}

	// Second consume must fail.
	if err := store.ConsumeBrokerConfigRevision(context.Background(), "rev-1", appliedAt); err == nil {
		t.Fatal("second consume should have returned an error")
	} else if !errors.Is(err, ErrBrokerConfigRevisionConsumed) {
		t.Fatalf("second consume err = %v, want ErrBrokerConfigRevisionConsumed", err)
	}
}

func TestBrokerConfigAdoptionRoundTrip(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	src := filepath.Join(t.TempDir(), "mosquitto.conf")
	a, err := store.InsertBrokerConfigAdoption(context.Background(), src, "admin")
	if err != nil {
		t.Fatalf("insert adoption: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("adoption id must be set")
	}

	got, err := store.LatestBrokerConfigAdoption(context.Background(), src)
	if err != nil {
		t.Fatalf("latest adoption: %v", err)
	}
	if got.AdoptedBy != "admin" || got.SourcePath != src {
		t.Fatalf("adoption mismatch: %+v", got)
	}
}

func TestLatestBrokerConfigAdoptionMissing(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	got, err := store.LatestBrokerConfigAdoption(context.Background(), "/nonexistent/path")
	if err != nil {
		t.Fatalf("missing adoption: %v", err)
	}
	if got.ID != 0 {
		t.Fatalf("missing adoption must return zero value, got %+v", got)
	}
}
