package storage

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

func TestACLStorePersistsRulesAcrossReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "mcm.db")
	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}

	aclStore := store.ACLStore()
	ctx := context.Background()

	created, err := aclStore.CreateRule(ctx, acl.Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  acl.PermissionRead,
		Description: "read access for alice",
	})
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("CreateRule did not assign an ID")
	}

	second, err := aclStore.CreateRule(ctx, acl.Rule{
		Principal:   "bob",
		TopicFilter: "factory/#",
		Permission:  acl.PermissionWrite,
	})
	if err != nil {
		t.Fatalf("CreateRule second returned error: %v", err)
	}

	updated, err := aclStore.UpdateRule(ctx, created.ID, acl.Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/humidity",
		Permission:  acl.PermissionReadWrite,
		Description: "promoted to readwrite",
	})
	if err != nil {
		t.Fatalf("UpdateRule returned error: %v", err)
	}
	if updated.ID != created.ID || updated.TopicFilter != "sensors/+/humidity" || updated.Permission != acl.PermissionReadWrite {
		t.Fatalf("unexpected updated rule: %#v", updated)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopened, err := Open(dbPath)
	if err != nil {
		t.Fatalf("reopen Open returned error: %v", err)
	}
	defer reopened.Close()

	aclStore = reopened.ACLStore()
	rules, err := aclStore.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules after reopen returned error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("got %d rules after reopen, want 2", len(rules))
	}

	if rules[0].ID != created.ID || rules[0].Principal != "alice" || rules[0].TopicFilter != "sensors/+/humidity" || rules[0].Permission != acl.PermissionReadWrite || rules[0].Description != "promoted to readwrite" {
		t.Fatalf("rule[0] after reopen = %#v, want updated alice rule", rules[0])
	}
	if rules[1].ID != second.ID || rules[1].Principal != "bob" || rules[1].TopicFilter != "factory/#" || rules[1].Permission != acl.PermissionWrite {
		t.Fatalf("rule[1] after reopen = %#v, want bob rule", rules[1])
	}

	if err := aclStore.DeleteRule(ctx, second.ID); err != nil {
		t.Fatalf("DeleteRule returned error: %v", err)
	}

	if err := aclStore.DeleteRule(ctx, second.ID); !errors.Is(err, acl.ErrRuleNotFound) {
		t.Fatalf("DeleteRule of removed rule returned %v, want acl.ErrRuleNotFound", err)
	}

	if err := reopened.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}

	final, err := Open(dbPath)
	if err != nil {
		t.Fatalf("third Open returned error: %v", err)
	}
	defer final.Close()

	rules, err = final.ACLStore().ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules after delete reopen returned error: %v", err)
	}
	if len(rules) != 1 || rules[0].ID != created.ID {
		t.Fatalf("rules after delete reopen = %#v, want only alice rule", rules)
	}
}

func TestACLStoreReturnsErrRuleNotFound(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	aclStore := store.ACLStore()
	ctx := context.Background()

	valid := acl.Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  acl.PermissionRead,
	}

	if _, err := aclStore.UpdateRule(ctx, "999", valid); !errors.Is(err, acl.ErrRuleNotFound) {
		t.Fatalf("UpdateRule missing id = %v, want acl.ErrRuleNotFound", err)
	}
	if _, err := aclStore.UpdateRule(ctx, "not-a-number", valid); !errors.Is(err, acl.ErrRuleNotFound) {
		t.Fatalf("UpdateRule non-numeric id = %v, want acl.ErrRuleNotFound", err)
	}
	if err := aclStore.DeleteRule(ctx, "999"); !errors.Is(err, acl.ErrRuleNotFound) {
		t.Fatalf("DeleteRule missing id = %v, want acl.ErrRuleNotFound", err)
	}
	if err := aclStore.DeleteRule(ctx, "not-a-number"); !errors.Is(err, acl.ErrRuleNotFound) {
		t.Fatalf("DeleteRule non-numeric id = %v, want acl.ErrRuleNotFound", err)
	}
}

func TestACLStoreValidatesOnCreateAndUpdate(t *testing.T) {
	store := newTestStore(t)
	defer store.Close()

	aclStore := store.ACLStore()
	ctx := context.Background()

	invalid := acl.Rule{Principal: "", TopicFilter: "sensors/+/temperature", Permission: acl.PermissionRead}
	if _, err := aclStore.CreateRule(ctx, invalid); err == nil {
		t.Fatal("CreateRule accepted invalid rule")
	} else {
		var validationErr *acl.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("CreateRule error = %v, want *acl.ValidationError", err)
		}
	}

	created, err := aclStore.CreateRule(ctx, acl.Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  acl.PermissionRead,
	})
	if err != nil {
		t.Fatalf("CreateRule returned error: %v", err)
	}

	if _, err := aclStore.UpdateRule(ctx, created.ID, invalid); err == nil {
		t.Fatal("UpdateRule accepted invalid rule")
	} else {
		var validationErr *acl.ValidationError
		if !errors.As(err, &validationErr) {
			t.Fatalf("UpdateRule error = %v, want *acl.ValidationError", err)
		}
	}
}
