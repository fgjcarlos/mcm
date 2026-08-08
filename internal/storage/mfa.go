package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrRecoveryCodeNotFound is returned when a recovery code lookup fails (unknown or already used).
var ErrRecoveryCodeNotFound = errors.New("recovery code not found")

// ErrTOTPCodeReused is returned when a TOTP time-step has already been accepted for the user.
var ErrTOTPCodeReused = errors.New("totp code already used")

// SetAdminUserMFA replaces the MFA secret and enabled flag for a user. Used both
// when enrolling (enabled=true) and when disabling (secret="" enabled=false).
func (s *Store) SetAdminUserMFA(ctx context.Context, userID int64, secret string, enabled bool) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := `UPDATE admin_users SET mfa_secret = ?, mfa_enabled = ?, updated_at = ? WHERE id = ?`
	args := []any{strings.TrimSpace(secret), boolToInt(enabled), now, userID}
	if !enabled {
		query = `UPDATE admin_users SET mfa_secret = ?, mfa_enabled = ?, mfa_last_totp_step = -1, updated_at = ? WHERE id = ?`
	}
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("set admin user mfa: %w", err)
	}
	return nil
}

// ConsumeTOTPTimeStep records an accepted TOTP time-step for the user.
// The update is monotonic and race-safe: replaying the same or an older step is rejected.
func (s *Store) ConsumeTOTPTimeStep(ctx context.Context, userID int64, step int64) error {
	if userID < 1 || step < 0 {
		return ErrTOTPCodeReused
	}
	result, err := s.db.ExecContext(ctx,
		`UPDATE admin_users SET mfa_last_totp_step = ?, updated_at = ? WHERE id = ? AND mfa_last_totp_step < ?`,
		step,
		time.Now().UTC().Format(time.RFC3339Nano),
		userID,
		step,
	)
	if err != nil {
		return fmt.Errorf("consume totp time step: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("totp time step row count: %w", err)
	}
	if affected == 0 {
		return ErrTOTPCodeReused
	}
	return nil
}

// ReplaceRecoveryCodes deletes any existing recovery codes for the user and inserts the
// provided hashes in a single transaction so a partially-rotated state is impossible.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID int64, codeHashes []string) error {
	if userID < 1 {
		return fmt.Errorf("invalid admin user id")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin recovery codes transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, `DELETE FROM admin_mfa_recovery_codes WHERE admin_user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete existing recovery codes: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, hash := range codeHashes {
		if _, err = tx.ExecContext(ctx,
			`INSERT INTO admin_mfa_recovery_codes(admin_user_id, code_hash, used, created_at) VALUES(?, ?, 0, ?)`,
			userID, hash, now,
		); err != nil {
			return fmt.Errorf("insert recovery code: %w", err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit recovery codes: %w", err)
	}
	return nil
}

// UnusedRecoveryCodeHashes returns the hashes of recovery codes still available for the user.
// Callers compare each hash to the operator-provided code rather than reversing it.
func (s *Store) UnusedRecoveryCodeHashes(ctx context.Context, userID int64) ([]struct {
	ID   int64
	Hash string
}, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, code_hash FROM admin_mfa_recovery_codes WHERE admin_user_id = ? AND used = 0 ORDER BY id ASC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("query unused recovery codes: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	var out []struct {
		ID   int64
		Hash string
	}
	for rows.Next() {
		var rec struct {
			ID   int64
			Hash string
		}
		if err := rows.Scan(&rec.ID, &rec.Hash); err != nil {
			return nil, fmt.Errorf("scan recovery code: %w", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recovery codes: %w", err)
	}
	return out, nil
}

// ConsumeRecoveryCode marks the recovery code with the given id as used inside a transaction.
// Returns ErrRecoveryCodeNotFound if the id no longer corresponds to an unused code (race-safe).
func (s *Store) ConsumeRecoveryCode(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx,
		`UPDATE admin_mfa_recovery_codes SET used = 1, used_at = ? WHERE id = ? AND used = 0`,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return fmt.Errorf("consume recovery code: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("recovery code row count: %w", err)
	}
	if affected == 0 {
		return ErrRecoveryCodeNotFound
	}
	return nil
}

// DeleteRecoveryCodes removes every recovery code for the user. Used when disabling MFA.
func (s *Store) DeleteRecoveryCodes(ctx context.Context, userID int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM admin_mfa_recovery_codes WHERE admin_user_id = ?`, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("delete recovery codes: %w", err)
	}
	return nil
}
