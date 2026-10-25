package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// TestACLSelectorMigrationFromSchema20 pins the forward migration that adds
// the native selector column: existing rows keep their identity, data,
// timestamps and indexes, and legacy rows default to the user scope.
func TestACLSelectorMigrationFromSchema20(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acl20.db")
	createSchemaThroughVersion(t, path, 20)

	db, err := sql.Open("sqlite", SQLiteDSN(path))
	if err != nil {
		t.Fatalf("open fixture database: %v", err)
	}
	createdAt := "2025-01-01T00:00:00Z"
	updatedAt := "2025-01-02T00:00:00Z"
	if _, err := db.Exec(`INSERT INTO acl_rules(id, principal, topic_filter, permission, description, created_at, updated_at)
		VALUES(7, 'alice', 'sensors/#', 'read', 'legacy rule', ?, ?)`, createdAt, updatedAt); err != nil {
		t.Fatalf("insert legacy acl rule: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close fixture database: %v", err)
	}

	store := openSchemaTestStore(t, path)
	assertMigrationRecorded(t, store.db, 21, "add_acl_rules_selector")

	var (
		id          int64
		principal   string
		topicFilter string
		permission  string
		description string
		selector    string
		gotCreated  string
		gotUpdated  string
	)
	if err := store.db.QueryRow(`SELECT id, principal, topic_filter, permission, description, selector, created_at, updated_at
		FROM acl_rules WHERE id = 7`).Scan(&id, &principal, &topicFilter, &permission, &description, &selector, &gotCreated, &gotUpdated); err != nil {
		t.Fatalf("read migrated acl rule: %v", err)
	}
	if id != 7 {
		t.Fatalf("id = %d, want 7", id)
	}
	if principal != "alice" || topicFilter != "sensors/#" || permission != "read" || description != "legacy rule" {
		t.Fatalf("migrated row changed: principal=%q topic_filter=%q permission=%q description=%q", principal, topicFilter, permission, description)
	}
	if gotCreated != createdAt || gotUpdated != updatedAt {
		t.Fatalf("timestamps changed: created_at=%q updated_at=%q", gotCreated, gotUpdated)
	}
	if selector != string(acl.SelectorUser) {
		t.Fatalf("selector = %q, want %q", selector, acl.SelectorUser)
	}

	for _, index := range []string{"idx_acl_rules_principal", "idx_acl_rules_topic_filter"} {
		var name string
		if err := store.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, index).Scan(&name); err != nil {
			t.Fatalf("index %q is missing after migration: %v", index, err)
		}
	}

	// The column keeps its NOT NULL constraint and default.
	if _, err := store.db.Exec(`INSERT INTO acl_rules(principal, topic_filter, permission, description, created_at, updated_at)
		VALUES('bob', 'cmd/#', 'write', '', ?, ?)`, createdAt, updatedAt); err != nil {
		t.Fatalf("insert rule without selector: %v", err)
	}
	var insertedSelector string
	if err := store.db.QueryRow(`SELECT selector FROM acl_rules WHERE principal = 'bob'`).Scan(&insertedSelector); err != nil {
		t.Fatalf("read inserted selector: %v", err)
	}
	if insertedSelector != string(acl.SelectorUser) {
		t.Fatalf("default selector = %q, want %q", insertedSelector, acl.SelectorUser)
	}
}

// TestACLRulesNativeSelectorRoundTrip pins that the ACL CRUD API stores
// and returns the native selector of every rule kind.
func TestACLRulesNativeSelectorRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openSchemaTestStore(t, filepath.Join(t.TempDir(), "native.db"))
	rules := store.ACLStore()

	anonymous, err := rules.CreateRule(ctx, acl.Rule{
		Selector:    acl.SelectorAnonymous,
		TopicFilter: "public/#",
		Permission:  acl.PermissionRead,
	})
	if err != nil {
		t.Fatalf("CreateRule anonymous: %v", err)
	}
	if anonymous.Selector != acl.SelectorAnonymous {
		t.Fatalf("created selector = %q, want %q", anonymous.Selector, acl.SelectorAnonymous)
	}
	if anonymous.Principal != "" {
		t.Fatalf("created principal = %q, want empty", anonymous.Principal)
	}

	pattern, err := rules.CreateRule(ctx, acl.Rule{
		Selector:    acl.SelectorPattern,
		TopicFilter: "clients/%u/state",
		Permission:  acl.PermissionDeny,
	})
	if err != nil {
		t.Fatalf("CreateRule pattern: %v", err)
	}
	if pattern.Selector != acl.SelectorPattern {
		t.Fatalf("created selector = %q, want %q", pattern.Selector, acl.SelectorPattern)
	}
	if pattern.Permission != acl.PermissionDeny {
		t.Fatalf("created permission = %q, want %q", pattern.Permission, acl.PermissionDeny)
	}

	legacy, err := rules.CreateRule(ctx, acl.Rule{
		Principal:   "alice",
		TopicFilter: "sensors/#",
		Permission:  acl.PermissionRead,
	})
	if err != nil {
		t.Fatalf("CreateRule legacy: %v", err)
	}
	if legacy.Selector != acl.SelectorUser {
		t.Fatalf("created selector = %q, want %q", legacy.Selector, acl.SelectorUser)
	}

	listed, err := rules.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("ListRules returned %d rules, want 3", len(listed))
	}

	byID := make(map[string]acl.Rule, len(listed))
	for _, rule := range listed {
		byID[rule.ID] = rule
	}
	if got := byID[anonymous.ID]; got.Selector != acl.SelectorAnonymous || got.TopicFilter != "public/#" {
		t.Fatalf("stored anonymous rule = %#v", got)
	}
	if got := byID[pattern.ID]; got.Selector != acl.SelectorPattern || got.Permission != acl.PermissionDeny {
		t.Fatalf("stored pattern rule = %#v", got)
	}
	if got := byID[legacy.ID]; got.Selector != acl.SelectorUser || got.Principal != "alice" {
		t.Fatalf("stored legacy rule = %#v", got)
	}

	updated, err := rules.UpdateRule(ctx, anonymous.ID, acl.Rule{
		Selector:    acl.SelectorUser,
		Principal:   "alice",
		TopicFilter: "public/#",
		Permission:  acl.PermissionRead,
	})
	if err != nil {
		t.Fatalf("UpdateRule: %v", err)
	}
	if updated.Selector != acl.SelectorUser || updated.Principal != "alice" {
		t.Fatalf("updated rule = %#v", updated)
	}

	reloaded, err := rules.ListRules(ctx)
	if err != nil {
		t.Fatalf("ListRules after update: %v", err)
	}
	for _, rule := range reloaded {
		if rule.ID != anonymous.ID {
			continue
		}
		if rule.Selector != acl.SelectorUser || rule.Principal != "alice" {
			t.Fatalf("reloaded rule = %#v", rule)
		}
	}
}

