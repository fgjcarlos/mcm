package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrUserNotFound is returned when an admin user does not exist.
	ErrUserNotFound = errors.New("admin user not found")
)

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		name:    "create_admin_users",
		sql: `
CREATE TABLE admin_users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	disabled INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_admin_users_username ON admin_users(username);
`,
	},
	{
		version: 2,
		name:    "create_broker_metrics",
		sql: `
CREATE TABLE broker_metric_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT '',
	topic TEXT NOT NULL DEFAULT '',
	payload_format TEXT NOT NULL DEFAULT '',
	payload_bytes INTEGER NOT NULL DEFAULT 0,
	truncated INTEGER NOT NULL DEFAULT 0,
	observed_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX idx_broker_metric_events_observed_at ON broker_metric_events(observed_at);
CREATE INDEX idx_broker_metric_events_type ON broker_metric_events(type);

CREATE TABLE broker_metric_samples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	observed_at TEXT NOT NULL,
	status TEXT NOT NULL DEFAULT '',
	messages_total INTEGER NOT NULL DEFAULT 0,
	payload_bytes_total INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE INDEX idx_broker_metric_samples_observed_at ON broker_metric_samples(observed_at);
`,
	},
}

// AdminUser is the stored administrative user model.
type AdminUser struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateAdminUserParams holds fields for user creation.
type CreateAdminUserParams struct {
	Username     string
	PasswordHash string
	Disabled     bool
}

// UpdateAdminUserParams holds mutable fields for user updates.
type UpdateAdminUserParams struct {
	Username     string
	PasswordHash *string
	Disabled     bool
}

// BrokerMetricEvent is a persisted broker metric/event without raw payload data.
type BrokerMetricEvent struct {
	ID            int64     `json:"id"`
	Type          string    `json:"type"`
	Status        string    `json:"status,omitempty"`
	Topic         string    `json:"topic,omitempty"`
	PayloadFormat string    `json:"payload_format,omitempty"`
	PayloadBytes  int       `json:"payload_bytes,omitempty"`
	Truncated     bool      `json:"truncated,omitempty"`
	ObservedAt    time.Time `json:"observed_at"`
	CreatedAt     time.Time `json:"created_at"`
}

// CreateBrokerMetricEventParams holds broker metric fields safe for persistence.
type CreateBrokerMetricEventParams struct {
	Type          string
	Status        string
	Topic         string
	PayloadFormat string
	PayloadBytes  int
	Truncated     bool
	ObservedAt    time.Time
}

// BrokerMetricSample stores counters derived from broker events.
type BrokerMetricSample struct {
	ID                int64     `json:"id"`
	ObservedAt        time.Time `json:"observed_at"`
	Status            string    `json:"status,omitempty"`
	MessagesTotal     int       `json:"messages_total"`
	PayloadBytesTotal int       `json:"payload_bytes_total"`
	CreatedAt         time.Time `json:"created_at"`
}

// BrokerMetricQuery controls metric query bounds.
type BrokerMetricQuery struct {
	Since time.Time
	Until time.Time
	Limit int
}

// Store wraps SQLite persistence.
type Store struct {
	db *sql.DB
}

// Open opens a SQLite database and ensures the schema is initialized.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	store := &Store{db: db}
	if err := store.Init(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

// Close closes the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// Init applies database migrations.
func (s *Store) Init(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TEXT NOT NULL
);
`); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	for _, migration := range migrations {
		var exists int
		if err = tx.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, migration.version).Scan(&exists); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check migration %d: %w", migration.version, err)
		}
		if exists == 1 {
			continue
		}

		if _, err = tx.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", migration.version, migration.name, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, name, applied_at) VALUES(?, ?, ?)`, migration.version, migration.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record migration %d: %w", migration.version, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

// CountAdminUsers returns the number of stored admin users.
func (s *Store) CountAdminUsers(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count admin users: %w", err)
	}
	return count, nil
}

