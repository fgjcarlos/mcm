package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/schema"
	_ "modernc.org/sqlite"
)

var (
	// ErrUserNotFound is returned when an admin user does not exist.
	ErrUserNotFound = errors.New("admin user not found")
	// ErrLastActiveAdmin is returned when mutating the last active admin would lock out the system.
	ErrLastActiveAdmin = errors.New("cannot disable or delete the last active admin")
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
	{
		version: 3,
		name:    "create_security_events",
		sql: `
CREATE TABLE security_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	category TEXT NOT NULL,
	reason TEXT NOT NULL,
	username TEXT NOT NULL DEFAULT '',
	source_ip TEXT NOT NULL DEFAULT '',
	method TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL DEFAULT '',
	observed_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);
CREATE INDEX idx_security_events_observed_at ON security_events(observed_at);
CREATE INDEX idx_security_events_category ON security_events(category);
`,
	},
	{
		version: 4,
		name:    "create_audit_events",
		sql: `
CREATE TABLE audit_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	occurred_at TEXT NOT NULL,
	actor TEXT NOT NULL,
	action TEXT NOT NULL,
	resource_type TEXT NOT NULL,
	resource_id TEXT NOT NULL DEFAULT '',
	result TEXT NOT NULL,
	metadata TEXT NOT NULL DEFAULT '{}'
);
CREATE INDEX idx_audit_events_occurred_at ON audit_events(occurred_at);
CREATE INDEX idx_audit_events_resource ON audit_events(resource_type, resource_id);
CREATE INDEX idx_audit_events_actor ON audit_events(actor);
`,
	},
	{
		version: 5,
		name:    "create_json_schemas",
		sql: `
CREATE TABLE json_schemas (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	topic_filter TEXT NOT NULL,
	schema TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_json_schemas_topic_filter ON json_schemas(topic_filter);
CREATE INDEX idx_json_schemas_enabled ON json_schemas(enabled);
`,
	},
	{
		version: 6,
		name:    "create_acl_rules",
		sql: `
CREATE TABLE acl_rules (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	principal TEXT NOT NULL,
	topic_filter TEXT NOT NULL,
	permission TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_acl_rules_principal ON acl_rules(principal);
CREATE INDEX idx_acl_rules_topic_filter ON acl_rules(topic_filter);
`,
	},
	{
		version: 7,
		name:    "create_login_attempts",
		sql: `
CREATE TABLE login_attempts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL DEFAULT '',
	source_ip TEXT NOT NULL DEFAULT '',
	success INTEGER NOT NULL DEFAULT 0,
	attempted_at TEXT NOT NULL
);
CREATE INDEX idx_login_attempts_source_ip_attempted ON login_attempts(source_ip, attempted_at);
CREATE INDEX idx_login_attempts_username_attempted ON login_attempts(username, attempted_at);
CREATE INDEX idx_login_attempts_attempted_at ON login_attempts(attempted_at);
`,
	},
	{
		version: 8,
		name:    "add_admin_users_role",
		sql: `
ALTER TABLE admin_users ADD COLUMN role TEXT NOT NULL DEFAULT 'admin';
CREATE INDEX idx_admin_users_role ON admin_users(role);
`,
	},
	{
		version: 9,
		name:    "add_admin_users_mfa",
		sql: `
ALTER TABLE admin_users ADD COLUMN mfa_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE admin_users ADD COLUMN mfa_secret TEXT NOT NULL DEFAULT '';
`,
	},
	{
		version: 10,
		name:    "create_admin_mfa_recovery_codes",
		sql: `
CREATE TABLE admin_mfa_recovery_codes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	admin_user_id INTEGER NOT NULL,
	code_hash TEXT NOT NULL,
	used INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	used_at TEXT NOT NULL DEFAULT '',
	FOREIGN KEY (admin_user_id) REFERENCES admin_users(id) ON DELETE CASCADE
);
CREATE INDEX idx_admin_mfa_recovery_codes_user ON admin_mfa_recovery_codes(admin_user_id, used);
`,
	},
	{
		version: 11,
		name:    "create_mqtt_users",
		sql: `
CREATE TABLE mqtt_users (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	disabled INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_mqtt_users_username ON mqtt_users(username);
`,
	},
	{
		version: 12,
		name:    "create_deployments",
		sql: `
CREATE TABLE deployments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	actor TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'pending',
	acl_snapshot TEXT NOT NULL DEFAULT '',
	passwd_snapshot TEXT NOT NULL DEFAULT '',
	acl_rendered TEXT NOT NULL DEFAULT '',
	passwd_rendered TEXT NOT NULL DEFAULT '',
	message TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_deployments_status ON deployments(status);
CREATE INDEX idx_deployments_created_at ON deployments(created_at);
`,
	},
	{
		version: 13,
		name:    "create_edge_sites",
		sql: `
CREATE TABLE edge_sites (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	version TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'unknown',
	message TEXT NOT NULL DEFAULT '',
	last_seen_at TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX idx_edge_sites_status ON edge_sites(status);
CREATE INDEX idx_edge_sites_last_seen_at ON edge_sites(last_seen_at);
`,
	},
	{
		version: 14,
		name:    "add_admin_users_mfa_last_totp_step",
		sql: `
ALTER TABLE admin_users ADD COLUMN mfa_last_totp_step INTEGER NOT NULL DEFAULT -1;
`,
	},
}

