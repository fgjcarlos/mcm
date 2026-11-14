package storage

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
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

// TestACLListRulesPaged runs the same cases against the SQL store and the
// in-memory store so both implement identical filtering, ordering and paging.
func TestACLListRulesPaged(t *testing.T) {
	seed := []acl.Rule{
		{Principal: "alice", TopicFilter: "sensors/temp", Permission: acl.PermissionRead},
		{Principal: "bob", TopicFilter: "factory/#", Permission: acl.PermissionWrite},
		{Principal: "Alice-Admin", TopicFilter: "sensors/+/humidity", Permission: acl.PermissionRead},
		{Principal: "pct%user", TopicFilter: "sensors/x", Permission: acl.PermissionRead},
		{Principal: "carol", TopicFilter: "factory/line1/#", Permission: acl.PermissionRead},
		{Principal: "alice", TopicFilter: "factory/alice", Permission: acl.PermissionReadWrite},
		{Principal: "under_score", TopicFilter: "a/b", Permission: acl.PermissionRead},
		{Principal: "underXscore", TopicFilter: "a/c", Permission: acl.PermissionRead},
		{Principal: "dev-0", TopicFilter: "d/0", Permission: acl.PermissionRead},
		{Principal: "dev-1", TopicFilter: "d/1", Permission: acl.PermissionRead},
		{Principal: "dev-2", TopicFilter: "d/2", Permission: acl.PermissionRead},
		{Principal: "dev-3", TopicFilter: "d/3", Permission: acl.PermissionRead},
	}
	all := make([]string, 0, len(seed))
	for _, r := range seed {
		all = append(all, r.Principal+" "+r.TopicFilter)
	}

	tests := []struct {
		name          string
		user, topic   string
		limit, offset int
		want          []string
		wantTotal     int
	}{
		{"empty filters match everything in id order", "", "", 50, 0, all, 12},
		{"page past id 9 keeps numeric order", "", "", 5, 10, all[10:], 12},
		{"user filter matches one rule", "bob", "", 50, 0, []string{"bob factory/#"}, 1},
		{"user filter is case-insensitive substring", "ALICE", "", 50, 0, []string{"alice sensors/temp", "Alice-Admin sensors/+/humidity", "alice factory/alice"}, 3},
		{"total counts all matches beyond the page", "alice", "", 2, 0, []string{"alice sensors/temp", "Alice-Admin sensors/+/humidity"}, 3},
		{"offset returns the next slice", "alice", "", 2, 2, []string{"alice factory/alice"}, 3},
		{"offset past the end returns no rows", "alice", "", 2, 9, []string{}, 3},
		{"topic filter matches one rule", "", "humid", 50, 0, []string{"Alice-Admin sensors/+/humidity"}, 1},
		{"topic filter matches several rules", "", "FACTORY", 50, 0, []string{"bob factory/#", "carol factory/line1/#", "alice factory/alice"}, 3},
		{"user and topic combine with AND", "alice", "factory", 50, 0, []string{"alice factory/alice"}, 1},
		{"percent in user matches literally", "%", "", 50, 0, []string{"pct%user sensors/x"}, 1},
		{"underscore in user matches literally", "under_", "", 50, 0, []string{"under_score a/b"}, 1},
		{"percent in topic is not a match-all", "", "%", 50, 0, []string{}, 0},
		{"no match returns no rows", "nobody", "", 50, 0, []string{}, 0},
	}

	ctx := context.Background()
	sqlStore := newTestStore(t)
	defer sqlStore.Close()
	stores := map[string]acl.Store{"sql": sqlStore.ACLStore(), "memory": acl.NewMemoryStore()}

	for storeName, store := range stores {
		for _, r := range seed {
			if _, err := store.CreateRule(ctx, r); err != nil {
				t.Fatalf("%s CreateRule(%q) returned error: %v", storeName, r.Principal, err)
			}
		}
		for _, tt := range tests {
			t.Run(storeName+"/"+tt.name, func(t *testing.T) {
				rules, total, err := store.ListRulesPaged(ctx, tt.user, tt.topic, tt.limit, tt.offset)
				if err != nil {
					t.Fatalf("ListRulesPaged returned error: %v", err)
				}
				got := make([]string, 0, len(rules))
				for _, r := range rules {
					got = append(got, r.Principal+" "+r.TopicFilter)
				}
				if total != tt.wantTotal {
					t.Errorf("total = %d, want %d", total, tt.wantTotal)
				}
				if !slices.Equal(got, tt.want) {
					t.Errorf("rules = %q, want %q", got, tt.want)
				}
			})
		}
		// The full listing is unchanged by the paged one.
		full, err := store.ListRules(ctx)
		if err != nil || len(full) != len(seed) {
			t.Fatalf("%s ListRules = %d rules, %v; want %d", storeName, len(full), err, len(seed))
		}
	}
}
