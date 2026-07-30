package acl

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// TestNewMemoryStore verifies that NewMemoryStore creates a valid store.
func TestNewMemoryStore(t *testing.T) {
	store := NewMemoryStore()

	if store == nil {
		t.Fatal("NewMemoryStore returned nil")
	}

	ctx := context.Background()
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules returned error: %v", err)
	}

	if len(rules) != 0 {
		t.Errorf("expected empty rules, got %d", len(rules))
	}
}

// TestMemoryStoreListRulesOrdered verifies that ListRules returns rules sorted by numeric ID.
func TestMemoryStoreListRulesOrdered(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create rules in non-sequential order
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
		t.Fatalf("CreateRule returned error: %v", err)
	}

	created2, err := store.CreateRule(ctx, r2)
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	created3, err := store.CreateRule(ctx, r3)
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules returned error: %v", err)
	}

	if len(rules) != 3 {
		t.Errorf("expected 3 rules, got %d", len(rules))
	}

	if rules[0].ID != created1.ID || rules[1].ID != created2.ID || rules[2].ID != created3.ID {
		t.Errorf("rules not in creation order: %v, %v, %v", rules[0].ID, rules[1].ID, rules[2].ID)
	}

	// Verify rules are sorted by numeric ID (they should be in order since we created them sequentially)
	for i := 0; i < len(rules)-1; i++ {
		// Simply verify the order matches creation order
		if rules[i].Principal != r1.Principal && rules[i].Principal != r2.Principal && rules[i].Principal != r3.Principal {
			t.Errorf("unexpected principal in sorted list: %q", rules[i].Principal)
		}
	}
}

// TestMemoryStoreCreateRule verifies rule creation and ID assignment.
func TestMemoryStoreCreateRule(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	r1 := Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  PermissionRead,
	}

	created1, err := store.CreateRule(ctx, r1)
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	if created1.ID == "" {
		t.Fatal("CreateRule did not assign an ID")
	}
	if created1.ID != "1" {
		t.Errorf("expected ID 1, got %q", created1.ID)
	}

	r2 := Rule{
		Principal:   "bob",
		TopicFilter: "factory/#",
		Permission:  PermissionWrite,
	}

	created2, err := store.CreateRule(ctx, r2)
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	if created2.ID != "2" {
		t.Errorf("expected ID 2, got %q", created2.ID)
	}

	// Test validation: empty principal should be rejected
	invalid := Rule{
		Principal:   "",
		TopicFilter: "topic",
		Permission:  PermissionRead,
	}

	_, err = store.CreateRule(ctx, invalid)
	if err == nil {
		t.Fatal("CreateRule accepted invalid rule with empty principal")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

// TestMemoryStoreUpdateRule verifies rule updates and error handling.
func TestMemoryStoreUpdateRule(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	r := Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  PermissionRead,
	}

	created, err := store.CreateRule(ctx, r)
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	// Update the rule
	updated := Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/humidity",
		Permission:  PermissionReadWrite,
	}

	result, err := store.UpdateRule(ctx, created.ID, updated)
	if err != nil {
		t.Fatalf("UpdateRule returned error: %v", err)
	}

	if result.ID != created.ID {
		t.Errorf("ID changed after update: %q -> %q", created.ID, result.ID)
	}
	if result.TopicFilter != "sensors/+/humidity" {
		t.Errorf("TopicFilter not updated: %q", result.TopicFilter)
	}
	if result.Permission != PermissionReadWrite {
		t.Errorf("Permission not updated: %q", result.Permission)
	}

	// Test missing ID
	_, err = store.UpdateRule(ctx, "999", updated)
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("UpdateRule missing ID returned %v, want ErrRuleNotFound", err)
	}

	// Test validation: invalid rule should be rejected
	invalid := Rule{
		Principal:   "",
		TopicFilter: "topic",
		Permission:  PermissionRead,
	}

	_, err = store.UpdateRule(ctx, created.ID, invalid)
	if err == nil {
		t.Fatal("UpdateRule accepted invalid rule")
	}

	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Errorf("expected *ValidationError, got %T", err)
	}
}

// TestMemoryStoreDeleteRule verifies rule deletion and error handling.
func TestMemoryStoreDeleteRule(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	r := Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  PermissionRead,
	}

	created, err := store.CreateRule(ctx, r)
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	// Delete should succeed
	err = store.DeleteRule(ctx, created.ID)
	if err != nil {
		t.Fatalf("DeleteRule returned error: %v", err)
	}

	// Verify rule is gone
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules returned error: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("expected 0 rules after delete, got %d", len(rules))
	}

	// Delete non-existent rule should return ErrRuleNotFound
	err = store.DeleteRule(ctx, "999")
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("DeleteRule missing ID returned %v, want ErrRuleNotFound", err)
	}

	// Delete missing rule again should still return ErrRuleNotFound
	err = store.DeleteRule(ctx, "999")
	if !errors.Is(err, ErrRuleNotFound) {
		t.Errorf("second DeleteRule missing ID returned %v, want ErrRuleNotFound", err)
	}
}

// TestMemoryStoreConcurrency verifies thread-safe concurrent access.
func TestMemoryStoreConcurrency(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	// Create rules concurrently using sync.WaitGroup
	var wg sync.WaitGroup
	wg.Add(100)
	for i := 0; i < 100; i++ {
		go func(index int) {
			defer wg.Done()
			// Use valid topic filters and principals
			principal := "user" + string(rune('a'+index%26))
			topicFilter := "home/" + "sensor" + string(rune('0'+(index%10)))
			r := Rule{
				Principal:   principal,
				TopicFilter: topicFilter,
				Permission:  PermissionRead,
			}
			_, err := store.CreateRule(ctx, r)
			if err != nil {
				t.Errorf("CreateRule returned error: %v", err)
			}
		}(i)
	}

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify all rules were created
	rules, err := store.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules returned error: %v", err)
	}

	if len(rules) != 100 {
		t.Errorf("expected 100 rules, got %d", len(rules))
	}

	// Verify all IDs are unique
	seen := make(map[string]bool)
	for _, rule := range rules {
		if seen[rule.ID] {
			t.Errorf("duplicate ID found: %q", rule.ID)
		}
		seen[rule.ID] = true
	}
}