// AdminUser is the stored administrative user model.
type AdminUser struct {
	ID              int64     `json:"id"`
	Username        string    `json:"username"`
	PasswordHash    string    `json:"-"`
	Disabled        bool      `json:"disabled"`
	Role            string    `json:"role"`
	MFAEnabled      bool      `json:"mfa_enabled"`
	MFASecret       string    `json:"-"`
	MFALastTOTPStep int64     `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// CreateAdminUserParams holds fields for user creation.
type CreateAdminUserParams struct {
	Username     string
	PasswordHash string
	Disabled     bool
	Role         string
}

// UpdateAdminUserParams holds mutable fields for user updates.
type UpdateAdminUserParams struct {
	Username     string
	PasswordHash *string
	Disabled     bool
	Role         string
}

// MQTTUser is a stored MQTT broker credential.
type MQTTUser struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	Disabled     bool      `json:"disabled"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// CreateMQTTUserParams holds fields for MQTT user creation.
type CreateMQTTUserParams struct {
	Username     string
	PasswordHash string
}

// UpdateMQTTUserParams holds mutable fields for MQTT user updates.
type UpdateMQTTUserParams struct {
	Username     *string
	PasswordHash *string
	Disabled     *bool
}

// ErrMQTTUserNotFound is returned when an MQTT user does not exist.
var ErrMQTTUserNotFound = errors.New("mqtt user not found")

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

// SecurityEvent is an operator-facing audit event without secrets or request payloads.
type SecurityEvent struct {
	ID         int64     `json:"id"`
	Category   string    `json:"category"`
	Reason     string    `json:"reason"`
	Username   string    `json:"username,omitempty"`
	SourceIP   string    `json:"source_ip,omitempty"`
	Method     string    `json:"method,omitempty"`
	Path       string    `json:"path,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// CreateSecurityEventParams holds sanitized event fields safe for persistence.
type CreateSecurityEventParams struct {
	Category   string
	Reason     string
	Username   string
	SourceIP   string
	Method     string
	Path       string
	ObservedAt time.Time
}

// SecurityEventQuery controls security event query bounds.
type SecurityEventQuery struct {
	Limit int
}

// AuditEvent is a durable record of a security-relevant administrative action.
type AuditEvent struct {
	ID           int64           `json:"id"`
	OccurredAt   time.Time       `json:"occurred_at"`
	Actor        string          `json:"actor"`
	Action       string          `json:"action"`
	ResourceType string          `json:"resource_type"`
	ResourceID   string          `json:"resource_id,omitempty"`
	Result       string          `json:"result"`
	Metadata     json.RawMessage `json:"metadata"`
}

// CreateAuditEventParams holds fields for audit event creation.
type CreateAuditEventParams struct {
	OccurredAt   time.Time
	Actor        string
	Action       string
	ResourceType string
	ResourceID   string
	Result       string
	Metadata     json.RawMessage
}

// AuditEventQuery controls audit event pagination.
type AuditEventQuery struct {
	Limit  int
	Offset int
}

// JSONSchemaDefinition is an operator-defined JSON schema bound to an MQTT topic filter.
type JSONSchemaDefinition struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	TopicFilter string          `json:"topic_filter"`
	Schema      json.RawMessage `json:"schema"`
	Description string          `json:"description,omitempty"`
	Enabled     bool            `json:"enabled"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// CreateJSONSchemaParams holds fields for schema definition creation.
type CreateJSONSchemaParams struct {
	Name        string
	TopicFilter string
	Schema      json.RawMessage
	Description string
	Enabled     bool
}

// UpdateJSONSchemaParams holds mutable schema definition fields.
type UpdateJSONSchemaParams struct {
	Name        string
	TopicFilter string
	Schema      json.RawMessage
	Description string
	Enabled     bool
}

// Store wraps SQLite persistence.
type Store struct {
	db *sql.DB
}

// sqliteDSNBase returns the "file:" URI form of path with the given query
// params appended. Using the "file:" URI scheme is required for correctness:
// the plain "path?..." form works only when path contains no "?" character;
// any "?" in the path would corrupt DSN parsing in the modernc.org/sqlite
// driver. The "file:" form with url.PathEscape is unambiguous for all paths
// including those with spaces, question marks, or other special characters.
//
// The driver passes SQLITE_OPEN_URI to sqlite3_open_v2 so the "file:" URI is
// handled natively by SQLite. Relative paths remain relative; absolute paths
// (both Unix and Windows) are preserved exactly by PathEscape which encodes
// only characters that are not valid in a URI path segment.
func sqliteDSNBase(path string) string {
	return "file:" + url.PathEscape(path)
}

// SQLiteDSN builds the canonical DSN for the server store. It enables WAL
// journal mode, a 5-second busy timeout, NORMAL synchronous mode, and
// foreign-key enforcement on every connection.
//
// Design notes:
//   - SetMaxOpenConns(1) + SetMaxIdleConns(1) serialise all writes through a
//     single connection, eliminating SQLITE_BUSY from internal goroutines.
//     WAL's reader concurrency only matters for external processes (e.g. the
//     backup CLI); busy_timeout covers that case.
//   - foreign_keys is a per-connection pragma (not persisted), so it must be
//     applied on every new connection via _pragma=.
//   - modernc.org/sqlite automatically runs busy_timeout before other pragmas
//     (gitlab.com/cznic/sqlite issue #198), so declaration order here does not
//     matter.
func SQLiteDSN(path string) string {
	return sqliteDSNBase(path) + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)"
}

// SQLiteBackupDSN builds a read-intent DSN for use by the backup command when
// opening either the source database (for VACUUM INTO) or a backup artifact
// (for validation). It intentionally omits journal_mode(WAL): setting
// journal_mode on the backup artifact would convert it from DELETE mode to WAL,
// which is an unwanted and irreversible side-effect on the artifact file. Only
// busy_timeout is applied so that VACUUM INTO retries under write pressure from
// the server process instead of returning SQLITE_BUSY immediately.
func SQLiteBackupDSN(path string) string {
	return sqliteDSNBase(path) + "?_pragma=busy_timeout(5000)"
}

// Open opens a SQLite database and ensures the schema is initialized.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("database path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", SQLiteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	// Serialise all writes through a single connection. WAL mode allows
	// concurrent reads from additional connections, but having more than one
	// writer causes SQLITE_BUSY under load. A single connection also removes
	// the "cannot start a transaction within a transaction" error that occurs
	// when database/sql re-uses a connection that still has an open transaction.
	// SetMaxIdleConns(1) matches SetMaxOpenConns(1) so that database/sql does
	// not retain more idle connections than the pool can ever open.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

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

// Ping verifies that the database connection is available.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
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

// CountActiveAdminUsers returns the number of enabled users with the admin role.
func (s *Store) CountActiveAdminUsers(ctx context.Context) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM admin_users WHERE disabled = 0 AND role = 'admin'`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active admin users: %w", err)
	}
	return count, nil
}

// CreateAdminUser creates a new admin user.
func (s *Store) CreateAdminUser(ctx context.Context, params CreateAdminUserParams) (AdminUser, error) {
	role := strings.TrimSpace(params.Role)
	if role == "" {
		role = "admin"
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO admin_users(username, password_hash, disabled, role, mfa_enabled, mfa_secret, created_at, updated_at) VALUES(?, ?, ?, ?, 0, '', ?, ?)`,
		strings.TrimSpace(params.Username),
		params.PasswordHash,
		boolToInt(params.Disabled),
		role,
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
	return s.getAdminUser(ctx, `SELECT id, username, password_hash, disabled, role, mfa_enabled, mfa_secret, mfa_last_totp_step, created_at, updated_at FROM admin_users WHERE id = ?`, id)
}

