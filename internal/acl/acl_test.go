package acl

import "testing"

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
