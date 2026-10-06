package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrListenerNotFound is returned when a listener specification does not exist.
var ErrListenerNotFound = errors.New("listener spec not found")

// ErrListenerConflict is returned when a listener specification conflicts with an existing row.
var ErrListenerConflict = errors.New("listener spec conflict")

// ListenerSpecRow is the SQLite projection of a Mosquitto listener specification.
type ListenerSpecRow struct {
	ID        string
	Port      int
	Bind      string
	Protocols []string
	Options   map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InsertListenerSpec persists row and populates timestamps that were not provided.
func (s *Store) InsertListenerSpec(ctx context.Context, row ListenerSpecRow) (ListenerSpecRow, error) {
	s.LockMutations()
	defer s.UnlockMutations()

	now := time.Now().UTC()
	if row.CreatedAt.IsZero() {
		row.CreatedAt = now
	}
	if row.UpdatedAt.IsZero() {
		row.UpdatedAt = now
	}
	if err := insertListenerSpec(ctx, s.db, row); err != nil {
		return ListenerSpecRow{}, err
	}
	return row, nil
}

// GetListenerSpec returns the listener specification with id.
func (s *Store) GetListenerSpec(ctx context.Context, id string) (ListenerSpecRow, error) {
	row, err := scanListenerSpec(s.db.QueryRowContext(ctx, listenerSpecSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ListenerSpecRow{}, ErrListenerNotFound
	}
	if err != nil {
		return ListenerSpecRow{}, fmt.Errorf("get listener spec: %w", err)
	}
	return row, nil
}

// ListListenerSpecs returns all listener specifications ordered by bind then port.
func (s *Store) ListListenerSpecs(ctx context.Context) ([]ListenerSpecRow, error) {
	rows, err := s.db.QueryContext(ctx, listenerSpecSelect+` ORDER BY bind ASC, port ASC`)
	if err != nil {
		return nil, fmt.Errorf("list listener specs: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var specs []ListenerSpecRow
	for rows.Next() {
		spec, err := scanListenerSpec(rows)
		if err != nil {
			return nil, fmt.Errorf("scan listener spec: %w", err)
		}
		specs = append(specs, spec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate listener specs: %w", err)
	}
	return specs, nil
}

// UpdateListenerSpec applies mutate to the listener specification with id.
// The callback may not change the stable public ID.
func (s *Store) UpdateListenerSpec(ctx context.Context, id string, mutate func(*ListenerSpecRow) error) (ListenerSpecRow, error) {
	s.LockMutations()
	defer s.UnlockMutations()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ListenerSpecRow{}, fmt.Errorf("begin update listener spec transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	row, err := scanListenerSpec(tx.QueryRowContext(ctx, listenerSpecSelect+` WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ListenerSpecRow{}, ErrListenerNotFound
	}
	if err != nil {
		return ListenerSpecRow{}, fmt.Errorf("get listener spec for update: %w", err)
	}
	if err := mutate(&row); err != nil {
		return ListenerSpecRow{}, err
	}
	if row.ID != id {
		return ListenerSpecRow{}, errors.New("listener spec ID cannot be changed")
	}
	row.UpdatedAt = time.Now().UTC()
	if err := updateListenerSpec(ctx, tx, row); err != nil {
		return ListenerSpecRow{}, err
	}
	if err := tx.Commit(); err != nil {
		return ListenerSpecRow{}, fmt.Errorf("commit update listener spec transaction: %w", err)
	}
	return row, nil
}

// DeleteListenerSpec removes the listener specification with id.
func (s *Store) DeleteListenerSpec(ctx context.Context, id string) error {
	s.LockMutations()
	defer s.UnlockMutations()

	result, err := s.db.ExecContext(ctx, `DELETE FROM listener_specs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete listener spec: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted listener spec row count: %w", err)
	}
	if affected == 0 {
		return ErrListenerNotFound
	}
	return nil
}

// ReplaceAllListenerSpecs atomically replaces every listener specification.
func (s *Store) ReplaceAllListenerSpecs(ctx context.Context, rows []ListenerSpecRow) error {
	s.LockMutations()
	defer s.UnlockMutations()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin replace listener specs transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM listener_specs`); err != nil {
		return fmt.Errorf("clear listener specs: %w", err)
	}
	now := time.Now().UTC()
	for _, row := range rows {
		if row.CreatedAt.IsZero() {
			row.CreatedAt = now
		}
		if row.UpdatedAt.IsZero() {
			row.UpdatedAt = now
		}
		if err := insertListenerSpec(ctx, tx, row); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit replace listener specs transaction: %w", err)
	}
	return nil
}

const listenerSpecSelect = `SELECT id, port, bind, protocols, options, created_at, updated_at FROM listener_specs`

type listenerSpecExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func insertListenerSpec(ctx context.Context, executor listenerSpecExecutor, row ListenerSpecRow) error {
	if err := listenerSpecBindingAvailable(ctx, executor, row.ID, row.Port, row.Bind); err != nil {
		return err
	}
	protocols, options, err := marshalListenerSpecFields(row)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, `
INSERT INTO listener_specs(id, port, bind, protocols, options, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		row.ID, row.Port, row.Bind, protocols, options,
		row.CreatedAt.UTC().Format(time.RFC3339Nano), row.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		if isListenerSpecConflict(err) {
			return ErrListenerConflict
		}
		return fmt.Errorf("insert listener spec: %w", err)
	}
	return nil
}

func updateListenerSpec(ctx context.Context, executor listenerSpecExecutor, row ListenerSpecRow) error {
	if err := listenerSpecBindingAvailable(ctx, executor, row.ID, row.Port, row.Bind); err != nil {
		return err
	}
	protocols, options, err := marshalListenerSpecFields(row)
	if err != nil {
		return err
	}
	_, err = executor.ExecContext(ctx, `
UPDATE listener_specs SET port = ?, bind = ?, protocols = ?, options = ?, updated_at = ?
WHERE id = ?`, row.Port, row.Bind, protocols, options, row.UpdatedAt.UTC().Format(time.RFC3339Nano), row.ID)
	if err != nil {
		if isListenerSpecConflict(err) {
			return ErrListenerConflict
		}
		return fmt.Errorf("update listener spec: %w", err)
	}
	return nil
}

func listenerSpecBindingAvailable(ctx context.Context, executor listenerSpecExecutor, id string, port int, bind string) error {
	var existingID string
	err := executor.QueryRowContext(ctx,
		`SELECT id FROM listener_specs WHERE port = ? AND bind = ? AND id != ?`, port, bind, id,
	).Scan(&existingID)
	if err == nil {
		return ErrListenerConflict
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("check listener spec binding: %w", err)
}

func marshalListenerSpecFields(row ListenerSpecRow) (string, string, error) {
	protocols := row.Protocols
	if protocols == nil {
		protocols = []string{}
	}
	options := row.Options
	if options == nil {
		options = map[string]string{}
	}
	encodedProtocols, err := json.Marshal(protocols)
	if err != nil {
		return "", "", fmt.Errorf("marshal listener spec protocols: %w", err)
	}
	encodedOptions, err := json.Marshal(options)
	if err != nil {
		return "", "", fmt.Errorf("marshal listener spec options: %w", err)
	}
	return string(encodedProtocols), string(encodedOptions), nil
}

func scanListenerSpec(row interface{ Scan(...any) error }) (ListenerSpecRow, error) {
	var spec ListenerSpecRow
	var protocols, options, createdAt, updatedAt string
	if err := row.Scan(&spec.ID, &spec.Port, &spec.Bind, &protocols, &options, &createdAt, &updatedAt); err != nil {
		return ListenerSpecRow{}, err
	}
	if err := json.Unmarshal([]byte(protocols), &spec.Protocols); err != nil {
		return ListenerSpecRow{}, fmt.Errorf("unmarshal listener spec protocols: %w", err)
	}
	if err := json.Unmarshal([]byte(options), &spec.Options); err != nil {
		return ListenerSpecRow{}, fmt.Errorf("unmarshal listener spec options: %w", err)
	}
	var err error
	spec.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return ListenerSpecRow{}, fmt.Errorf("parse listener spec created_at: %w", err)
	}
	spec.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return ListenerSpecRow{}, fmt.Errorf("parse listener spec updated_at: %w", err)
	}
	return spec, nil
}

func isListenerSpecConflict(err error) bool {
	// modernc's SQLite driver wraps constraint errors without exposing a stable
	// driver error type here, so retain compatibility by matching SQLite's
	// documented UNIQUE constraint message instead of importing the driver.
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