// GetAdminUserByUsername returns an admin user by username.
func (s *Store) GetAdminUserByUsername(ctx context.Context, username string) (AdminUser, error) {
	return s.getAdminUser(ctx, `SELECT id, username, password_hash, disabled, role, mfa_enabled, mfa_secret, mfa_last_totp_step, created_at, updated_at FROM admin_users WHERE username = ?`, strings.TrimSpace(username))
}

func (s *Store) getAdminUser(ctx context.Context, query string, arg any) (AdminUser, error) {
	var user AdminUser
	var disabled int
	var mfaEnabled int
	var createdAt string
	var updatedAt string

	err := s.db.QueryRowContext(ctx, query, arg).Scan(
		&user.ID,
		&user.Username,
		&user.PasswordHash,
		&disabled,
		&user.Role,
		&mfaEnabled,
		&user.MFASecret,
		&user.MFALastTOTPStep,
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
	user.MFAEnabled = mfaEnabled == 1
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
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, password_hash, disabled, role, mfa_enabled, mfa_secret, mfa_last_totp_step, created_at, updated_at FROM admin_users ORDER BY username ASC`)
	if err != nil {
		return nil, fmt.Errorf("list admin users: %w", err)
	}
	defer rows.Close()

	var users []AdminUser
	for rows.Next() {
		var user AdminUser
		var disabled int
		var mfaEnabled int
		var createdAt string
		var updatedAt string
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &disabled, &user.Role, &mfaEnabled, &user.MFASecret, &user.MFALastTOTPStep, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan admin user: %w", err)
		}
		user.Disabled = disabled == 1
		user.MFAEnabled = mfaEnabled == 1
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
	role := strings.TrimSpace(params.Role)
	if role == "" {
		role = current.Role
	}
	if current.Role == "admin" && !current.Disabled && (params.Disabled || role != current.Role) {
		activeAdmins, err := s.CountActiveAdminUsers(ctx)
		if err != nil {
			return AdminUser{}, err
		}
		if activeAdmins <= 1 {
			return AdminUser{}, ErrLastActiveAdmin
		}
	}

	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE admin_users SET username = ?, password_hash = ?, disabled = ?, role = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(params.Username),
		passwordHash,
		boolToInt(params.Disabled),
		role,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	); err != nil {
		return AdminUser{}, fmt.Errorf("update admin user: %w", err)
	}

	return s.GetAdminUserByID(ctx, id)
}

// DeleteAdminUser removes an admin user.
func (s *Store) DeleteAdminUser(ctx context.Context, id int64) error {
	current, err := s.GetAdminUserByID(ctx, id)
	if err != nil {
		return err
	}
	if current.Role == "admin" && !current.Disabled {
		activeAdmins, err := s.CountActiveAdminUsers(ctx)
		if err != nil {
			return err
		}
		if activeAdmins <= 1 {
			return ErrLastActiveAdmin
		}
	}

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

// CreateJSONSchema creates a JSON schema definition.
func (s *Store) CreateJSONSchema(ctx context.Context, params CreateJSONSchemaParams) (JSONSchemaDefinition, error) {
	if err := validateJSONSchemaFields(params.Name, params.TopicFilter, params.Schema); err != nil {
		return JSONSchemaDefinition{}, err
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx, `
INSERT INTO json_schemas(name, topic_filter, schema, description, enabled, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(params.Name),
		strings.TrimSpace(params.TopicFilter),
		strings.TrimSpace(string(params.Schema)),
		strings.TrimSpace(params.Description),
		boolToInt(params.Enabled),
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return JSONSchemaDefinition{}, fmt.Errorf("create json schema: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return JSONSchemaDefinition{}, fmt.Errorf("get json schema id: %w", err)
	}
	return s.GetJSONSchema(ctx, id)
}

// GetJSONSchema returns a schema definition by ID.
func (s *Store) GetJSONSchema(ctx context.Context, id int64) (JSONSchemaDefinition, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, name, topic_filter, schema, description, enabled, created_at, updated_at FROM json_schemas WHERE id = ?`, id)
	return scanJSONSchema(row)
}

// ListJSONSchemas returns all schema definitions ordered by topic filter and name.
func (s *Store) ListJSONSchemas(ctx context.Context) ([]JSONSchemaDefinition, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, topic_filter, schema, description, enabled, created_at, updated_at FROM json_schemas ORDER BY topic_filter ASC, name ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list json schemas: %w", err)
	}
	defer rows.Close()
	var schemas []JSONSchemaDefinition
	for rows.Next() {
		definition, err := scanJSONSchema(rows)
		if err != nil {
			return nil, err
		}
		schemas = append(schemas, definition)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate json schemas: %w", err)
	}
	return schemas, nil
}

// UpdateJSONSchema updates a schema definition.
func (s *Store) UpdateJSONSchema(ctx context.Context, id int64, params UpdateJSONSchemaParams) (JSONSchemaDefinition, error) {
	if err := validateJSONSchemaFields(params.Name, params.TopicFilter, params.Schema); err != nil {
		return JSONSchemaDefinition{}, err
	}
	result, err := s.db.ExecContext(ctx, `
UPDATE json_schemas SET name = ?, topic_filter = ?, schema = ?, description = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		strings.TrimSpace(params.Name),
		strings.TrimSpace(params.TopicFilter),
		strings.TrimSpace(string(params.Schema)),
		strings.TrimSpace(params.Description),
		boolToInt(params.Enabled),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return JSONSchemaDefinition{}, fmt.Errorf("update json schema: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return JSONSchemaDefinition{}, fmt.Errorf("get updated json schema row count: %w", err)
	} else if affected == 0 {
		return JSONSchemaDefinition{}, sql.ErrNoRows
	}
	return s.GetJSONSchema(ctx, id)
}

// DeleteJSONSchema removes a schema definition.
func (s *Store) DeleteJSONSchema(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM json_schemas WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete json schema: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return fmt.Errorf("get deleted json schema row count: %w", err)
	} else if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func validateJSONSchemaFields(name, topicFilter string, schemaDoc json.RawMessage) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("schema name is required")
	}
	if err := acl.ValidateTopicFilter(strings.TrimSpace(topicFilter)); err != nil {
		return err
	}
	if len(schemaDoc) > 64*1024 {
		return fmt.Errorf("schema document exceeds 65536 bytes")
	}
	if err := schema.ValidateSchemaDocument(schemaDoc); err != nil {
		return err
	}
	return nil
}

