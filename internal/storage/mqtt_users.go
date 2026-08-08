package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateMQTTUser creates a new MQTT user.
func (s *Store) CreateMQTTUser(ctx context.Context, params CreateMQTTUserParams) (MQTTUser, error) {
	now := time.Now().UTC()
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO mqtt_users(username, password_hash, disabled, created_at, updated_at) VALUES(?, ?, 0, ?, ?)`,
		strings.TrimSpace(params.Username),
		params.PasswordHash,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return MQTTUser{}, fmt.Errorf("create mqtt user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return MQTTUser{}, fmt.Errorf("get created mqtt user id: %w", err)
	}

	return s.GetMQTTUser(ctx, id)
}

// GetMQTTUser returns an MQTT user by ID.
func (s *Store) GetMQTTUser(ctx context.Context, id int64) (MQTTUser, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, disabled, created_at, updated_at FROM mqtt_users WHERE id = ?`, id)
	user, err := scanMQTTUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MQTTUser{}, ErrMQTTUserNotFound
	}
	if err != nil {
		return MQTTUser{}, fmt.Errorf("query mqtt user: %w", err)
	}
	return user, nil
}

// GetMQTTUserByUsername returns an MQTT user by username.
func (s *Store) GetMQTTUserByUsername(ctx context.Context, username string) (MQTTUser, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, disabled, created_at, updated_at FROM mqtt_users WHERE username = ?`, strings.TrimSpace(username))
	user, err := scanMQTTUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return MQTTUser{}, ErrMQTTUserNotFound
	}
	if err != nil {
		return MQTTUser{}, fmt.Errorf("query mqtt user by username: %w", err)
	}
	return user, nil
}

// ListMQTTUsers returns all MQTT users ordered by username ascending.
// Returns nil when no users exist.
func (s *Store) ListMQTTUsers(ctx context.Context) ([]MQTTUser, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, username, password_hash, disabled, created_at, updated_at FROM mqtt_users ORDER BY username ASC`)
	if err != nil {
		return nil, fmt.Errorf("list mqtt users: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var users []MQTTUser
	for rows.Next() {
		var user MQTTUser
		var disabled int
		var createdAt string
		var updatedAt string
		if err := rows.Scan(&user.ID, &user.Username, &user.PasswordHash, &disabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan mqtt user: %w", err)
		}
		user.Disabled = disabled == 1
		user.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse mqtt user created_at: %w", err)
		}
		user.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse mqtt user updated_at: %w", err)
		}
		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate mqtt users: %w", err)
	}
	return users, nil
}

// UpdateMQTTUser applies partial updates to an MQTT user.
// Only non-nil fields in params are changed; updated_at is always refreshed.
func (s *Store) UpdateMQTTUser(ctx context.Context, id int64, params UpdateMQTTUserParams) (MQTTUser, error) {
	current, err := s.GetMQTTUser(ctx, id)
	if err != nil {
		return MQTTUser{}, err
	}

	username := current.Username
	if params.Username != nil {
		username = strings.TrimSpace(*params.Username)
	}
	passwordHash := current.PasswordHash
	if params.PasswordHash != nil {
		passwordHash = *params.PasswordHash
	}
	disabled := current.Disabled
	if params.Disabled != nil {
		disabled = *params.Disabled
	}

	if _, err := s.db.ExecContext(
		ctx,
		`UPDATE mqtt_users SET username = ?, password_hash = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		username,
		passwordHash,
		boolToInt(disabled),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	); err != nil {
		return MQTTUser{}, fmt.Errorf("update mqtt user: %w", err)
	}

	return s.GetMQTTUser(ctx, id)
}

// DeleteMQTTUser removes an MQTT user by ID.
func (s *Store) DeleteMQTTUser(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM mqtt_users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete mqtt user: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted mqtt user row count: %w", err)
	}
	if affected == 0 {
		return ErrMQTTUserNotFound
	}
	return nil
}

// scanMQTTUser scans a single mqtt_users row into an MQTTUser.
// It converts the disabled integer to bool and parses RFC3339Nano timestamp strings.
func scanMQTTUser(row *sql.Row) (MQTTUser, error) {
	var user MQTTUser
	var disabled int
	var createdAt string
	var updatedAt string

	if err := row.Scan(&user.ID, &user.Username, &user.PasswordHash, &disabled, &createdAt, &updatedAt); err != nil {
		return MQTTUser{}, err
	}

	user.Disabled = disabled == 1

	var err error
	user.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return MQTTUser{}, fmt.Errorf("parse mqtt user created_at: %w", err)
	}
	user.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return MQTTUser{}, fmt.Errorf("parse mqtt user updated_at: %w", err)
	}

	return user, nil
}
