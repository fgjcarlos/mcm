package acl_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// RunACLRulesContractTests is a shared helper function that tests the full ACL Store contract.
// It is exported so that other Store implementations (e.g., the SQLite store in
// internal/storage) can also call it from their own *_test.go files to verify
// contract compliance against the same scenarios.
//
// The name deliberately starts with "Run" (not "Test") so that the Go testing
// framework does not try to invoke it as a test function — it accepts a store
// argument, so it cannot satisfy the standard `func TestXxx(t *testing.T)`
// signature and would otherwise be flagged as a setup failure.
//
// Usage:
//
//	func TestStorageACLStoreContract(t *testing.T) {
//	    store := store.ACLStore()
//	    acl.RunACLRulesContractTests(t, store)
//	}
//
// The helper uses the provided store argument directly (no internal store creation),
// so sub-tests exercise only the implementation passed in.
func RunACLRulesContractTests(t *testing.T, store acl.Store) {
	ctx := context.Background()

	t.Run("create assigns ID", func(t *testing.T) {
		r := acl.Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  acl.PermissionRead,
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
		// Create a fresh store for this sub-test
		freshStore := acl.NewMemoryStore()

		r1 := acl.Rule{
			Principal:   "user1",
			TopicFilter: "topic1",
			Permission:  acl.PermissionRead,
		}
		r2 := acl.Rule{
			Principal:   "user2",
			TopicFilter: "topic2",
			Permission:  acl.PermissionWrite,
		}
		r3 := acl.Rule{
			Principal:   "user3",
			TopicFilter: "topic3",
			Permission:  acl.PermissionReadWrite,
		}

		created1, err := freshStore.CreateRule(ctx, r1)
		if err != nil {
			t.Fatalf("CreateRule 1 returned error: %v", err)
		}

		created2, err := freshStore.CreateRule(ctx, r2)
		if err != nil {
			t.Fatalf("CreateRule 2 returned error: %v", err)
		}

		created3, err := freshStore.CreateRule(ctx, r3)
		if err != nil {
			t.Fatalf("CreateRule 3 returned error: %v", err)
		}

		rules, err := freshStore.ListRules(ctx)
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
		freshStore := acl.NewMemoryStore()

		r := acl.Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  acl.PermissionRead,
			Description: "original",
		}

		created, err := freshStore.CreateRule(ctx, r)
		if err != nil {
			t.Fatalf("CreateRule returned error: %v", err)
		}

		updated := acl.Rule{
			Principal:   "bob",
			TopicFilter: "factory/#",
			Permission:  acl.PermissionWrite,
			Description: "updated",
		}

		result, err := freshStore.UpdateRule(ctx, created.ID, updated)
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
		if result.Permission != acl.PermissionWrite {
			t.Errorf("Permission not updated: %q", result.Permission)
		}
		if result.Description != "updated" {
			t.Errorf("Description not updated: %q", result.Description)
		}
	})

	t.Run("update missing ID returns ErrRuleNotFound", func(t *testing.T) {
		freshStore := acl.NewMemoryStore()

		r := acl.Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  acl.PermissionRead,
		}

		_, err := freshStore.UpdateRule(ctx, "999", r)
		if !errors.Is(err, acl.ErrRuleNotFound) {
			t.Errorf("UpdateRule missing ID returned %v, want ErrRuleNotFound", err)
		}
	})

	t.Run("delete existing succeeds", func(t *testing.T) {
		freshStore := acl.NewMemoryStore()

		r := acl.Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  acl.PermissionRead,
		}

		created, err := freshStore.CreateRule(ctx, r)
		if err != nil {
			t.Fatalf("CreateRule returned error: %v", err)
		}

		err = freshStore.DeleteRule(ctx, created.ID)
		if err != nil {
			t.Fatalf("DeleteRule returned error: %v", err)
		}

		rules, err := freshStore.ListRules(ctx)
		if err != nil {
			t.Fatalf("ListRules returned error: %v", err)
		}
		if len(rules) != 0 {
			t.Errorf("expected 0 rules after delete, got %d", len(rules))
		}
	})

	t.Run("delete missing ID returns ErrRuleNotFound", func(t *testing.T) {
		freshStore := acl.NewMemoryStore()

		err := freshStore.DeleteRule(ctx, "999")
		if !errors.Is(err, acl.ErrRuleNotFound) {
			t.Errorf("DeleteRule missing ID returned %v, want ErrRuleNotFound", err)
		}
	})

	t.Run("validation rejects empty principal", func(t *testing.T) {
		freshStore := acl.NewMemoryStore()

		r := acl.Rule{
			Principal:   "",
			TopicFilter: "sensors/+/temperature",
			Permission:  acl.PermissionRead,
		}

		_, err := freshStore.CreateRule(ctx, r)
		if err == nil {
			t.Fatal("CreateRule accepted empty principal")
		}

		var validationErr *acl.ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("expected *ValidationError, got %T", err)
		}
	})

	t.Run("validation rejects invalid permission", func(t *testing.T) {
		freshStore := acl.NewMemoryStore()

		r := acl.Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  acl.Permission("invalid"),
		}

		_, err := freshStore.CreateRule(ctx, r)
		if err == nil {
			t.Fatal("CreateRule accepted invalid permission")
		}

		var validationErr *acl.ValidationError
		if !errors.As(err, &validationErr) {
			t.Errorf("expected *ValidationError, got %T", err)
		}
	})
}

// TestMemoryStoreRunsContract verifies that MemoryStore implements the Store contract.
func TestMemoryStoreRunsContract(t *testing.T) {
	RunACLRulesContractTests(t, acl.NewMemoryStore())
}
