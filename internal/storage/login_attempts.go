package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LoginAttemptParams captures one observed admin login attempt.
type LoginAttemptParams struct {
	Username    string
	SourceIP    string
	Success     bool
	AttemptedAt time.Time
}

// LoginAttemptStats summarizes failed login attempts for rate limiting decisions.
type LoginAttemptStats struct {
	Count       int
	OldestAt    time.Time
}

// RecordLoginAttempt stores the outcome of a login attempt.
func (s *Store) RecordLoginAttempt(ctx context.Context, params LoginAttemptParams) error {
	attemptedAt := params.AttemptedAt.UTC()
	if attemptedAt.IsZero() {
		attemptedAt = time.Now().UTC()
	}
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO login_attempts(username, source_ip, success, attempted_at)
VALUES(?, ?, ?, ?)`,
		strings.TrimSpace(params.Username),
		strings.TrimSpace(params.SourceIP),
		boolToInt(params.Success),
		attemptedAt.Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("record login attempt: %w", err)
	}
	return nil
}

// CountFailedLoginAttemptsByIP returns the count and oldest timestamp of failed
// login attempts from sourceIP at or after since. The source IP is treated
// case-sensitively; callers should canonicalize beforehand. An empty sourceIP
// returns zero results.
func (s *Store) CountFailedLoginAttemptsByIP(ctx context.Context, sourceIP string, since time.Time) (LoginAttemptStats, error) {
	ip := strings.TrimSpace(sourceIP)
	if ip == "" {
		return LoginAttemptStats{}, nil
	}
	return s.failedLoginAttemptStats(ctx, `source_ip = ?`, ip, since)
}

// CountFailedLoginAttemptsByUsername returns the count and oldest timestamp of
// failed login attempts for username at or after since. An empty username
// returns zero results.
func (s *Store) CountFailedLoginAttemptsByUsername(ctx context.Context, username string, since time.Time) (LoginAttemptStats, error) {
	name := strings.TrimSpace(username)
	if name == "" {
		return LoginAttemptStats{}, nil
	}
	return s.failedLoginAttemptStats(ctx, `username = ?`, name, since)
}

func (s *Store) failedLoginAttemptStats(ctx context.Context, condition string, value string, since time.Time) (LoginAttemptStats, error) {
	var count int
	var oldest sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(attempted_at) FROM login_attempts WHERE success = 0 AND `+condition+` AND attempted_at >= ?`,
		value,
		since.UTC().Format(time.RFC3339Nano),
	).Scan(&count, &oldest)
	if err != nil {
		return LoginAttemptStats{}, fmt.Errorf("count failed login attempts: %w", err)
	}
	stats := LoginAttemptStats{Count: count}
	if oldest.Valid && oldest.String != "" {
		oldestAt, parseErr := time.Parse(time.RFC3339Nano, oldest.String)
		if parseErr != nil {
			return LoginAttemptStats{}, fmt.Errorf("parse oldest login attempt timestamp: %w", parseErr)
		}
		stats.OldestAt = oldestAt
	}
	return stats, nil
}

// PruneLoginAttempts deletes attempts older than cutoff and returns how many rows were removed.
func (s *Store) PruneLoginAttempts(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM login_attempts WHERE attempted_at < ?`, cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("prune login attempts: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count pruned login attempts: %w", err)
	}
	return affected, nil
}

// ResetFailedLoginAttempts removes the failure history for the matching username
// and/or sourceIP. Empty arguments are ignored. Returns rows deleted.
func (s *Store) ResetFailedLoginAttempts(ctx context.Context, username string, sourceIP string) (int64, error) {
	username = strings.TrimSpace(username)
	sourceIP = strings.TrimSpace(sourceIP)
	if username == "" && sourceIP == "" {
		return 0, nil
	}

	var (
		clauses []string
		args    []any
	)
	clauses = append(clauses, "success = 0")
	if username != "" {
		clauses = append(clauses, "username = ?")
		args = append(args, username)
	}
	if sourceIP != "" {
		clauses = append(clauses, "source_ip = ?")
		args = append(args, sourceIP)
	}

	// Use OR semantics across identifiers when both are provided so a successful
	// login clears both the username and IP buckets.
	query := `DELETE FROM login_attempts WHERE success = 0 AND (`
	identifierClauses := clauses[1:]
	query += strings.Join(identifierClauses, " OR ") + `)`

	result, err := s.db.ExecContext(ctx, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reset failed login attempts: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count reset login attempts: %w", err)
	}
	return affected, nil
}
