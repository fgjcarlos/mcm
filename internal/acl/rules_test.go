package acl

import (
	"context"
	"errors"
	"testing"
)

// testACLRules is a shared helper function that tests the full ACL Store contract.
// It can be reused by other Store implementations (e.g., SQLite store).
func testACLRules(t *testing.T, store Store) {
	ctx := context.Background()

	t.Run("create assigns ID", func(t *testing.T) {
		r := Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  PermissionRead,
		}

		created, err := store.CreateRule(ctx, r)
		if err != nil {
			t.Fatalf("CreateRule returned error: %v", err)
		}

		if created.ID == "" {
			t.Error("CreateRule did not assign an ID")
		}
	})

	t.Run("list returns sorted by ID", func(t *testing.T) {
		store := NewMemoryStore()

		r1 := Rule{
			Principal:   "user1",
			TopicFilter: "topic1",
			Permission:  PermissionRead,
		}
		r2 := Rule{
			Principal:   "user2",
			TopicFilter: "topic2",
			Permission:  PermissionWrite,
		}
		r3 := Rule{
			Principal:   "user3",
			TopicFilter: "topic3",
			Permission:  PermissionReadWrite,
		}

		created1, err := store.CreateRule(ctx, r1)
		if err != nil {
			t.Fatalf("CreateRule 1 returned error: %v", err)
		}

		created2, err := store.CreateRule(ctx, r2)
		if err != nil {
			t.Fatalf("CreateRule 2 returned error: %v", err)
		}

		created3, err := store.CreateRule(ctx, r3)
		if err != nil {
			t.Fatalf("CreateRule 3 returned error: %v", err)
		}

		rules, err := store.ListRules(ctx)
		if err != nil {
			t.Fatalf("ListRules returned error: %v", err)
		}

		if len(rules) != 3 {
			t.Errorf("expected 3 rules, got %d", len(rules))
		}

		// Verify they are sorted by ID (should match creation order)
		if rules[0].ID != created1.ID || rules[1].ID != created2.ID || rules[2].ID != created3.ID {
			t.Error("rules not sorted by ID")
		}
	})

	t.Run("update preserves ID and replaces content", func(t *testing.T) {
		store := NewMemoryStore()

		r := Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  PermissionRead,
			Description: "original",
		}

		created, err := store.CreateRule(ctx, r)
		if err != nil {
			t.Fatalf("CreateRule returned error: %v", err)
		}

		updated := Rule{
			Principal:   "bob",
			TopicFilter: "factory/#",
			Permission:  PermissionWrite,
			Description: "updated",
		}

		result, err := store.UpdateRule(ctx, created.ID, updated)
		if err != nil {
			t.Fatalf("UpdateRule returned error: %v", err)
		}

		if result.ID != created.ID {
			t.Errorf("ID changed: %q -> %q", created.ID, result.ID)
		}
		if result.Principal != "bob" {
			t.Errorf("Principal not updated: %q", result.Principal)
		}
		if result.TopicFilter != "factory/#" {
			t.Errorf("TopicFilter not updated: %q", result.TopicFilter)
		}
		if result.Permission != PermissionWrite {
			t.Errorf("Permission not updated: %q", result.Permission)
		}
		if result.Description != "updated" {
			t.Errorf("Description not updated: %q", result.Description)
		}
	})

	t.Run("update missing ID returns ErrRuleNotFound", func(t *testing.T) {
		store := NewMemoryStore()

		r := Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  PermissionRead,
		}

		_, err := store.UpdateRule(ctx, "999", r)
		if !errors.Is(err, ErrRuleNotFound) {
			t.Errorf("UpdateRule missing ID returned %v, want ErrRuleNotFound", err)
		}
	})

	t.Run("delete existing succeeds", func(t *testing.T) {
		store := NewMemoryStore()

		r := Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  PermissionRead,
		}

		created, err := store.CreateRule(ctx, r)
		if err != nil {
			t.Fatalf("CreateRule returned error: %v", err)
		}

		err = store.DeleteRule(ctx, created.ID)
		if err != nil {
			t.Fatalf("DeleteRule returned error: %v", err)
		}

		rules, err := store.ListRules(ctx)
		if err != nil {
			t.Fatalf("ListRules returned error: %v", err)
		}
		if len(rules) != 0 {
			t.Errorf("expected 0 rules after delete, got %d", len(rules))
		}
	})

	t.Run("delete missing ID returns ErrRuleNotFound", func(t *testing.T) {
		store := NewMemoryStore()

		err := store.DeleteRule(ctx, "999")
		if !errors.Is(err, ErrRuleNotFound) {
			t.Errorf("DeleteRule missing ID returned %v, want ErrRuleNotFound", err)
		}
	})

	t.Run("validation rejects empty principal", func(t *testing.T) {
		store := NewMemoryStore()

		r := Rule{
			Principal:   "",
			TopicFilter: "sensors/+/temperature",
			Permission:  PermissionRead,
		}

		_, err := store.CreateRule(ctx, r)
		if err == nil {
			t.Fatal("CreateRule accepted empty principal")
		}

		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("expected *ValidationError, got %T", err)
		}
	})

	t.Run("validation rejects invalid permission", func(t *testing.T) {
		store := NewMemoryStore()

		r := Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  Permission("invalid"),
		}

		_, err := store.CreateRule(ctx, r)
		if err == nil {
			t.Fatal("CreateRule accepted invalid permission")
		}

		var validationErr *ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("expected *ValidationError, got %T", err)
		}
	})
}

// TestMemoryStoreRunsContract verifies that MemoryStore implements the Store contract.
func TestMemoryStoreRunsContract(t *testing.T) {
	testACLRules(t, NewMemoryStore())
}
