package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// ACLRuleRow holds the storage-layer representation of an acl_rules row.
// The ID is the integer primary key surfaced as a string for compatibility
// with the existing acl.Store interface.
type ACLRuleRow struct {
	ID string `json:"id"`
	// Selector is the native Mosquitto scope of the rule. A row stored
	// before native selectors existed carries the user scope.
	Selector    acl.Selector   `json:"selector,omitempty"`
	Principal   string         `json:"principal"`
	TopicFilter string         `json:"topic_filter"`
	Permission  acl.Permission `json:"permission"`
	Description string         `json:"description,omitempty"`
}

// CreateRule persists an acl.Rule and returns the row with its assigned ID.
// It is the *Store wrapper around the acl.Store interface so the cascade
// rename and orphan-detection helpers can share a single transaction
// (issue #297).
func (s *Store) CreateRule(ctx context.Context, rule acl.Rule) (ACLRuleRow, error) {
	if err := acl.ValidateRule(rule); err != nil {
		return ACLRuleRow{}, err
	}
	rule = rule.Normalize()
	s.LockMutations()
	defer s.UnlockMutations()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO acl_rules(principal, topic_filter, permission, description, selector, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		rule.Principal,
		rule.TopicFilter,
		string(rule.Permission),
		rule.Description,
		string(rule.Selector),
		now,
		now,
	)
	if err != nil {
		return ACLRuleRow{}, fmt.Errorf("insert acl rule: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ACLRuleRow{}, fmt.Errorf("get inserted acl rule id: %w", err)
	}
	return ACLRuleRow{
		ID:          strconv.FormatInt(id, 10),
		Selector:    rule.Selector,
		Principal:   rule.Principal,
		TopicFilter: rule.TopicFilter,
		Permission:  rule.Permission,
		Description: rule.Description,
	}, nil
}

// GetRule returns a single ACL rule by string ID.
func (s *Store) GetRule(ctx context.Context, id string) (ACLRuleRow, error) {
	numericID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return ACLRuleRow{}, acl.ErrRuleNotFound
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT principal, topic_filter, permission, description, selector FROM acl_rules WHERE id = ?`,
		numericID,
	)
	var (
		principal   string
		topicFilter string
		permission  string
		description string
		selector    string
	)
	if err := row.Scan(&principal, &topicFilter, &permission, &description, &selector); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ACLRuleRow{}, acl.ErrRuleNotFound
		}
		return ACLRuleRow{}, fmt.Errorf("scan acl rule: %w", err)
	}
	return ACLRuleRow{
		ID:          id,
		Selector:    acl.Selector(selector),
		Principal:   principal,
		TopicFilter: topicFilter,
		Permission:  acl.Permission(permission),
		Description: description,
	}, nil
}

// FindOrphanRules returns every ACL rule whose principal references no
// enabled MQTT user (issue #297). Disabled and deleted users both produce
// orphan rules; the deploy service surfaces this list before rendering so
// operators can decide whether to delete or reassign the rules.
//
// Only user-scoped rules can be orphans: an anonymous or pattern rule
// carries no MQTT username, so it never references an MQTT user.
func (s *Store) FindOrphanRules(ctx context.Context) ([]ACLRuleRow, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, principal, topic_filter, permission, description, selector
FROM acl_rules
WHERE selector = ?
AND principal NOT IN (
	SELECT username FROM mqtt_users WHERE disabled = 0
)
ORDER BY id ASC`, string(acl.SelectorUser))
	if err != nil {
		return nil, fmt.Errorf("list orphan acl rules: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var out []ACLRuleRow
	for rows.Next() {
		var (
			id          int64
			principal   string
			topicFilter string
			permission  string
			description string
			selector    string
		)
		if err := rows.Scan(&id, &principal, &topicFilter, &permission, &description, &selector); err != nil {
			return nil, fmt.Errorf("scan orphan acl rule: %w", err)
		}
		out = append(out, ACLRuleRow{
			ID:          strconv.FormatInt(id, 10),
			Selector:    acl.Selector(selector),
			Principal:   principal,
			TopicFilter: topicFilter,
			Permission:  acl.Permission(permission),
			Description: description,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate orphan acl rules: %w", err)
	}
	return out, nil
}
