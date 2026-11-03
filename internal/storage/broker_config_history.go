package storage

import (
	"context"
)

// BrokerConfigRevisionSummary never exposes rendered config or credentials.
// ConsumedAt is an apply attempt, not proof of activation; Status comes from
// the deployment bound to this exact revision, never a hash-based guess.
type BrokerConfigRevisionSummary struct {
	ID           string `json:"id"`
	Actor        string `json:"actor"`
	Hash         string `json:"hash"`
	CreatedAt    string `json:"created_at"`
	ConsumedAt   string `json:"consumed_at,omitempty"`
	Status       string `json:"status"`
	DeploymentID int64  `json:"deployment_id,omitempty"`
	Message      string `json:"message,omitempty"`
}

func (s *Store) ListBrokerConfigRevisions(ctx context.Context) ([]BrokerConfigRevisionSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.id, r.actor, r.rendered_conf_hash, r.created_at, r.applied_at, COALESCE(d.status,'preview'), COALESCE(d.id,0), COALESCE(d.message,'') FROM broker_config_revisions r LEFT JOIN deployments d ON d.revision_id = r.id ORDER BY r.created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close() //nolint:errcheck
	result := make([]BrokerConfigRevisionSummary, 0)
	for rows.Next() {
		var r BrokerConfigRevisionSummary
		if err := rows.Scan(&r.ID, &r.Actor, &r.Hash, &r.CreatedAt, &r.ConsumedAt, &r.Status, &r.DeploymentID, &r.Message); err != nil {
			return nil, err
		}
		result = append(result, r)
	}
	return result, rows.Err()
}
