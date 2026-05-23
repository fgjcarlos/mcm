package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// validEdgeSiteStatuses is the allowed set of edge site status values.
var validEdgeSiteStatuses = map[string]struct{}{
	"healthy":  {},
	"degraded": {},
	"unknown":  {},
}

// ErrEdgeSiteNotFound is returned when an edge site record does not exist.
var ErrEdgeSiteNotFound = errors.New("edge site not found")

// EdgeSite is a remote MCM agent that periodically reports its health.
type EdgeSite struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Status     string    `json:"status"`
	Message    string    `json:"message,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// UpsertEdgeSite inserts or replaces an edge site record keyed on ID.
// The created_at column is only set on first insert; subsequent upserts preserve it
// by using INSERT OR REPLACE with the existing value when the row already exists.
func (s *Store) UpsertEdgeSite(ctx context.Context, site *EdgeSite) error {
	id := strings.TrimSpace(site.ID)
	if id == "" {
		return fmt.Errorf("edge site id is required")
	}
	if _, ok := validEdgeSiteStatuses[site.Status]; !ok {
		return fmt.Errorf("invalid edge site status %q: must be one of healthy, degraded, unknown", site.Status)
	}

	now := time.Now().UTC()
	lastSeenAt := site.LastSeenAt.UTC()
	if lastSeenAt.IsZero() {
		lastSeenAt = now
	}

	// Read existing created_at if the row already exists so we can preserve it.
	var existingCreatedAt string
	err := s.db.QueryRowContext(ctx, `SELECT created_at FROM edge_sites WHERE id = ?`, id).Scan(&existingCreatedAt)
	createdAt := now.Format(time.RFC3339Nano)
	if err == nil {
		// Row exists — preserve original created_at.
		createdAt = existingCreatedAt
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("query edge site created_at: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO edge_sites(id, name, version, status, message, last_seen_at, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		strings.TrimSpace(site.Name),
		strings.TrimSpace(site.Version),
		site.Status,
		strings.TrimSpace(site.Message),
		lastSeenAt.Format(time.RFC3339Nano),
		createdAt,
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("upsert edge site: %w", err)
	}

	got, err := s.GetEdgeSite(ctx, id)
	if err != nil {
		return err
	}
	*site = got
	return nil
}

// GetEdgeSite returns a single edge site by ID.
func (s *Store) GetEdgeSite(ctx context.Context, id string) (EdgeSite, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, name, version, status, message, last_seen_at, created_at, updated_at
		 FROM edge_sites WHERE id = ?`, id)
	site, err := scanEdgeSite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EdgeSite{}, ErrEdgeSiteNotFound
	}
	if err != nil {
		return EdgeSite{}, fmt.Errorf("query edge site: %w", err)
	}
	return site, nil
}

// ListEdgeSites returns all edge sites ordered by last_seen_at descending.
// Returns an empty slice (not nil) when no records exist.
func (s *Store) ListEdgeSites(ctx context.Context) ([]EdgeSite, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, version, status, message, last_seen_at, created_at, updated_at
		 FROM edge_sites ORDER BY last_seen_at DESC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list edge sites: %w", err)
	}
	defer rows.Close()

	sites := make([]EdgeSite, 0)
	for rows.Next() {
		site, err := scanEdgeSite(rows)
		if err != nil {
			return nil, err
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edge sites: %w", err)
	}
	return sites, nil
}

func scanEdgeSite(row interface{ Scan(dest ...any) error }) (EdgeSite, error) {
	var site EdgeSite
	var lastSeenAt string
	var createdAt string
	var updatedAt string

	if err := row.Scan(
		&site.ID,
		&site.Name,
		&site.Version,
		&site.Status,
		&site.Message,
		&lastSeenAt,
		&createdAt,
		&updatedAt,
	); err != nil {
		return EdgeSite{}, err
	}

	var err error
	site.LastSeenAt, err = time.Parse(time.RFC3339Nano, lastSeenAt)
	if err != nil {
		return EdgeSite{}, fmt.Errorf("parse edge site last_seen_at: %w", err)
	}
	site.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return EdgeSite{}, fmt.Errorf("parse edge site created_at: %w", err)
	}
	site.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return EdgeSite{}, fmt.Errorf("parse edge site updated_at: %w", err)
	}

	return site, nil
}
