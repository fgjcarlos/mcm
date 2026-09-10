package storage

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPreviewRevisionMigration(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	var table string
	if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'preview_revisions'`).Scan(&table); err != nil {
		t.Fatalf("preview_revisions table was not created: %v", err)
	}
	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_migrations WHERE version = 15`).Scan(&version); err != nil {
		t.Fatalf("preview revisions migration was not recorded: %v", err)
	}
}

func TestPreviewRevisionLifecycle(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()
	createdAt := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	revision := &PreviewRevision{
		ID:                 "revision-1",
		Actor:              "operator",
		BaseACLHash:        "base-acl",
		BasePasswdHash:     "base-passwd",
		RenderedACLHash:    "rendered-acl",
		RenderedPasswdHash: "rendered-passwd",
		ACLRendered:        "rendered acl",
		PasswdRendered:     "rendered passwd",
		CreatedAt:          createdAt,
	}
	if err := store.InsertPreviewRevision(ctx, revision); err != nil {
		t.Fatalf("InsertPreviewRevision returned error: %v", err)
	}

	got, err := store.GetPreviewRevision(ctx, revision.ID)
	if err != nil {
		t.Fatalf("GetPreviewRevision returned error: %v", err)
	}
	if got.ID != revision.ID || got.Actor != revision.Actor || !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("retrieved revision = %#v, want %#v", got, revision)
	}
	if got.ACLRendered != revision.ACLRendered || got.PasswdRendered != revision.PasswdRendered {
		t.Fatalf("rendered content was not persisted: %#v", got)
	}
	if !got.AppliedAt.IsZero() {
		t.Fatalf("new revision AppliedAt = %v, want zero", got.AppliedAt)
	}

	appliedAt := createdAt.Add(time.Minute)
	if err := store.ConsumePreviewRevision(ctx, revision.ID, appliedAt); err != nil {
		t.Fatalf("ConsumePreviewRevision returned error: %v", err)
	}
	got, err = store.GetPreviewRevision(ctx, revision.ID)
	if err != nil {
		t.Fatalf("GetPreviewRevision after consume returned error: %v", err)
	}
	if !got.AppliedAt.Equal(appliedAt) {
		t.Fatalf("AppliedAt = %v, want %v", got.AppliedAt, appliedAt)
	}
	if err := store.ConsumePreviewRevision(ctx, revision.ID, appliedAt.Add(time.Minute)); !errors.Is(err, ErrPreviewRevisionConsumed) {
		t.Fatalf("second ConsumePreviewRevision error = %v, want ErrPreviewRevisionConsumed", err)
	}
}

func TestGetPreviewRevisionNotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	_, err := store.GetPreviewRevision(context.Background(), "missing")
	if !errors.Is(err, ErrPreviewRevisionNotFound) {
		t.Fatalf("GetPreviewRevision error = %v, want ErrPreviewRevisionNotFound", err)
	}
}
