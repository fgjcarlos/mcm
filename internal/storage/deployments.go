package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// validDeploymentStatuses is the allowed set of deployment status values.
var validDeploymentStatuses = map[string]struct{}{
	"pending":         {},
	"applied":         {},
	"failed":          {},
	"rolled_back":     {},
	"rollback_failed": {},
}

// ErrDeploymentNotFound is returned when a deployment record does not exist.
var ErrDeploymentNotFound = errors.New("deployment not found")

// Deployment is a stored deployment lifecycle record.
type Deployment struct {
	ID             int64     `json:"id"`
	Actor          string    `json:"actor"`
	Status         string    `json:"status"`
	ACLSnapshot    string    `json:"-"`
	PasswdSnapshot string    `json:"-"`
	ACLRendered    string    `json:"-"`
	PasswdRendered string    `json:"-"`
	Message        string    `json:"message,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// InsertDeployment stores a new deployment record and returns it with its assigned ID.
func (s *Store) InsertDeployment(ctx context.Context, d *Deployment) error {
	if _, ok := validDeploymentStatuses[d.Status]; !ok {
		return fmt.Errorf("invalid deployment status %q", d.Status)
	}
	now := time.Now().UTC()
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO deployments(actor, status, acl_snapshot, passwd_snapshot, acl_rendered, passwd_rendered, message, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.Actor,
		d.Status,
		d.ACLSnapshot,
		d.PasswdSnapshot,
		d.ACLRendered,
		d.PasswdRendered,
		d.Message,
		now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert deployment: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get deployment id: %w", err)
	}

	got, err := s.GetDeployment(ctx, id)
	if err != nil {
		return err
	}
	*d = got
	return nil
}

// GetDeployment returns a deployment record by ID.
func (s *Store) GetDeployment(ctx context.Context, id int64) (Deployment, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, actor, status, acl_snapshot, passwd_snapshot, acl_rendered, passwd_rendered, message, created_at, updated_at
		 FROM deployments WHERE id = ?`, id)
	d, err := scanDeployment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrDeploymentNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("query deployment: %w", err)
	}
	return d, nil
}

// UpdateDeploymentStatus updates the status and message of an existing deployment.
func (s *Store) UpdateDeploymentStatus(ctx context.Context, id int64, status, message string) error {
	if _, ok := validDeploymentStatuses[status]; !ok {
		return fmt.Errorf("invalid deployment status %q", status)
	}

	result, err := s.db.ExecContext(ctx,
		`UPDATE deployments SET status = ?, message = ?, updated_at = ? WHERE id = ?`,
		status,
		message,
		time.Now().UTC().Format(time.RFC3339Nano),
		id,
	)
	if err != nil {
		return fmt.Errorf("update deployment status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get updated deployment row count: %w", err)
	}
	if affected == 0 {
		return ErrDeploymentNotFound
	}
	return nil
}

// ListDeployments returns deployment records ordered newest first with limit/offset pagination.
// Returns an empty slice (not nil) when no records exist.
func (s *Store) ListDeployments(ctx context.Context, limit, offset int) ([]Deployment, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT id, actor, status, acl_snapshot, passwd_snapshot, acl_rendered, passwd_rendered, message, created_at, updated_at
		 FROM deployments ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`,
		limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list deployments: %w", err)
	}
	defer rows.Close()

	deployments := make([]Deployment, 0)
	for rows.Next() {
		d, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		deployments = append(deployments, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployments: %w", err)
	}
	return deployments, nil
}

func scanDeployment(row interface{ Scan(dest ...any) error }) (Deployment, error) {
	var d Deployment
	var createdAt string
	var updatedAt string

	if err := row.Scan(
		&d.ID,
		&d.Actor,
		&d.Status,
		&d.ACLSnapshot,
		&d.PasswdSnapshot,
		&d.ACLRendered,
		&d.PasswdRendered,
		&d.Message,
		&createdAt,
		&updatedAt,
	); err != nil {
		return Deployment{}, err
	}

	var err error
	d.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Deployment{}, fmt.Errorf("parse deployment created_at: %w", err)
	}
	d.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Deployment{}, fmt.Errorf("parse deployment updated_at: %w", err)
	}

	return d, nil
}