func scanJSONSchema(rows interface{ Scan(dest ...any) error }) (JSONSchemaDefinition, error) {
	var definition JSONSchemaDefinition
	var schemaText string
	var enabled int
	var createdAt string
	var updatedAt string
	if err := rows.Scan(&definition.ID, &definition.Name, &definition.TopicFilter, &schemaText, &definition.Description, &enabled, &createdAt, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return JSONSchemaDefinition{}, err
		}
		return JSONSchemaDefinition{}, fmt.Errorf("scan json schema: %w", err)
	}
	definition.Schema = json.RawMessage(schemaText)
	definition.Enabled = enabled == 1
	var err error
	definition.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return JSONSchemaDefinition{}, fmt.Errorf("parse json schema created_at: %w", err)
	}
	definition.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return JSONSchemaDefinition{}, fmt.Errorf("parse json schema updated_at: %w", err)
	}
	return definition, nil
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

// PruneAuditEvents deletes audit events older than cutoff and returns the number of rows removed.
func (s *Store) PruneAuditEvents(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM audit_events WHERE occurred_at < ?`, cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune audit events: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned audit events: %w", err)
	}
	return affected, nil
}

// PruneSecurityEvents deletes security events older than cutoff and returns the number of rows removed.
func (s *Store) PruneSecurityEvents(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM security_events WHERE observed_at < ?`, cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune security events: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned security events: %w", err)
	}
	return affected, nil
}