// CreateAdminUser creates a new admin user.
func (s *Store) CreateAdminUser(ctx context.Context, params CreateAdminUserParams) (AdminUser, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO admin_users(username, password_hash, disabled, created_at, updated_at) VALUES(?, ?, ?, ?, ?)`,
		strings.TrimSpace(params.Username),
		params.PasswordHash,
		boolToInt(params.Disabled),
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return AdminUser{}, fmt.Errorf("create admin user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return AdminUser{}, fmt.Errorf("get created admin user id: %w", err)
	}

	return s.GetAdminUserByID(ctx, id)
}

// GetAdminUserByID returns an admin user by ID.
func (s *Store) GetAdminUserByID(ctx context.Context, id int64) (AdminUser, error) {
	return s.getAdminUser(ctx, `SELECT id, username, password_hash, disabled, created_at, updated_at FROM admin_users WHERE id = ?`, id)
}

// GetAdminUserByUsername returns an admin user by username.
func (s *Store) GetAdminUserByUsername(ctx context.Context, username string) (AdminUser, error) {
	return s.getAdminUser(ctx, `SELECT id, username, password_hash, disabled, created_at, updated_at FROM admin_users WHERE username = ?`, strings.TrimSpace(username))
}

func (s *Store) getAdminUser(ctx context.Context, query string, arg any) (AdminUser, error) {
	var user AdminUser
	var disabled int
	var createdAt string
	var updatedAt string

	err := s.db.QueryRowContext(ctx, query, arg).Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&disabled,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AdminUser{}, ErrUserNotFound
	}
	if err != nil {
		return AdminUser{}, fmt.Errorf("query admin user: %w", err)
	}

	user.Disabled = disabled == 1
	user.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return AdminUser{}, fmt.Errorf("parse admin user created_at: %w", err)
	}
	user.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return AdminUser{}, fmt.Errorf("parse admin user updated_at: %w", err)
	}

	return user, nil
}

// ListAdminUsers returns all admin users ordered by username.
func (s *Store) ListAdminUsers(ctx context.Context) ([]AdminUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, password_hash, disabled, created_at, updated_at FROM admin_users ORDER BY username ASC`)
	if err != nil {
		return nil, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()

	var users []AdminUser
	for rows.Next() {
		var user AdminUser
		var disabled int
		var createdAt string
		var updatedAt string
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &disabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan admin user: %w", err)
		}
		user.Disabled = disabled == 1
		user.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse admin user created_at: %w", err)
		}
		user.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse admin user updated_at: %w", err)
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin users: %w", err)
	}
	return users, nil
}

// UpdateAdminUser updates a stored admin user.
func (s *Store) UpdateAdminUser(ctx context.Context, id int64, params UpdateAdminUserParams) (AdminUser, error) {
	current, err := s.GetAdminUserByID(ctx, id)
	if err != nil {
		return AdminUser{}, err
	}

	passwordHash := current.PasswordHash
	if params.PasswordHash != nil {
		passwordHash = *params.PasswordHash
	}

	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE admin_users SET username = ?, password_hash = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(params.Username),
		passwordHash,
		boolToInt(params.Disabled),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	); err != nil {
		return AdminUser{}, fmt.Errorf("update admin user: %w", err)
	}

	return s.GetAdminUserByID(ctx, id)
}

// DeleteAdminUser removes an admin user.
func (s *Store) DeleteAdminUser(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM admin_users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete admin user: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted row count: %w", err)
	}
	if affected == 0 {
		return ErrUserNotFound
	}
	return nil
}

