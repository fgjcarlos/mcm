package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestInsertDeployment(t *testing.T) {
	t.Parallel()

	t.Run("insert and retrieve matches all fields", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		d := &Deployment{
			Actor:          "operator",
			Status:         "pending",
			ACLSnapshot:    "old acl content",
			PasswdSnapshot: "old passwd content",
			ACLRendered:    "new acl content",
			PasswdRendered: "new passwd content",
			Message:        "",
		}
		if err := store.InsertDeployment(ctx, d); err != nil {
			t.Fatalf("InsertDeployment returned error: %v", err)
		}
		if d.ID == 0 {
			t.Error("want ID > 0, got 0")
		}
		if d.CreatedAt.IsZero() {
			t.Error("want non-zero created_at")
		}
		if d.UpdatedAt.IsZero() {
			t.Error("want non-zero updated_at")
		}

		got, err := store.GetDeployment(ctx, d.ID)
		if err != nil {
			t.Fatalf("GetDeployment returned error: %v", err)
		}
		if got.Actor != "operator" {
			t.Errorf("Actor = %q, want %q", got.Actor, "operator")
		}
		if got.Status != "pending" {
			t.Errorf("Status = %q, want %q", got.Status, "pending")
		}
		if got.ACLSnapshot != "old acl content" {
			t.Errorf("ACLSnapshot = %q, want %q", got.ACLSnapshot, "old acl content")
		}
		if got.PasswdSnapshot != "old passwd content" {
			t.Errorf("PasswdSnapshot = %q, want %q", got.PasswdSnapshot, "old passwd content")
		}
		if got.ACLRendered != "new acl content" {
			t.Errorf("ACLRendered = %q, want %q", got.ACLRendered, "new acl content")
		}
		if got.PasswdRendered != "new passwd content" {
			t.Errorf("PasswdRendered = %q, want %q", got.PasswdRendered, "new passwd content")
		}
	})

	t.Run("invalid status returns error", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		d := &Deployment{
			Actor:  "operator",
			Status: "unknown",
		}
		err := store.InsertDeployment(ctx, d)
		if err == nil {
			t.Fatal("InsertDeployment with invalid status: want error, got nil")
		}
	})
}

func TestGetDeployment(t *testing.T) {
	t.Parallel()

	t.Run("not found returns ErrDeploymentNotFound", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		_, err := store.GetDeployment(context.Background(), 99999)
		if !errors.Is(err, ErrDeploymentNotFound) {
			t.Errorf("error = %v, want ErrDeploymentNotFound", err)
		}
	})
}

func TestUpdateDeploymentStatus(t *testing.T) {
	t.Parallel()

	t.Run("status transition updates status and updated_at", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		d := &Deployment{Actor: "operator", Status: "pending"}
		if err := store.InsertDeployment(ctx, d); err != nil {
			t.Fatalf("InsertDeployment returned error: %v", err)
		}

		// Small sleep to ensure updated_at > created_at.
		time.Sleep(time.Millisecond)

		if err := store.UpdateDeploymentStatus(ctx, d.ID, "applied", ""); err != nil {
			t.Fatalf("UpdateDeploymentStatus returned error: %v", err)
		}

		got, err := store.GetDeployment(ctx, d.ID)
		if err != nil {
			t.Fatalf("GetDeployment returned error: %v", err)
		}
		if got.Status != "applied" {
			t.Errorf("Status = %q, want %q", got.Status, "applied")
		}
		if !got.UpdatedAt.After(got.CreatedAt) {
			t.Errorf("UpdatedAt %v should be after CreatedAt %v", got.UpdatedAt, got.CreatedAt)
		}
	})

	t.Run("invalid status returns error without modifying record", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		d := &Deployment{Actor: "operator", Status: "pending"}
		if err := store.InsertDeployment(ctx, d); err != nil {
			t.Fatalf("InsertDeployment returned error: %v", err)
		}

		err := store.UpdateDeploymentStatus(ctx, d.ID, "unknown", "bad status")
		if err == nil {
			t.Fatal("UpdateDeploymentStatus with invalid status: want error, got nil")
		}

		// Record should remain unchanged.
		got, err := store.GetDeployment(ctx, d.ID)
		if err != nil {
			t.Fatalf("GetDeployment returned error: %v", err)
		}
		if got.Status != "pending" {
			t.Errorf("Status = %q, want unchanged %q", got.Status, "pending")
		}
	})

	t.Run("not found returns ErrDeploymentNotFound", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		err := store.UpdateDeploymentStatus(context.Background(), 99999, "applied", "")
		if !errors.Is(err, ErrDeploymentNotFound) {
			t.Errorf("error = %v, want ErrDeploymentNotFound", err)
		}
	})
}

func TestListDeployments(t *testing.T) {
	t.Parallel()

	t.Run("empty store returns empty slice not nil", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()

		deployments, err := store.ListDeployments(context.Background(), 20, 0)
		if err != nil {
			t.Fatalf("ListDeployments returned error: %v", err)
		}
		if deployments == nil {
			t.Error("want empty slice, got nil")
		}
		if len(deployments) != 0 {
			t.Errorf("want 0 deployments, got %d", len(deployments))
		}
	})

	t.Run("pagination returns correct window", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		// Insert 5 deployments.
		for i := 0; i < 5; i++ {
			d := &Deployment{Actor: "operator", Status: "applied"}
			if err := store.InsertDeployment(ctx, d); err != nil {
				t.Fatalf("InsertDeployment %d returned error: %v", i, err)
			}
		}

		// First page: limit 3, offset 0 → 3 records.
		page1, err := store.ListDeployments(ctx, 3, 0)
		if err != nil {
			t.Fatalf("ListDeployments page1 returned error: %v", err)
		}
		if len(page1) != 3 {
			t.Errorf("page1: got %d records, want 3", len(page1))
		}

		// Second page: limit 3, offset 3 → 2 records.
		page2, err := store.ListDeployments(ctx, 3, 3)
		if err != nil {
			t.Fatalf("ListDeployments page2 returned error: %v", err)
		}
		if len(page2) != 2 {
			t.Errorf("page2: got %d records, want 2", len(page2))
		}

		// Verify newest-first ordering: page1[0].ID > page2[last].ID.
		if len(page1) > 0 && len(page2) > 0 {
			if page1[0].ID <= page2[len(page2)-1].ID {
				t.Errorf("expected newest-first order: page1[0].ID=%d should be > page2[last].ID=%d",
					page1[0].ID, page2[len(page2)-1].ID)
			}
		}
	})

	t.Run("limit capped at 100", func(t *testing.T) {
		t.Parallel()
		store := newTestStore(t)
		defer store.Close()
		ctx := context.Background()

		// Insert 3 records.
		for i := 0; i < 3; i++ {
			d := &Deployment{Actor: "operator", Status: "applied"}
			if err := store.InsertDeployment(ctx, d); err != nil {
				t.Fatalf("InsertDeployment %d returned error: %v", i, err)
			}
		}

		// Limit > 100 should be treated as default (20).
		deployments, err := store.ListDeployments(ctx, 500, 0)
		if err != nil {
			t.Fatalf("ListDeployments returned error: %v", err)
		}
		// Should return all 3 since 3 < 20.
		if len(deployments) != 3 {
			t.Errorf("got %d deployments, want 3", len(deployments))
		}
	})
}
