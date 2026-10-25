package acl

import (
	"strings"
	"testing"
)

// TestValidateRuleRequiresListenerForListenerScope pins that a
// listener-scoped rule must name the listener it belongs to.
func TestValidateRuleRequiresListenerForListenerScope(t *testing.T) {
	t.Parallel()

	err := ValidateRule(Rule{
		Scope:       ScopeListener,
		TopicFilter: "sensors/#",
		Permission:  PermissionRead,
	})
	if err == nil {
		t.Fatal("ValidateRule() accepted a listener scoped rule without a listener id")
	}
	if !strings.Contains(err.Error(), "listener_id is required") {
		t.Fatalf("ValidateRule() error = %q, want it to mention the missing listener id", err)
	}
}

// TestValidateRuleRejectsListenerForGlobalScope pins that a global rule
// never carries a listener reference.
func TestValidateRuleRejectsListenerForGlobalScope(t *testing.T) {
	t.Parallel()

	err := ValidateRule(Rule{
		Scope:       ScopeGlobal,
		ListenerID:  "tls",
		TopicFilter: "sensors/#",
		Permission:  PermissionRead,
	})
	if err == nil {
		t.Fatal("ValidateRule() accepted a global rule with a listener id")
	}
	if !strings.Contains(err.Error(), "listener_id must be empty") {
		t.Fatalf("ValidateRule() error = %q, want it to mention the listener id", err)
	}
}

// TestValidateRuleRejectsUnsafeListenerID pins that the listener reference
// cannot escape the ACL directory: it is used to name the ACL file.
func TestValidateRuleRejectsUnsafeListenerID(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"../etc/passwd", "a/b", `a\b`, "with space", "..", ""} {
		err := ValidateRule(Rule{
			Scope:       ScopeListener,
			ListenerID:  id,
			TopicFilter: "sensors/#",
			Permission:  PermissionRead,
		})
		if err == nil {
			t.Fatalf("ValidateRule() accepted the listener id %q", id)
		}
	}
}

// TestValidateRuleRejectsUnknownScope pins that an unknown scope is
// refused instead of silently falling back to the global scope.
func TestValidateRuleRejectsUnknownScope(t *testing.T) {
	t.Parallel()

	err := ValidateRule(Rule{
		Scope:       Scope("everything"),
		TopicFilter: "sensors/#",
		Permission:  PermissionRead,
	})
	if err == nil {
		t.Fatal("ValidateRule() accepted an unknown scope")
	}
	if !strings.Contains(err.Error(), `scope must be one of`) {
		t.Fatalf("ValidateRule() error = %q, want it to list the valid scopes", err)
	}
}

// TestNormalizeResolvesScope pins that a rule stored before the scoped
// rules existed keeps its meaning: an empty scope is the global scope.
func TestNormalizeResolvesScope(t *testing.T) {
	t.Parallel()

	rule := Rule{TopicFilter: "sensors/#", Permission: PermissionRead}.Normalize()
	if rule.Scope != ScopeGlobal {
		t.Fatalf("Normalize() scope = %q, want %q", rule.Scope, ScopeGlobal)
	}
	if rule.ListenerID != "" {
		t.Fatalf("Normalize() listener id = %q, want empty", rule.ListenerID)
	}

	scoped := Rule{Scope: ScopeListener, ListenerID: "tls", TopicFilter: "sensors/#", Permission: PermissionRead}.Normalize()
	if scoped.Scope != ScopeListener || scoped.ListenerID != "tls" {
		t.Fatalf("Normalize() = %#v, want the listener scope preserved", scoped)
	}
}
