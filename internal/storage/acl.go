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

// ACLStore returns an acl.Store backed by this SQLite database.
func (s *Store) ACLStore() acl.Store {
	return &aclSQLStore{db: s.db}
}

type aclSQLStore struct {
	db *sql.DB
}

func (s *aclSQLStore) ListRules(ctx context.Context) ([]acl.Rule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, principal, topic_filter, permission, description FROM acl_rules ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list acl rules: %w", err)
	}
	defer rows.Close()

	var rules []acl.Rule
	for rows.Next() {
		rule, err := scanACLRule(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate acl rules: %w", err)
	}
	return rules, nil
}

func (s *aclSQLStore) CreateRule(ctx context.Context, rule acl.Rule) (acl.Rule, error) {
	if err := acl.ValidateRule(rule); err != nil {
		return acl.Rule{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := s.db.ExecContext(ctx, `
INSERT INTO acl_rules(principal, topic_filter, permission, description, created_at, updated_at)
VALUES(?, ?, ?, ?, ?, ?)`,
		rule.Principal,
		rule.TopicFilter,
		string(rule.Permission),
		rule.Description,
		now,
		now,
	)
	if err != nil {
		return acl.Rule{}, fmt.Errorf("insert acl rule: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return acl.Rule{}, fmt.Errorf("get acl rule id: %w", err)
	}

	rule.ID = strconv.FormatInt(id, 10)
	return rule, nil
}

func (s *aclSQLStore) UpdateRule(ctx context.Context, id string, rule acl.Rule) (acl.Rule, error) {
	if err := acl.ValidateRule(rule); err != nil {
		return acl.Rule{}, err
	}

	numericID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return acl.Rule{}, acl.ErrRuleNotFound
	}

	result, err := s.db.ExecContext(ctx, `
UPDATE acl_rules SET principal = ?, topic_filter = ?, permission = ?, description = ?, updated_at = ? WHERE id = ?`,
		rule.Principal,
		rule.TopicFilter,
		string(rule.Permission),
		rule.Description,
		time.Now().UTC().Format(time.RFC3339Nano),
		numericID,
	)
	if err != nil {
		return acl.Rule{}, fmt.Errorf("update acl rule: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return acl.Rule{}, fmt.Errorf("get updated acl rule row count: %w", err)
	}
	if affected == 0 {
		return acl.Rule{}, acl.ErrRuleNotFound
	}

	rule.ID = id
	return rule, nil
}

func (s *aclSQLStore) DeleteRule(ctx context.Context, id string) error {
	numericID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return acl.ErrRuleNotFound
	}

	result, err := s.db.ExecContext(ctx, `DELETE FROM acl_rules WHERE id = ?`, numericID)
	if err != nil {
		return fmt.Errorf("delete acl rule: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("get deleted acl rule row count: %w", err)
	}
	if affected == 0 {
		return acl.ErrRuleNotFound
	}
	return nil
}

func scanACLRule(rows interface{ Scan(dest ...any) error }) (acl.Rule, error) {
	var (
		id          int64
		principal   string
		topicFilter string
		permission  string
		description string
	)
	if err := rows.Scan(&id, &principal, &topicFilter, &permission, &description); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return acl.Rule{}, err
		}
		return acl.Rule{}, fmt.Errorf("scan acl rule: %w", err)
	}
	return acl.Rule{
		ID:          strconv.FormatInt(id, 10),
		Principal:   principal,
		TopicFilter: topicFilter,
		Permission:  acl.Permission(permission),
		Description: description,
	}, nil
}
