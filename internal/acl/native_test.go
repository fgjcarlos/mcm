package acl

import (
	"strings"
	"testing"
)

// TestValidateRuleSelectorScopes pins the native selector semantics: the
// user scope needs a principal, the anonymous and pattern scopes must not
// carry one.
func TestValidateRuleSelectorScopes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		rule        Rule
		wantProblem string
	}{
		{
			name: "anonymous without principal",
			rule: Rule{
				Selector:    SelectorAnonymous,
				TopicFilter: "public/#",
				Permission:  PermissionRead,
			},
		},
		{
			name: "pattern without principal",
			rule: Rule{
				Selector:    SelectorPattern,
				TopicFilter: "clients/%u/state",
				Permission:  PermissionRead,
			},
		},
		{
			name: "user without principal keeps legacy error",
			rule: Rule{
				TopicFilter: "sensors/#",
				Permission:  PermissionRead,
			},
			wantProblem: "principal is required",
		},
		{
			name: "explicit user without principal",
			rule: Rule{
				Selector:    SelectorUser,
				TopicFilter: "sensors/#",
				Permission:  PermissionRead,
			},
			wantProblem: "principal is required",
		},
		{
			name: "anonymous with principal is rejected",
			rule: Rule{
				Selector:    SelectorAnonymous,
				Principal:   "alice",
				TopicFilter: "public/#",
				Permission:  PermissionRead,
			},
			wantProblem: "principal must be empty for anonymous rules",
		},
		{
			name: "pattern with principal is rejected",
			rule: Rule{
				Selector:    SelectorPattern,
				Principal:   "alice",
				TopicFilter: "clients/%u/state",
				Permission:  PermissionRead,
			},
			wantProblem: "principal must be empty for pattern rules",
		},
		{
			name: "unknown selector is rejected",
			rule: Rule{
				Selector:    Selector("bogus"),
				TopicFilter: "sensors/#",
				Permission:  PermissionRead,
			},
			wantProblem: `selector must be one of: "user", "anonymous", "pattern"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateRule(tt.rule)
			if tt.wantProblem == "" {
				if err != nil {
					t.Fatalf("ValidateRule(%#v) returned error: %v", tt.rule, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantProblem) {
				t.Fatalf("ValidateRule(%#v) error = %v, want %q", tt.rule, err, tt.wantProblem)
			}
		})
	}
}

// TestValidateRuleRejectsSelectorInjection pins that a selector can never
// smuggle an ACL directive into the rendered file.
func TestValidateRuleRejectsSelectorInjection(t *testing.T) {
	t.Parallel()

	err := ValidateRule(Rule{
		Selector:    Selector("user\npattern deny #"),
		Principal:   "alice",
		TopicFilter: "sensors/#",
		Permission:  PermissionRead,
	})
	if err == nil || !strings.Contains(err.Error(), `selector must be one of`) {
		t.Fatalf("ValidateRule(injected selector) error = %v, want selector rejection", err)
	}
}

// TestValidateRulePatternPlaceholders pins the whole-level placeholder
// policy for pattern rules.
func TestValidateRulePatternPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		filter      string
		wantProblem string
	}{
		{
			name:   "username placeholder on its own level",
			filter: "clients/%u/state",
		},
		{
			name:   "client id placeholder on its own level",
			filter: "clients/%c/state",
		},
		{
			name:        "placeholder glued to text",
			filter:      "clients/%uX/state",
			wantProblem: "must each occupy an entire topic level",
		},
		{
			name:        "both placeholders on one level",
			filter:      "clients/%u%c/state",
			wantProblem: "must each occupy an entire topic level",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateRule(Rule{
				Selector:    SelectorPattern,
				TopicFilter: tt.filter,
				Permission:  PermissionRead,
			})
			if tt.wantProblem == "" {
				if err != nil {
					t.Fatalf("ValidateRule(pattern %q) returned error: %v", tt.filter, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantProblem) {
				t.Fatalf("ValidateRule(pattern %q) error = %v, want %q", tt.filter, err, tt.wantProblem)
			}
		})
	}
}

// TestValidateRuleKeepsLiteralPlaceholdersInTopicRules pins that a
// user-scoped rule keeps a literal %u/%c in its topic filter: MCM neither
// rewrites nor rejects it.
func TestValidateRuleKeepsLiteralPlaceholdersInTopicRules(t *testing.T) {
	t.Parallel()

	rule := Rule{
		Principal:   "alice",
		TopicFilter: "clients/%u/state",
		Permission:  PermissionRead,
	}
	if err := ValidateRule(rule); err != nil {
		t.Fatalf("ValidateRule(user rule with placeholder) returned error: %v", err)
	}
	if rule.TopicFilter != "clients/%u/state" {
		t.Fatalf("topic filter was rewritten to %q", rule.TopicFilter)
	}
}

// TestRuleEffectiveSelector pins the legacy default: a rule without an
// explicit selector behaves as a user-scoped rule.
func TestRuleEffectiveSelector(t *testing.T) {
	t.Parallel()

	legacy := Rule{Principal: "alice"}
	if got := legacy.EffectiveSelector(); got != SelectorUser {
		t.Fatalf("EffectiveSelector() = %q, want %q", got, SelectorUser)
	}
	if !legacy.IsUserScoped() {
		t.Fatal("legacy rule must be user scoped")
	}

	anonymous := Rule{Selector: SelectorAnonymous}
	if anonymous.IsUserScoped() {
		t.Fatal("anonymous rule must not be user scoped")
	}

	pattern := Rule{Selector: SelectorPattern}
	if pattern.IsUserScoped() {
		t.Fatal("pattern rule must not be user scoped")
	}

	normalized := legacy.Normalize()
	if normalized.Selector != SelectorUser {
		t.Fatalf("Normalize().Selector = %q, want %q", normalized.Selector, SelectorUser)
	}
}

// TestRuleMosquittoPatternACL pins the rendered pattern directive.
func TestRuleMosquittoPatternACL(t *testing.T) {
	t.Parallel()

	rule := Rule{
		Selector:    SelectorPattern,
		TopicFilter: "clients/%u/state",
		Permission:  PermissionDeny,
	}
	if got, want := rule.MosquittoPatternACL(), "pattern deny clients/%u/state"; got != want {
		t.Fatalf("MosquittoPatternACL() = %q, want %q", got, want)
	}
}
