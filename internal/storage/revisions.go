package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrPreviewRevisionNotFound is returned when a preview revision does not exist.
var ErrPreviewRevisionNotFound = errors.New("preview revision not found")

// ErrPreviewRevisionConsumed is returned when a preview revision was already applied.
var ErrPreviewRevisionConsumed = errors.New("preview revision already consumed")

// PreviewRevision stores the immutable output of a deploy preview.
// Rendered content is intentionally kept private: it is used by Apply but
// never serialized in an API response.
type PreviewRevision struct {
	ID                 string
	Actor              string
	BaseACLHash        string
	BasePasswdHash     string
	RenderedACLHash    string
	RenderedPasswdHash string
	ACLRendered        string
	PasswdRendered     string
	CreatedAt          time.Time
	AppliedAt          time.Time
}

// InsertPreviewRevision persists a newly generated preview revision.
func (s *Store) InsertPreviewRevision(ctx context.Context, revision *PreviewRevision) error {
	if revision.ID == "" {
		return errors.New("preview revision id is required")
	}
	if revision.CreatedAt.IsZero() {
		revision.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO preview_revisions(
	id, actor, base_acl_hash, base_passwd_hash, rendered_acl_hash,
	rendered_passwd_hash, acl_rendered, passwd_rendered, created_at, applied_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		revision.ID,
		revision.Actor,
		revision.BaseACLHash,
		revision.BasePasswdHash,
		revision.RenderedACLHash,
		revision.RenderedPasswdHash,
		revision.ACLRendered,
		revision.PasswdRendered,
		revision.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert preview revision: %w", err)
	}
	return nil
}

// GetPreviewRevision retrieves an immutable preview revision by ID.
func (s *Store) GetPreviewRevision(ctx context.Context, id string) (PreviewRevision, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, actor, base_acl_hash, base_passwd_hash, rendered_acl_hash,
       rendered_passwd_hash, acl_rendered, passwd_rendered, created_at, applied_at
FROM preview_revisions WHERE id = ?`, id)
	return scanPreviewRevision(row)
}

// ConsumePreviewRevision marks a revision as applied exactly once.
func (s *Store) ConsumePreviewRevision(ctx context.Context, id string, appliedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE preview_revisions SET applied_at = ?
WHERE id = ? AND applied_at = ''`, appliedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("consume preview revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get consumed preview revision row count: %w", err)
	}
	if affected == 1 {
		return nil
	}

	revision, getErr := s.GetPreviewRevision(ctx, id)
	if errors.Is(getErr, ErrPreviewRevisionNotFound) {
		return ErrPreviewRevisionNotFound
	}
	if getErr != nil {
		return getErr
	}
	if !revision.AppliedAt.IsZero() {
		return ErrPreviewRevisionConsumed
	}
	return ErrPreviewRevisionNotFound
}

func scanPreviewRevision(row interface{ Scan(dest ...any) error }) (PreviewRevision, error) {
	var revision PreviewRevision
	var createdAt, appliedAt string
	if err := row.Scan(
		&revision.ID,
		&revision.Actor,
		&revision.BaseACLHash,
		&revision.BasePasswdHash,
		&revision.RenderedACLHash,
		&revision.RenderedPasswdHash,
		&revision.ACLRendered,
		&revision.PasswdRendered,
		&createdAt,
		&appliedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PreviewRevision{}, ErrPreviewRevisionNotFound
		}
		return PreviewRevision{}, fmt.Errorf("scan preview revision: %w", err)
	}

	var err error
	revision.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return PreviewRevision{}, fmt.Errorf("parse preview revision created_at: %w", err)
	}
	if appliedAt != "" {
		revision.AppliedAt, err = time.Parse(time.RFC3339Nano, appliedAt)
		if err != nil {
			return PreviewRevision{}, fmt.Errorf("parse preview revision applied_at: %w", err)
		}
	}
	return revision, nil
}