// RecordBrokerMetricEvent stores a broker event and its derived counter sample.
func (s *Store) RecordBrokerMetricEvent(ctx context.Context, params CreateBrokerMetricEventParams) (BrokerMetricEvent, error) {
	if strings.TrimSpace(params.Type) == "" {
		return BrokerMetricEvent{}, fmt.Errorf("broker metric event type is required")
	}
	if params.PayloadBytes < 0 {
		return BrokerMetricEvent{}, fmt.Errorf("broker metric payload bytes cannot be negative")
	}
	observedAt := params.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	createdAt := time.Now().UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("begin broker metric transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	result, err := tx.ExecContext(ctx, `
INSERT INTO broker_metric_events(type, status, topic, payload_format, payload_bytes, truncated, observed_at, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(params.Type),
		strings.TrimSpace(params.Status),
		strings.TrimSpace(params.Topic),
		strings.TrimSpace(params.PayloadFormat),
		params.PayloadBytes,
		boolToInt(params.Truncated),
		observedAt.Format(time.RFC3339Nano),
		createdAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("insert broker metric event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("get broker metric event id: %w", err)
	}

	messagesTotal := 0
	payloadBytesTotal := 0
	if strings.TrimSpace(params.Type) == "topic_message" {
		messagesTotal = 1
		payloadBytesTotal = params.PayloadBytes
	}
	if _, err = tx.ExecContext(ctx, `
INSERT INTO broker_metric_samples(observed_at, status, messages_total, payload_bytes_total, created_at)
VALUES(?, ?, ?, ?, ?)`,
		observedAt.Format(time.RFC3339Nano),
		strings.TrimSpace(params.Status),
		messagesTotal,
		payloadBytesTotal,
		createdAt.Format(time.RFC3339Nano),
	); err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("insert broker metric sample: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("commit broker metric event: %w", err)
	}

	return BrokerMetricEvent{
		ID:            id,
		Type:          strings.TrimSpace(params.Type),
		Status:        strings.TrimSpace(params.Status),
		Topic:         strings.TrimSpace(params.Topic),
		PayloadFormat: strings.TrimSpace(params.PayloadFormat),
		PayloadBytes:  params.PayloadBytes,
		Truncated:     params.Truncated,
		ObservedAt:    observedAt,
		CreatedAt:     createdAt,
	}, nil
}

// ListBrokerMetricEvents returns persisted broker events ordered newest first.
func (s *Store) ListBrokerMetricEvents(ctx context.Context, query BrokerMetricQuery) ([]BrokerMetricEvent, error) {
	where, args := brokerMetricWhere(query)
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `SELECT id, type, status, topic, payload_format, payload_bytes, truncated, observed_at, created_at FROM broker_metric_events`+where+` ORDER BY observed_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list broker metric events: %w", err)
	}
	defer rows.Close()

	var events []BrokerMetricEvent
	for rows.Next() {
		event, err := scanBrokerMetricEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate broker metric events: %w", err)
	}
	return events, nil
}

// ListBrokerMetricSamples returns persisted broker metric samples ordered newest first.
func (s *Store) ListBrokerMetricSamples(ctx context.Context, query BrokerMetricQuery) ([]BrokerMetricSample, error) {
	where, args := brokerMetricWhere(query)
	limit := query.Limit
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, `SELECT id, observed_at, status, messages_total, payload_bytes_total, created_at FROM broker_metric_samples`+where+` ORDER BY observed_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list broker metric samples: %w", err)
	}
	defer rows.Close()

	var samples []BrokerMetricSample
	for rows.Next() {
		var sample BrokerMetricSample
		var observedAt string
		var createdAt string
		if err := rows.Scan(&sample.ID, &observedAt, &sample.Status, &sample.MessagesTotal, &sample.PayloadBytesTotal, &createdAt); err != nil {
			return nil, fmt.Errorf("scan broker metric sample: %w", err)
		}
		var err error
		sample.ObservedAt, err = time.Parse(time.RFC3339Nano, observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse broker metric sample observed_at: %w", err)
		}
		sample.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse broker metric sample created_at: %w", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate broker metric samples: %w", err)
	}
	return samples, nil
}

// PruneBrokerMetrics deletes broker metrics older than cutoff and returns deleted row counts.
func (s *Store) PruneBrokerMetrics(ctx context.Context, cutoff time.Time) (int64, int64, error) {
	cutoffText := cutoff.UTC().Format(time.RFC3339Nano)
	eventResult, err := s.db.ExecContext(ctx, `DELETE FROM broker_metric_events WHERE observed_at < ?`, cutoffText)
	if err != nil {
		return 0, 0, fmt.Errorf("prune broker metric events: %w", err)
	}
	sampleResult, err := s.db.ExecContext(ctx, `DELETE FROM broker_metric_samples WHERE observed_at < ?`, cutoffText)
	if err != nil {
		return 0, 0, fmt.Errorf("prune broker metric samples: %w", err)
	}
	eventsDeleted, err := eventResult.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count pruned broker metric events: %w", err)
	}
	samplesDeleted, err := sampleResult.RowsAffected()
	if err != nil {
		return 0, 0, fmt.Errorf("count pruned broker metric samples: %w", err)
	}
	return eventsDeleted, samplesDeleted, nil
}

func scanBrokerMetricEvent(rows interface {
	Scan(dest ...any) error
}) (BrokerMetricEvent, error) {
	var event BrokerMetricEvent
	var truncated int
	var observedAt string
	var createdAt string
	if err := rows.Scan(&event.ID, &event.Type, &event.Status, &event.Topic, &event.PayloadFormat, &event.PayloadBytes, &truncated, &observedAt, &createdAt); err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("scan broker metric event: %w", err)
	}
	var err error
	event.Truncated = truncated == 1
	event.ObservedAt, err = time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("parse broker metric event observed_at: %w", err)
	}
	event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return BrokerMetricEvent{}, fmt.Errorf("parse broker metric event created_at: %w", err)
	}
	return event, nil
}

func brokerMetricWhere(query BrokerMetricQuery) (string, []any) {
	var clauses []string
	var args []any
	if !query.Since.IsZero() {
		clauses = append(clauses, "observed_at >= ?")
		args = append(args, query.Since.UTC().Format(time.RFC3339Nano))
	}
	if !query.Until.IsZero() {
		clauses = append(clauses, "observed_at <= ?")
		args = append(args, query.Until.UTC().Format(time.RFC3339Nano))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