// RecordAuditEvent stores a sanitized audit event.
func (s *Store) RecordAuditEvent(ctx context.Context, params CreateAuditEventParams) (AuditEvent, error) {
	occurredAt := params.OccurredAt.UTC()
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	metadata := strings.TrimSpace(string(params.Metadata))
	if metadata == "" || !json.Valid([]byte(metadata)) {
		metadata = "{}"
	}

	result, err := s.db.ExecContext(ctx, `
INSERT INTO audit_events(occurred_at, actor, action, resource_type, resource_id, result, metadata)
VALUES(?, ?, ?, ?, ?, ?, ?)`,
		occurredAt.Format(time.RFC3339Nano),
		defaultString(strings.TrimSpace(params.Actor), "unknown"),
		strings.TrimSpace(params.Action),
		strings.TrimSpace(params.ResourceType),
		strings.TrimSpace(params.ResourceID),
		defaultString(strings.TrimSpace(params.Result), "unknown"),
		metadata,
	)
	if err != nil {
		return AuditEvent{}, fmt.Errorf("record audit event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return AuditEvent{}, fmt.Errorf("get audit event id: %w", err)
	}
	return AuditEvent{
		ID:           id,
		OccurredAt:   occurredAt,
		Actor:        defaultString(strings.TrimSpace(params.Actor), "unknown"),
		Action:       strings.TrimSpace(params.Action),
		ResourceType: strings.TrimSpace(params.ResourceType),
		ResourceID:   strings.TrimSpace(params.ResourceID),
		Result:       defaultString(strings.TrimSpace(params.Result), "unknown"),
		Metadata:     json.RawMessage(metadata),
	}, nil
}

// ListAuditEvents returns audit events ordered newest first with offset pagination.
func (s *Store) ListAuditEvents(ctx context.Context, query AuditEventQuery) ([]AuditEvent, error) {
	limit := query.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, occurred_at, actor, action, resource_type, resource_id, result, metadata FROM audit_events ORDER BY occurred_at DESC, id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()

	var events []AuditEvent
	for rows.Next() {
		var event AuditEvent
		var occurredAt string
		var metadata string
		if err := rows.Scan(&event.ID, &occurredAt, &event.Actor, &event.Action, &event.ResourceType, &event.ResourceID, &event.Result, &metadata); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		var err error
		event.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return nil, fmt.Errorf("parse audit event occurred_at: %w", err)
		}
		event.Metadata = json.RawMessage(metadata)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate audit events: %w", err)
	}
	return events, nil
}

