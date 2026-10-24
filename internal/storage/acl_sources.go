package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrACLSourceNotFound is returned when an ACL source does not exist for the
// requested path.
var ErrACLSourceNotFound = errors.New("acl source not found")

// ACLSourceRow is an adopted ACL source: the verbatim body of an ACL file
// MCM manages on disk. One row exists per configured ACL path.
//
// The body is stored byte-exact so the deploy service can render it back
// without losing the content MCM does not model (comments, blank lines and
// directives it does not understand).
type ACLSourceRow struct {
	ID          string    `json:"id"`
	Path        string    `json:"path"`
	Body        string    `json:"body"`
	ContentHash string    `json:"content_hash"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// SaveACLSource stores or updates the adopted ACL source of a path.
//
// The body is stored byte-exact and the content hash is the lowercase hex
// SHA-256 of that body. Saving the same path twice updates the existing row
// instead of adding a second one: a path has exactly one adopted source.
func (s *Store) SaveACLSource(ctx context.Context, path string, body string) (ACLSourceRow, error) {
	if path == "" {
		return ACLSourceRow{}, errors.New("acl source path is required")
	}

	now := time.Now().UTC()
	timestamp := now.Format(time.RFC3339Nano)
	hash := aclSourceContentHash(body)

	if _, err := s.db.ExecContext(ctx, `
INSERT INTO acl_sources(path, body, content_hash, created_at, updated_at)
VALUES(?, ?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET
	body = excluded.body,
	content_hash = excluded.content_hash,
	updated_at = excluded.updated_at`,
		path, body, hash, timestamp, timestamp,
	); err != nil {
		return ACLSourceRow{}, fmt.Errorf("save acl source: %w", err)
	}

	return s.GetACLSource(ctx, path)
}

// GetACLSource returns the adopted ACL source of a path. It returns
// ErrACLSourceNotFound when the path has no source.
func (s *Store) GetACLSource(ctx context.Context, path string) (ACLSourceRow, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, path, body, content_hash, created_at, updated_at
FROM acl_sources WHERE path = ?`, path)
	return scanACLSourceRow(row)
}

// DeleteACLSource removes the adopted ACL source of a path. It returns
// ErrACLSourceNotFound when the path has no source.
func (s *Store) DeleteACLSource(ctx context.Context, path string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM acl_sources WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("delete acl source: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted acl source row count: %w", err)
	}
	if affected == 0 {
		return ErrACLSourceNotFound
	}
	return nil
}

// aclSourceContentHash returns the lowercase hex SHA-256 of an ACL body.
// The hash is computed locally: the deploy package must not be imported
// here, it would create an import cycle.
func aclSourceContentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// scanACLSourceRow reads one acl_sources row.
func scanACLSourceRow(row interface{ Scan(dest ...any) error }) (ACLSourceRow, error) {
	var (
		result    ACLSourceRow
		id        int64
		createdAt string
		updatedAt string
	)
	if err := row.Scan(
		&id,
		&result.Path,
		&result.Body,
		&result.ContentHash,
		&createdAt,
		&updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ACLSourceRow{}, ErrACLSourceNotFound
		}
		return ACLSourceRow{}, fmt.Errorf("scan acl source: %w", err)
	}
	result.ID = fmt.Sprintf("%d", id)

	var err error
	result.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ACLSourceRow{}, fmt.Errorf("parse acl source created_at: %w", err)
	}
	result.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ACLSourceRow{}, fmt.Errorf("parse acl source updated_at: %w", err)
	}
	return result, nil
}
