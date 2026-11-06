package acl

import (
	"strings"
	"testing"
)

func TestValidateTopicFilterAcceptsWildcards(t *testing.T) {
	t.Parallel()

	valid := []string{
		"sensors/+/temperature",
		"factory/#",
		"#",
		"/leading/slash",
		"trailing/slash/",
	}

	for _, filter := range valid {
		if err := ValidateTopicFilter(filter); err != nil {
			t.Fatalf("ValidateTopicFilter(%q) returned error: %v", filter, err)
		}
	}
}

func TestValidateTopicFilterRejectsInvalidWildcards(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"sensors/#/temperature",
		"sensors+room/value",
		"factory/area#",
		"factory/+east/value",
	}

	for _, filter := range invalid {
		if err := ValidateTopicFilter(filter); err == nil {
			t.Fatalf("ValidateTopicFilter(%q) succeeded, want error", filter)
		}
	}
}

func TestValidateRuleRejectsControlCharacters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{
			name: "principal newline",
			rule: Rule{Principal: "operator\nforged", TopicFilter: "factory/#", Permission: PermissionRead},
			want: "principal must not contain control characters",
		},
		{
			name: "topic filter NUL",
			rule: Rule{Principal: "operator", TopicFilter: "factory/\x00/#", Permission: PermissionRead},
			want: "topic_filter must not contain NUL",
		},
		{
			name: "topic filter carriage return",
			rule: Rule{Principal: "operator", TopicFilter: "factory/temperature\r", Permission: PermissionRead},
			want: "topic_filter must not contain control characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRule(tt.rule)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRule() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestValidateRuleRejectsMosquittoUnrepresentableFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rule Rule
		want string
	}{
		{name: "principal whitespace", rule: Rule{Principal: "two words", TopicFilter: "sensors/#", Permission: PermissionRead}, want: "principal must not contain whitespace"},
		{name: "principal passwd separator", rule: Rule{Principal: "alice:admin", TopicFilter: "sensors/#", Permission: PermissionRead}, want: "principal must not contain ':'"},
		{name: "principal too long", rule: Rule{Principal: strings.Repeat("a", 65536), TopicFilter: "sensors/#", Permission: PermissionRead}, want: "principal must be at most"},
		{name: "topic whitespace separator", rule: Rule{Principal: "alice", TopicFilter: "sensor room/#", Permission: PermissionRead}, want: "topic_filter must not contain whitespace"},
		{name: "topic too long", rule: Rule{Principal: "alice", TopicFilter: strings.Repeat("a", 65536), Permission: PermissionRead}, want: "topic_filter must be at most"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRule(tt.rule)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ValidateRule() error = %v, want field error containing %q", err, tt.want)
			}
		})
	}
}

func TestValidateRuleRejectsInvalidUTF8AndAcceptsByteLimit(t *testing.T) {
	t.Parallel()
	invalid := string([]byte{0xff})
	if err := ValidateRule(Rule{Principal: invalid, TopicFilter: "sensors/#", Permission: PermissionRead}); err == nil || !strings.Contains(err.Error(), "principal must be valid UTF-8") {
		t.Fatalf("invalid UTF-8 principal error = %v, want field error", err)
	}
	if err := ValidateRule(Rule{Principal: "alice", TopicFilter: strings.Repeat("a", MaxFieldBytes), Permission: PermissionRead}); err != nil {
		t.Fatalf("topic filter at byte limit rejected: %v", err)
	}
}

func TestValidateRulePreservesUnicodeNormalizationForms(t *testing.T) {
	t.Parallel()
	for _, principal := range []string{"é", "e\u0301", "用户"} {
		if err := ValidateRule(Rule{Principal: principal, TopicFilter: "sensors/#", Permission: PermissionRead}); err != nil {
			t.Errorf("ValidateRule(%q) rejected representable Unicode: %v", principal, err)
		}
	}
}

func TestValidateRuleAcceptsPermissions(t *testing.T) {
	t.Parallel()

	permissions := []Permission{
		PermissionRead,
		PermissionWrite,
		PermissionReadWrite,
	}

	for _, permission := range permissions {
		err := ValidateRule(Rule{
			Principal:   "alice",
			TopicFilter: "sensors/+/temperature",
			Permission:  permission,
		})
		if err != nil {
			t.Fatalf("ValidateRule(%q) returned error: %v", permission, err)
		}
	}
}

func TestRuleMosquittoACL(t *testing.T) {
	t.Parallel()

	rule := Rule{
		Principal:   "alice",
		TopicFilter: "sensors/+/temperature",
		Permission:  PermissionWrite,
	}

	if got, want := rule.MosquittoACL(), "topic write sensors/+/temperature"; got != want {
		t.Fatalf("MosquittoACL() = %q, want %q", got, want)
	}
}
