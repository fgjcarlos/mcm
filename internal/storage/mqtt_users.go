package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// MQTTUserValidationError holds validation failures for an MQTT username.
type MQTTUserValidationError struct {
	Problems []string
}

func (e *MQTTUserValidationError) Error() string {
	return fmt.Sprintf("mqtt user validation failed: %s", strings.Join(e.Problems, "; "))
}

// ValidateMQTTUsername validates the value before it is trimmed or persisted.
// Mosquitto passwd files are line-oriented, so control characters cannot be
// accepted even though ordinary MQTT client-id/username syntax remains valid.
func ValidateMQTTUsername(username string) error {
	var problems []string
	if strings.TrimSpace(username) == "" {
		problems = append(problems, "username is required")
	}
	for _, r := range username {
		if unicode.IsControl(r) {
			problems = append(problems, "username must not contain control characters")
			break
		}
	}
	if len(problems) > 0 {
		return &MQTTUserValidationError{Problems: problems}
	}
	return nil
}

// CreateMQTTUser creates a new MQTT user.
func (s *Store) CreateMQTTUser(ctx context.Context, params CreateMQTTUserParams) (MQTTUser, error) {
	if err := ValidateMQTTUsername(params.Username); err != nil {
		return MQTTUser{}, err
	}
	username := strings.TrimSpace(params.Username)
	if reserved := strings.TrimSpace(params.ServiceReserved); reserved != "" && username == reserved {
		return MQTTUser{}, ErrMQTTUserServiceReserved
	}
	s.LockMutations()
	defer s.UnlockMutations()
	now := time.Now().UTC()
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO mqtt_users(username, password_hash, disabled, created_at, updated_at) VALUES(?, ?, 0, ?, ?)`,
		username,
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
	if params.Username != nil {
		if err := ValidateMQTTUsername(*params.Username); err != nil {
			return MQTTUser{}, err
		}
	}
	s.LockMutations()
	defer s.UnlockMutations()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MQTTUser{}, fmt.Errorf("begin update mqtt user transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		oldUsername  string
		passwordHash string
		disabledInt  int
	)
	if err := tx.QueryRowContext(ctx,
		`SELECT username, password_hash, disabled FROM mqtt_users WHERE id = ?`, id,
	).Scan(&oldUsername, &passwordHash, &disabledInt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return MQTTUser{}, ErrMQTTUserNotFound
		}
		return MQTTUser{}, fmt.Errorf("lookup mqtt user for update: %w", err)
	}

	username := oldUsername
	if params.Username != nil {
		newName := strings.TrimSpace(*params.Username)
		if reserved := strings.TrimSpace(params.ServiceReserved); reserved != "" && newName == reserved {
			return MQTTUser{}, ErrMQTTUserServiceReserved
		}
		username = newName
	}
	if params.PasswordHash != nil {
		passwordHash = *params.PasswordHash
	}
	disabled := disabledInt == 1
	if params.Disabled != nil {
		disabled = *params.Disabled
		if disabled && strings.TrimSpace(params.ServiceReserved) != "" && oldUsername == strings.TrimSpace(params.ServiceReserved) {
			return MQTTUser{}, ErrMQTTUserServiceReserved
		}
	}

	if _, err := tx.ExecContext(
		ctx,
		`UPDATE mqtt_users SET username = ?, password_hash = ?, disabled = ?, updated_at = ? WHERE id = ?`,
		username,
		passwordHash,
		boolToInt(disabled),
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	); err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return MQTTUser{}, ErrMQTTUserConflict
		}
		return MQTTUser{}, fmt.Errorf("update mqtt user: %w", err)
	}

	if username != oldUsername {
		if _, err := tx.ExecContext(ctx,
			`UPDATE acl_rules SET principal = ?, updated_at = ? WHERE principal = ?`,
			username,
			time.Now().UTC().Format(time.RFC3339Nano),
			oldUsername,
		); err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				return MQTTUser{}, ErrMQTTUserConflict
			}
			return MQTTUser{}, fmt.Errorf("cascade rename to acl_rules: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return MQTTUser{}, fmt.Errorf("commit update mqtt user transaction: %w", err)
	}

	return s.GetMQTTUser(ctx, id)
}

// DeleteMQTTUser removes an MQTT user by ID.
func (s *Store) DeleteMQTTUser(ctx context.Context, id int64) error {
	s.LockMutations()
	defer s.UnlockMutations()

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

// DeleteMQTTUserByUsername removes an MQTT user by username lookup.
// The deploy service consumes usernames (not IDs) when reconciling the
// rendered output, so the storage layer exposes a helper for that path
// (issue #297).
func (s *Store) DeleteMQTTUserByUsername(ctx context.Context, username string) error {
	s.LockMutations()
	defer s.UnlockMutations()

	result, err := s.db.ExecContext(ctx, `DELETE FROM mqtt_users WHERE username = ?`, strings.TrimSpace(username))
	if err != nil {
		return fmt.Errorf("delete mqtt user by username: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted mqtt user by username row count: %w", err)
	}
	if affected == 0 {
		return ErrMQTTUserNotFound
	}
	return nil
}

// RenameMQTTUser updates the username of an MQTT user and cascades the new
// value into acl_rules.principal inside a single SQLite transaction so a
// failed rename never leaves an orphan ACL rule behind (issue #297).
func (s *Store) RenameMQTTUser(ctx context.Context, id int64, newUsername string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return fmt.Errorf("rename mqtt user: username is required")
	}
	_, err := s.UpdateMQTTUser(ctx, id, UpdateMQTTUserParams{Username: &newUsername})
	return err
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
