package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrManagedACLFileNotFound is returned when a managed ACL file path is not
// registered.
var ErrManagedACLFileNotFound = errors.New("managed acl file not found")

// ManagedACLFileRow is one ACL file MCM owns.
//
// Mosquitto resolves `acl_file` per listener once
// `per_listener_settings true` is set, so MCM can write one ACL file per
// listener in addition to the broker-wide one. The registry records the
// per-listener files MCM created so a deploy can delete the file of a
// listener whose scoped rules are gone. Without the registry MCM would not
// know which files it owns and a stale file would keep granting access the
// operator removed.
type ManagedACLFileRow struct {
	// Path is the absolute path of the file.
	Path string `json:"path"`
	// ListenerID names the listener whose file this is.
	ListenerID string `json:"listener_id"`
	// CreatedAt is when MCM first wrote the file.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is when MCM last wrote the file.
	UpdatedAt time.Time `json:"updated_at"`
}

// RecordManagedACLFile registers an ACL file MCM owns, or refreshes the
// registration of a file it already owns.
//
// The registry follows the deploy apply: the deploy service holds the
// mutation lock while it writes the registry, so this method does not take
// the lock itself — taking it here would deadlock the apply.
func (s *Store) RecordManagedACLFile(ctx context.Context, path, listenerID string) error {
	if path == "" {
		return errors.New("managed acl file path is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)

	_, err := s.db.ExecContext(ctx, `
INSERT INTO managed_acl_files(path, listener_id, created_at, updated_at)
VALUES(?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET listener_id = excluded.listener_id, updated_at = excluded.updated_at`,
		path, listenerID, now, now,
	)
	if err != nil {
		return fmt.Errorf("record managed acl file: %w", err)
	}
	return nil
}

// ListManagedACLFiles returns every ACL file MCM owns, ordered by path.
func (s *Store) ListManagedACLFiles(ctx context.Context) ([]ManagedACLFileRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT path, listener_id, created_at, updated_at
FROM managed_acl_files
ORDER BY path ASC`)
	if err != nil {
		return nil, fmt.Errorf("list managed acl files: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []ManagedACLFileRow
	for rows.Next() {
		var (
			path       string
			listenerID string
			createdAt  string
			updatedAt  string
		)
		if err := rows.Scan(&path, &listenerID, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan managed acl file: %w", err)
		}
		row := ManagedACLFileRow{Path: path, ListenerID: listenerID}
		row.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		row.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate managed acl files: %w", err)
	}
	return out, nil
}

// DeleteManagedACLFile removes the registration of an ACL file MCM owned.
// The file itself is removed by the deploy service, which owns the
// transactional write.
//
// Like RecordManagedACLFile, the method does not take the mutation lock:
// the deploy service holds it while it updates the registry.
func (s *Store) DeleteManagedACLFile(ctx context.Context, path string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM managed_acl_files WHERE path = ?`, path)
	if err != nil {
		return fmt.Errorf("delete managed acl file: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted managed acl file row count: %w", err)
	}
	if affected == 0 {
		return ErrManagedACLFileNotFound
	}
	return nil
}

// ManagedACLFileExists reports whether the path is registered as a managed
// ACL file.
func (s *Store) ManagedACLFileExists(ctx context.Context, path string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM managed_acl_files WHERE path = ?`, path).Scan(&exists)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("check managed acl file: %w", err)
	}
	return true, nil
}