// RecordSecurityEvent stores a sanitized security event for operator visibility.
func (s *Store) RecordSecurityEvent(ctx context.Context, params CreateSecurityEventParams) (SecurityEvent, error) {
	category := strings.TrimSpace(params.Category)
	reason := strings.TrimSpace(params.Reason)
	if category == "" {
		return SecurityEvent{}, fmt.Errorf("security event category is required")
	}
	if reason == "" {
		return SecurityEvent{}, fmt.Errorf("security event reason is required")
	}
	observedAt := params.ObservedAt.UTC()
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	createdAt := time.Now().UTC()

	result, err := s.db.ExecContext(ctx, `
INSERT INTO security_events(category, reason, username, source_ip, method, path, observed_at, created_at)
VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		category,
		reason,
		strings.TrimSpace(params.Username),
		strings.TrimSpace(params.SourceIP),
		strings.TrimSpace(params.Method),
		strings.TrimSpace(params.Path),
		observedAt.Format(time.RFC3339Nano),
		createdAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return SecurityEvent{}, fmt.Errorf("insert security event: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return SecurityEvent{}, fmt.Errorf("get security event id: %w", err)
	}

	return SecurityEvent{
		ID:         id,
		Category:   category,
		Reason:     reason,
		Username:   strings.TrimSpace(params.Username),
		SourceIP:   strings.TrimSpace(params.SourceIP),
		Method:     strings.TrimSpace(params.Method),
		Path:       strings.TrimSpace(params.Path),
		ObservedAt: observedAt,
		CreatedAt:  createdAt,
	}, nil
}

// ListSecurityEvents returns recent security events ordered newest first.
func (s *Store) ListSecurityEvents(ctx context.Context, query SecurityEventQuery) ([]SecurityEvent, error) {
	limit := query.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	rows, err := s.db.QueryContext(ctx, `SELECT id, category, reason, username, source_ip, method, path, observed_at, created_at FROM security_events ORDER BY observed_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list security events: %w", err)
	}
	defer rows.Close()

	var events []SecurityEvent
	for rows.Next() {
		event, err := scanSecurityEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate security events: %w", err)
	}
	return events, nil
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

func scanSecurityEvent(rows interface {
	Scan(dest ...any) error
}) (SecurityEvent, error) {
	var event SecurityEvent
	var observedAt string
	var createdAt string
	if err := rows.Scan(&event.ID, &event.Category, &event.Reason, &event.Username, &event.SourceIP, &event.Method, &event.Path, &observedAt, &createdAt); err != nil {
		return SecurityEvent{}, fmt.Errorf("scan security event: %w", err)
	}
	var err error
	event.ObservedAt, err = time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return SecurityEvent{}, fmt.Errorf("parse security event observed_at: %w", err)
	}
	event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return SecurityEvent{}, fmt.Errorf("parse security event created_at: %w", err)
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

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
