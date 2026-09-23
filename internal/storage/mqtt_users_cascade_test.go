package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// aclRuleForPrincipal returns a Rule with the given principal and topic for
// use in storage-layer tests. The caller persists it via Store.CreateRule.
func aclRuleForPrincipal(t *testing.T, principal, topicFilter string) acl.Rule {
	t.Helper()
	return acl.Rule{
		Principal:   principal,
		TopicFilter: topicFilter,
		Permission:  acl.PermissionRead,
		Description: "",
	}
}

// TestRenameMQTTUserCascadesPrincipal ensures that renaming an MQTT user
// updates the principal column in acl_rules that referenced the old username
// so the rendered ACL keeps working after the rename (issue #297).
func TestRenameMQTTUserCascadesPrincipal(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	user, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "alice"})
	if err != nil {
		t.Fatalf("CreateMQTTUser alice: %v", err)
	}
	rule, err := store.CreateRule(ctx, aclRuleForPrincipal(t, "alice", "sensors/#"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	if err := store.RenameMQTTUser(ctx, user.ID, "alice2"); err != nil {
		t.Fatalf("RenameMQTTUser: %v", err)
	}

	got, err := store.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.Principal != "alice2" {
		t.Fatalf("principal not cascaded: got %q want %q", got.Principal, "alice2")
	}
}

// TestRenameMQTTUserConflicts ensures renaming to a username already taken
// by another user returns ErrMQTTUserConflict and leaves both the user table
// and acl_rules untouched.
func TestRenameMQTTUserConflicts(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	if _, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "alice"}); err != nil {
		t.Fatalf("CreateMQTTUser alice: %v", err)
	}
	bob, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "bob"})
	if err != nil {
		t.Fatalf("CreateMQTTUser bob: %v", err)
	}
	rule, err := store.CreateRule(ctx, aclRuleForPrincipal(t, "alice", "sensors/#"))
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}

	err = store.RenameMQTTUser(ctx, bob.ID, "alice")
	if err == nil {
		t.Fatal("expected ErrMQTTUserConflict, got nil")
	}
	if !errors.Is(err, ErrMQTTUserConflict) {
		t.Fatalf("error = %v, want ErrMQTTUserConflict", err)
	}

	got, err := store.GetRule(ctx, rule.ID)
	if err != nil {
		t.Fatalf("GetRule: %v", err)
	}
	if got.Principal != "alice" {
		t.Fatalf("conflict should leave principal untouched, got %q", got.Principal)
	}
}

// TestFindOrphanRules returns the rules whose principal references no
// enabled MQTT user. The deploy service uses this list to warn operators
// that the next render() will silently drop those rules.
func TestFindOrphanRules(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	alice, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "alice"})
	if err != nil {
		t.Fatalf("CreateMQTTUser alice: %v", err)
	}
	if _, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{Username: "bob"}); err != nil {
		t.Fatalf("CreateMQTTUser bob: %v", err)
	}

	aliceRule, err := store.CreateRule(ctx, aclRuleForPrincipal(t, "alice", "sensors/#"))
	if err != nil {
		t.Fatalf("CreateRule alice: %v", err)
	}
	if _, err := store.CreateRule(ctx, aclRuleForPrincipal(t, "bob", "factory/#")); err != nil {
		t.Fatalf("CreateRule bob: %v", err)
	}
	ghostRule, err := store.CreateRule(ctx, aclRuleForPrincipal(t, "ghost", "telemetry/#"))
	if err != nil {
		t.Fatalf("CreateRule ghost: %v", err)
	}

	// Disable alice: her rule becomes orphan.
	disabled := true
	if _, err := store.UpdateMQTTUser(ctx, alice.ID, UpdateMQTTUserParams{Disabled: &disabled}); err != nil {
		t.Fatalf("UpdateMQTTUser disable alice: %v", err)
	}
	// Delete bob by username lookup: the storage layer needs a helper that
	// accepts the username (callers come from the deploy service which only
	// sees usernames in the rendered output).
	if err := store.DeleteMQTTUserByUsername(ctx, "bob"); err != nil {
		t.Fatalf("DeleteMQTTUserByUsername: %v", err)
	}

	orphans, err := store.FindOrphanRules(ctx)
	if err != nil {
		t.Fatalf("FindOrphanRules: %v", err)
	}
	gotPrincipals := make(map[string]struct{}, len(orphans))
	for _, r := range orphans {
		gotPrincipals[r.Principal] = struct{}{}
	}
	if _, ok := gotPrincipals["alice"]; !ok {
		t.Errorf("expected alice's rule to be reported as orphan; got %v", orphans)
	}
	if _, ok := gotPrincipals["ghost"]; !ok {
		t.Errorf("expected ghost's rule to be reported as orphan; got %v", orphans)
	}
	// Sanity: the rule IDs match what we created.
	if orphans[0].ID != aliceRule.ID && (len(orphans) < 2 || orphans[1].ID != aliceRule.ID) {
		t.Errorf("aliceRule id %q missing from orphans %+v", aliceRule.ID, orphans)
	}
	_ = ghostRule
}

// TestCreateMQTTUserReservesServiceUsername ensures the storage layer rejects
// creating an MQTT user that would shadow the broker service account.
// The caller passes the reserved username explicitly via CreateMQTTUserParams.
// This is the storage-layer half of the reservation; the HTTP layer rejects
// before reaching this path when the deployment context is available.
func TestCreateMQTTUserReservesServiceUsername(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	defer store.Close()
	ctx := context.Background()

	_, err := store.CreateMQTTUser(ctx, CreateMQTTUserParams{
		Username:        "svc-mcm",
		PasswordHash:    "irrelevant",
		ServiceReserved: "svc-mcm",
	})
	if err == nil {
		t.Fatal("expected error when creating an MQTT user that collides with the service account")
	}
	if !errors.Is(err, ErrMQTTUserServiceReserved) {
		t.Fatalf("error = %v, want ErrMQTTUserServiceReserved", err)
	}
}
