package storage

import (
	"context"
	"testing"
	"time"
)

func TestUpsertEdgeSite(t *testing.T) {
	t.Parallel()

	t.Run("insert new site and retrieve", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		now := time.Now().UTC().Truncate(time.Second)
		site := &EdgeSite{
			ID:         "factory-floor-gw-01",
			Name:       "Factory Floor Gateway",
			Version:    "0.1.0",
			Status:     "healthy",
			Message:    "all systems nominal",
			LastSeenAt: now,
		}
		if err := store.UpsertEdgeSite(ctx, site); err != nil {
			t.Fatalf("UpsertEdgeSite returned error: %v", err)
		}
		if site.ID != "factory-floor-gw-01" {
			t.Errorf("ID = %q, want factory-floor-gw-01", site.ID)
		}
		if site.CreatedAt.IsZero() {
			t.Error("CreatedAt must be set after upsert")
		}
		if site.UpdatedAt.IsZero() {
			t.Error("UpdatedAt must be set after upsert")
		}

		got, err := store.GetEdgeSite(ctx, "factory-floor-gw-01")
		if err != nil {
			t.Fatalf("GetEdgeSite returned error: %v", err)
		}
		if got.ID != "factory-floor-gw-01" {
			t.Errorf("ID = %q, want factory-floor-gw-01", got.ID)
		}
		if got.Name != "Factory Floor Gateway" {
			t.Errorf("Name = %q, want Factory Floor Gateway", got.Name)
		}
		if got.Version != "0.1.0" {
			t.Errorf("Version = %q, want 0.1.0", got.Version)
		}
		if got.Status != "healthy" {
			t.Errorf("Status = %q, want healthy", got.Status)
		}
		if got.Message != "all systems nominal" {
			t.Errorf("Message = %q, want all systems nominal", got.Message)
		}
	})

	t.Run("upsert existing site updates last_seen_at and status", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		first := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
		site := &EdgeSite{
			ID:         "gw-update",
			Name:       "Gateway",
			Version:    "0.1.0",
			Status:     "healthy",
			LastSeenAt: first,
		}
		if err := store.UpsertEdgeSite(ctx, site); err != nil {
			t.Fatalf("first UpsertEdgeSite returned error: %v", err)
		}
		originalCreatedAt := site.CreatedAt

		second := time.Date(2026, 5, 1, 11, 0, 0, 0, time.UTC)
		site2 := &EdgeSite{
			ID:         "gw-update",
			Name:       "Gateway",
			Version:    "0.1.1",
			Status:     "degraded",
			Message:    "disk full",
			LastSeenAt: second,
		}
		if err := store.UpsertEdgeSite(ctx, site2); err != nil {
			t.Fatalf("second UpsertEdgeSite returned error: %v", err)
		}

		got, err := store.GetEdgeSite(ctx, "gw-update")
		if err != nil {
			t.Fatalf("GetEdgeSite returned error: %v", err)
		}
		if got.Status != "degraded" {
			t.Errorf("Status = %q, want degraded", got.Status)
		}
		if got.Version != "0.1.1" {
			t.Errorf("Version = %q, want 0.1.1", got.Version)
		}
		if got.Message != "disk full" {
			t.Errorf("Message = %q, want disk full", got.Message)
		}
		if !got.LastSeenAt.Equal(second) {
			t.Errorf("LastSeenAt = %v, want %v", got.LastSeenAt, second)
		}
		// created_at must be preserved from the first insert
		if !got.CreatedAt.Equal(originalCreatedAt) {
			t.Errorf("CreatedAt changed: got %v, want %v", got.CreatedAt, originalCreatedAt)
		}
	})

	t.Run("list returns sites ordered by last_seen_at desc", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		older := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
		newer := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)

		siteA := &EdgeSite{ID: "gw-a", Name: "A", Version: "1.0.0", Status: "healthy", LastSeenAt: older}
		siteB := &EdgeSite{ID: "gw-b", Name: "B", Version: "1.0.0", Status: "healthy", LastSeenAt: newer}

		if err := store.UpsertEdgeSite(ctx, siteA); err != nil {
			t.Fatalf("UpsertEdgeSite A returned error: %v", err)
		}
		if err := store.UpsertEdgeSite(ctx, siteB); err != nil {
			t.Fatalf("UpsertEdgeSite B returned error: %v", err)
		}

		sites, err := store.ListEdgeSites(ctx)
		if err != nil {
			t.Fatalf("ListEdgeSites returned error: %v", err)
		}
		if len(sites) != 2 {
			t.Fatalf("list length = %d, want 2", len(sites))
		}
		// Newest last_seen_at first
		if sites[0].ID != "gw-b" {
			t.Errorf("sites[0].ID = %q, want gw-b (newest first)", sites[0].ID)
		}
		if sites[1].ID != "gw-a" {
			t.Errorf("sites[1].ID = %q, want gw-a", sites[1].ID)
		}
	})

	t.Run("list returns empty slice not nil when no sites", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		sites, err := store.ListEdgeSites(ctx)
		if err != nil {
			t.Fatalf("ListEdgeSites returned error: %v", err)
		}
		if sites == nil {
			t.Error("ListEdgeSites must return non-nil empty slice, got nil")
		}
		if len(sites) != 0 {
			t.Errorf("list length = %d, want 0", len(sites))
		}
	})

	t.Run("invalid status rejected", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		site := &EdgeSite{
			ID:         "gw-bad-status",
			Name:       "Bad",
			Version:    "1.0.0",
			Status:     "online", // not a valid status
			LastSeenAt: time.Now().UTC(),
		}
		if err := store.UpsertEdgeSite(ctx, site); err == nil {
			t.Error("UpsertEdgeSite must reject invalid status")
		}
	})

	t.Run("empty id rejected", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		site := &EdgeSite{
			ID:         "",
			Name:       "Missing ID",
			Version:    "1.0.0",
			Status:     "healthy",
			LastSeenAt: time.Now().UTC(),
		}
		if err := store.UpsertEdgeSite(ctx, site); err == nil {
			t.Error("UpsertEdgeSite must reject empty id")
		}
	})

	t.Run("get not found returns ErrEdgeSiteNotFound", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		_, err := store.GetEdgeSite(ctx, "does-not-exist")
		if err != ErrEdgeSiteNotFound {
			t.Errorf("GetEdgeSite error = %v, want ErrEdgeSiteNotFound", err)
		}
	})
}
