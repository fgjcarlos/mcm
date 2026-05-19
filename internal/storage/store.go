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

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
