package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrBrokerConfigRevisionNotFound is returned when a broker config revision
// does not exist. Issue #298.
var ErrBrokerConfigRevisionNotFound = errors.New("broker config revision not found")

// ErrBrokerConfigRevisionConsumed is returned when a broker config revision
// was already applied.
var ErrBrokerConfigRevisionConsumed = errors.New("broker config revision already consumed")

// BrokerConfigRevision stores the immutable output of a broker-config preview.
// It mirrors preview_revisions (issue #296) but for the broker conf + include
// snapshot produced by the importer (issue #298).
type BrokerConfigRevision struct {
	ID               string
	Actor            string
	BaseConfHash     string
	RenderedConfHash string
	BasePath         string
	ConfRendered     string
	IncludeSnapshot  string
	CreatedAt        time.Time
	AppliedAt        time.Time
}

// BrokerConfigAdoption records the operator's acknowledgement that an
// imported mosquitto.conf (and its include_dir tree) is the new source of
// truth before any apply is permitted. Apply refuses unless the imported
// source matches a recent adoption (or adopt is invoked in the same
// request body).
type BrokerConfigAdoption struct {
	ID         int64
	SourcePath string
	AdoptedBy  string
	AdoptedAt  time.Time
}

// InsertBrokerConfigRevision persists a newly generated broker config revision.
func (s *Store) InsertBrokerConfigRevision(ctx context.Context, r *BrokerConfigRevision) error {
	if r.ID == "" {
		return errors.New("broker config revision id is required")
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO broker_config_revisions(
	id, actor, base_conf_hash, rendered_conf_hash,
	base_path, conf_rendered, include_snapshot, created_at, applied_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, '')`,
		r.ID, r.Actor, r.BaseConfHash, r.RenderedConfHash,
		r.BasePath, r.ConfRendered, r.IncludeSnapshot,
		r.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert broker config revision: %w", err)
	}
	return nil
}

// GetBrokerConfigRevision retrieves a broker config revision by ID.
func (s *Store) GetBrokerConfigRevision(ctx context.Context, id string) (BrokerConfigRevision, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, actor, base_conf_hash, rendered_conf_hash,
       base_path, conf_rendered, include_snapshot, created_at, applied_at
FROM broker_config_revisions WHERE id = ?`, id)
	return scanBrokerConfigRevision(row)
}

// ConsumeBrokerConfigRevision marks a revision as applied exactly once.
func (s *Store) ConsumeBrokerConfigRevision(ctx context.Context, id string, appliedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE broker_config_revisions SET applied_at = ?
WHERE id = ? AND applied_at = ''`, appliedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("consume broker config revision: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get consumed broker config revision row count: %w", err)
	}
	if affected == 1 {
		return nil
	}
	r, getErr := s.GetBrokerConfigRevision(ctx, id)
	if errors.Is(getErr, ErrBrokerConfigRevisionNotFound) {
		return ErrBrokerConfigRevisionNotFound
	}
	if getErr != nil {
		return getErr
	}
	if !r.AppliedAt.IsZero() {
		return ErrBrokerConfigRevisionConsumed
	}
	return ErrBrokerConfigRevisionNotFound
}

// InsertBrokerConfigAdoption records an explicit operator acknowledgement
// that an imported conf + include tree may now be applied.
func (s *Store) InsertBrokerConfigAdoption(ctx context.Context, sourcePath, adoptedBy string) (BrokerConfigAdoption, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO broker_config_adoptions(source_path, adopted_by, adopted_at) VALUES(?, ?, ?)`,
		sourcePath, adoptedBy, now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return BrokerConfigAdoption{}, fmt.Errorf("insert broker config adoption: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return BrokerConfigAdoption{}, fmt.Errorf("get broker config adoption id: %w", err)
	}
	return BrokerConfigAdoption{
		ID:         id,
		SourcePath: sourcePath,
		AdoptedBy:  adoptedBy,
		AdoptedAt:  now,
	}, nil
}

// LatestBrokerConfigAdoption returns the most recent adoption record for the
// given source path, or (nil, nil) when none exists.
func (s *Store) LatestBrokerConfigAdoption(ctx context.Context, sourcePath string) (BrokerConfigAdoption, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, source_path, adopted_by, adopted_at
FROM broker_config_adoptions
WHERE source_path = ?
ORDER BY adopted_at DESC, id DESC
LIMIT 1`, sourcePath)
	var a BrokerConfigAdoption
	var adoptedAt string
	if err := row.Scan(&a.ID, &a.SourcePath, &a.AdoptedBy, &adoptedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BrokerConfigAdoption{}, nil
		}
		return BrokerConfigAdoption{}, fmt.Errorf("query broker config adoption: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, adoptedAt)
	if err != nil {
		return BrokerConfigAdoption{}, fmt.Errorf("parse broker config adoption timestamp: %w", err)
	}
	a.AdoptedAt = parsed
	return a, nil
}

func scanBrokerConfigRevision(row interface{ Scan(dest ...any) error }) (BrokerConfigRevision, error) {
	var r BrokerConfigRevision
	var createdAt, appliedAt string
	if err := row.Scan(
		&r.ID,
		&r.Actor,
		&r.BaseConfHash,
		&r.RenderedConfHash,
		&r.BasePath,
		&r.ConfRendered,
		&r.IncludeSnapshot,
		&createdAt,
		&appliedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BrokerConfigRevision{}, ErrBrokerConfigRevisionNotFound
		}
		return BrokerConfigRevision{}, fmt.Errorf("scan broker config revision: %w", err)
	}
	var err error
	r.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return BrokerConfigRevision{}, fmt.Errorf("parse broker config revision created_at: %w", err)
	}
	if appliedAt != "" {
		r.AppliedAt, err = time.Parse(time.RFC3339Nano, appliedAt)
		if err != nil {
			return BrokerConfigRevision{}, fmt.Errorf("parse broker config revision applied_at: %w", err)
		}
	}
	return r, nil
}