// TestStoreCreateRuleKeepsNativeSelector pins the storage wrapper keeps
// the native selector of a rule and normalizes legacy rules to the user
// scope.
func TestStoreCreateRuleKeepsNativeSelector(t *testing.T) {
	ctx := context.Background()
	store := openSchemaTestStore(t, filepath.Join(t.TempDir(), "rows.db"))

	row, err := store.CreateRule(ctx, acl.Rule{
		Selector:    acl.SelectorPattern,
		TopicFilter: "clients/%u/state",
		Permission:  acl.PermissionDeny,
	})
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	if row.Selector != acl.SelectorPattern {
		t.Fatalf("row selector = %q, want %q", row.Selector, acl.SelectorPattern)
	}

	got, err := store.GetRule(ctx, row.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.Selector != acl.SelectorPattern {
		t.Fatalf("stored selector = %q, want %q", got.Selector, acl.SelectorPattern)
	}
	if got.Permission != acl.PermissionDeny {
		t.Fatalf("stored permission = %q, want %q", got.Permission, acl.PermissionDeny)
	}

	legacyRow, err := store.CreateRule(ctx, acl.Rule{
		Principal:   "alice",
		TopicFilter: "sensors/#",
		Permission:  acl.PermissionRead,
	})
	if err != nil {
		t.Fatalf("CreateRule legacy: %v", err)
	}
	if legacyRow.Selector != acl.SelectorUser {
		t.Fatalf("legacy row selector = %q, want %q", legacyRow.Selector, acl.SelectorUser)
	}
}

// TestFindOrphanRulesIgnoresNativeRules pins that only user-scoped rules can
// become orphans: anonymous and pattern records are not MQTT usernames.
func TestFindOrphanRulesIgnoresNativeRules(t *testing.T) {
	ctx := context.Background()
	store := openSchemaTestStore(t, filepath.Join(t.TempDir(), "orphans.db"))

	anonymousRule := acl.Rule{
		Selector:    acl.SelectorAnonymous,
		TopicFilter: "public/#",
		Permission:  acl.PermissionRead,
	}
	if _, err := store.CreateRule(ctx, anonymousRule); err != nil {
		t.Fatalf("CreateRule anonymous: %v", err)
	}
	patternRule := acl.Rule{
		Selector:    acl.SelectorPattern,
		TopicFilter: "clients/%u/state",
		Permission:  acl.PermissionRead,
	}
	if _, err := store.CreateRule(ctx, patternRule); err != nil {
		t.Fatalf("CreateRule pattern: %v", err)
	}
	userRule := acl.Rule{
		Principal:   "ghost",
		TopicFilter: "sensors/#",
		Permission:  acl.PermissionRead,
	}
	userRow, err := store.CreateRule(ctx, userRule)
	if err != nil {
		t.Fatalf("CreateRule user: %v", err)
	}

	orphans, err := store.FindOrphanRules(ctx)
	if err != nil {
		t.Fatalf("FindOrphanRules: %v", err)
	}
	if len(orphans) != 1 {
		t.Fatalf("FindOrphanRules returned %d rules, want 1: %#v", len(orphans), orphans)
	}
	if orphans[0].ID != userRow.ID {
		t.Fatalf("orphan id = %q, want %q", orphans[0].ID, userRow.ID)
	}
}

// TestACLSelectorColumnIsQueryable pins the persisted column shape so a
// stored rule can be inspected without going through the API.
func TestACLSelectorColumnIsQueryable(t *testing.T) {
	ctx := context.Background()
	store := openSchemaTestStore(t, filepath.Join(t.TempDir(), "column.db"))

	if _, err := store.CreateRule(ctx, acl.Rule{
		Selector:    acl.SelectorAnonymous,
		TopicFilter: "public/#",
		Permission:  acl.PermissionRead,
	}); err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	var selector string
	if err := store.db.QueryRowContext(ctx, `SELECT selector FROM acl_rules LIMIT 1`).Scan(&selector); err != nil {
		t.Fatalf("read selector column: %v", err)
	}
	if selector != string(acl.SelectorAnonymous) {
		t.Fatalf("selector column = %q, want %q", selector, acl.SelectorAnonymous)
	}
}
