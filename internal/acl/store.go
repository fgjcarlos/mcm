package acl

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
)

// Store abstracts ACL persistence.
type Store interface {
	ListRules(ctx context.Context) ([]Rule, error)
	CreateRule(ctx context.Context, rule Rule) (Rule, error)
	UpdateRule(ctx context.Context, id string, rule Rule) (Rule, error)
	DeleteRule(ctx context.Context, id string) error
}

// ErrRuleNotFound is returned when a rule ID does not exist.
var ErrRuleNotFound = fmt.Errorf("acl rule not found")

// MemoryStore is an in-memory ACL store suitable for tests and the initial MVP.
type MemoryStore struct {
	mu      sync.RWMutex
	nextID  uint64
	records map[string]Rule
}

// NewMemoryStore creates an empty in-memory ACL store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nextID:  1,
		records: make(map[string]Rule),
	}
}

// ListRules returns all stored rules ordered by numeric ID.
func (s *MemoryStore) ListRules(_ context.Context) ([]Rule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rules := make([]Rule, 0, len(s.records))
	for _, rule := range s.records {
		rules = append(rules, rule)
	}

	sort.Slice(rules, func(i, j int) bool {
		left, _ := strconv.ParseUint(rules[i].ID, 10, 64)
		right, _ := strconv.ParseUint(rules[j].ID, 10, 64)
		return left < right
	})

	return rules, nil
}

// CreateRule validates and stores a new rule.
func (s *MemoryStore) CreateRule(_ context.Context, rule Rule) (Rule, error) {
	if err := ValidateRule(rule); err != nil {
		return Rule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	rule.ID = strconv.FormatUint(s.nextID, 10)
	s.nextID++
	s.records[rule.ID] = rule

	return rule, nil
}

// UpdateRule validates and replaces an existing rule.
func (s *MemoryStore) UpdateRule(_ context.Context, id string, rule Rule) (Rule, error) {
	if err := ValidateRule(rule); err != nil {
		return Rule{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[id]; !ok {
		return Rule{}, ErrRuleNotFound
	}

	rule.ID = id
	s.records[id] = rule
	return rule, nil
}

// DeleteRule removes an existing rule.
func (s *MemoryStore) DeleteRule(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.records[id]; !ok {
		return ErrRuleNotFound
	}

	delete(s.records, id)
	return nil
}
