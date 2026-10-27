package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrDynSecPlanNotFound is returned when a dynamic security plan does not
// exist.
var ErrDynSecPlanNotFound = errors.New("dynamic security plan not found")

// ErrDynSecPlanConsumed is returned when a plan was already applied.
var ErrDynSecPlanConsumed = errors.New("dynamic security plan already consumed")

// DynSecPlan stores the immutable plan of a dynamic security change.
//
// The plan is the reviewable form of a change: the operator sees the
// commands MCM is about to run before any of them happens, and the apply
// runs exactly the plan that was reviewed.
type DynSecPlan struct {
	// ID identifies the plan.
	ID string
	// Actor is the operator that requested the change.
	Actor string
	// Kind is "update" for a change of the security database and
	// "adopt" for the migration away from the file backend.
	Kind string
	// BaseHash is the fingerprint of the state the plan was computed
	// against. The apply refuses the plan when the state moved on.
	BaseHash string
	// Plan is the serialized plan.
	Plan string
	// Desired is the serialized desired state of the database.
	Desired string
	// ManagementClient is the account that administers the broker.
	ManagementClient string
	// CreatedAt is when the plan was computed.
	CreatedAt time.Time
	// AppliedAt is when the plan was applied. It is zero while the plan
	// waits.
	AppliedAt time.Time
}

// InsertDynSecPlan persists a newly generated plan.
func (s *Store) InsertDynSecPlan(ctx context.Context, plan *DynSecPlan) error {
	if plan == nil || plan.ID == "" {
		return errors.New("dynamic security plan id is required")
	}
	if plan.CreatedAt.IsZero() {
		plan.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO dynsec_plans(
	id, actor, kind, base_hash, plan, desired, management_client, created_at, applied_at
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, '')`,
		plan.ID,
		plan.Actor,
		plan.Kind,
		plan.BaseHash,
		plan.Plan,
		plan.Desired,
		plan.ManagementClient,
		plan.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("insert dynamic security plan: %w", err)
	}
	return nil
}

// GetDynSecPlan retrieves a plan by ID.
func (s *Store) GetDynSecPlan(ctx context.Context, id string) (DynSecPlan, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, actor, kind, base_hash, plan, desired, management_client, created_at, applied_at
FROM dynsec_plans WHERE id = ?`, id)
	return scanDynSecPlan(row)
}

// ConsumeDynSecPlan marks a plan as applied exactly once.
func (s *Store) ConsumeDynSecPlan(ctx context.Context, id string, appliedAt time.Time) error {
	result, err := s.db.ExecContext(ctx, `
UPDATE dynsec_plans SET applied_at = ?
WHERE id = ? AND applied_at = ''`, appliedAt.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return fmt.Errorf("consume dynamic security plan: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get consumed dynamic security plan row count: %w", err)
	}
	if affected == 1 {
		return nil
	}
	plan, getErr := s.GetDynSecPlan(ctx, id)
	if errors.Is(getErr, ErrDynSecPlanNotFound) {
		return ErrDynSecPlanNotFound
	}
	if getErr != nil {
		return getErr
	}
	if !plan.AppliedAt.IsZero() {
		return ErrDynSecPlanConsumed
	}
	return ErrDynSecPlanNotFound
}

func scanDynSecPlan(row interface{ Scan(dest ...any) error }) (DynSecPlan, error) {
	var plan DynSecPlan
	var createdAt, appliedAt string
	if err := row.Scan(
		&plan.ID,
		&plan.Actor,
		&plan.Kind,
		&plan.BaseHash,
		&plan.Plan,
		&plan.Desired,
		&plan.ManagementClient,
		&createdAt,
		&appliedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return DynSecPlan{}, ErrDynSecPlanNotFound
		}
		return DynSecPlan{}, fmt.Errorf("scan dynamic security plan: %w", err)
	}

	var err error
	plan.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return DynSecPlan{}, fmt.Errorf("parse dynamic security plan created_at: %w", err)
	}
	if appliedAt != "" {
		plan.AppliedAt, err = time.Parse(time.RFC3339Nano, appliedAt)
		if err != nil {
			return DynSecPlan{}, fmt.Errorf("parse dynamic security plan applied_at: %w", err)
		}
	}
	return plan, nil
}
