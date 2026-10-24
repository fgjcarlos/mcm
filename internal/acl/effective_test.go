package acl

import "testing"

// TestEffectivePermissionAppliesDenyBeforeGrant pins the documented
// Mosquitto semantics: a deny rule is handled before a grant, so a deny
// that matches the topic wins over any grant that matches it too.
func TestEffectivePermissionAppliesDenyBeforeGrant(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: PermissionReadWrite},
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/secret/#", Permission: PermissionDeny},
	}

	denied := EffectivePermission(rules, Identity{Username: "alice"}, "sensors/secret/1", AccessRead)
	if denied.Allowed {
		t.Fatalf("sensors/secret/1 = allowed (%s), want denied: the deny rule covers the grant", denied.Reason)
	}

	allowed := EffectivePermission(rules, Identity{Username: "alice"}, "sensors/public/1", AccessRead)
	if !allowed.Allowed {
		t.Fatalf("sensors/public/1 = denied (%s), want allowed", allowed.Reason)
	}
}

// TestEffectivePermissionSeparatesAnonymousSection pins that the anonymous
// section of an ACL file applies to unauthenticated clients only and is
// not a fallback for the authenticated ones.
func TestEffectivePermissionSeparatesAnonymousSection(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Selector: SelectorAnonymous, TopicFilter: "public/#", Permission: PermissionRead},
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "private/#", Permission: PermissionRead},
	}

	anonymous := EffectivePermission(rules, Identity{}, "public/news", AccessRead)
	if !anonymous.Allowed {
		t.Fatalf("anonymous on public/news = denied (%s), want allowed", anonymous.Reason)
	}

	authenticated := EffectivePermission(rules, Identity{Username: "alice"}, "public/news", AccessRead)
	if authenticated.Allowed {
		t.Fatalf("alice on public/news = allowed, want denied: the anonymous section does not apply to a user")
	}

	onPrivate := EffectivePermission(rules, Identity{Username: "alice"}, "private/1", AccessRead)
	if !onPrivate.Allowed {
		t.Fatalf("alice on private/1 = denied (%s), want allowed", onPrivate.Reason)
	}
}

// TestEffectivePermissionAppliesPatternToEveryUser pins that pattern rules
// apply to every user, with the %u and %c placeholders substituted.
func TestEffectivePermissionAppliesPatternToEveryUser(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Selector: SelectorPattern, TopicFilter: "sensors/%u/data", Permission: PermissionReadWrite},
	}

	allowed := EffectivePermission(rules, Identity{Username: "alice", ClientID: "c1"}, "sensors/alice/data", AccessWrite)
	if !allowed.Allowed {
		t.Fatalf("alice on sensors/alice/data = denied (%s), want allowed", allowed.Reason)
	}

	other := EffectivePermission(rules, Identity{Username: "bob", ClientID: "c2"}, "sensors/alice/data", AccessWrite)
	if other.Allowed {
		t.Fatalf("bob on sensors/alice/data = allowed, want denied: the username placeholder substitutes the username")
	}

	clientID := EffectivePermission(rules, Identity{Username: "bob", ClientID: "c2"}, "sensors/bob/data", AccessRead)
	if !clientID.Allowed {
		t.Fatalf("bob on sensors/bob/data = denied (%s), want allowed", clientID.Reason)
	}
}

// TestEffectivePermissionDeniesUnmatchedTopic pins the default deny: a
// topic no rule matches is not accessible.
func TestEffectivePermissionDeniesUnmatchedTopic(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: PermissionReadWrite},
	}

	decision := EffectivePermission(rules, Identity{Username: "alice"}, "other/1", AccessRead)
	if decision.Allowed {
		t.Fatalf("alice on other/1 = allowed (%s), want denied", decision.Reason)
	}
}

// TestEffectivePermissionSeparatesReadAndWrite pins that a read grant does
// not allow a publish and the other way round.
func TestEffectivePermissionSeparatesReadAndWrite(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: PermissionRead},
	}

	read := EffectivePermission(rules, Identity{Username: "alice"}, "sensors/1", AccessRead)
	if !read.Allowed {
		t.Fatalf("alice read on sensors/1 = denied (%s), want allowed", read.Reason)
	}

	write := EffectivePermission(rules, Identity{Username: "alice"}, "sensors/1", AccessWrite)
	if write.Allowed {
		t.Fatalf("alice write on sensors/1 = allowed, want denied: the grant is read only")
	}
}

// TestEffectiveGrantsReportsDeniedGrants pins that a grant whose whole
// topic filter is covered by a deny rule is reported as denied.
func TestEffectiveGrantsReportsDeniedGrants(t *testing.T) {
	t.Parallel()

	rules := []Rule{
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: PermissionReadWrite},
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/secret/#", Permission: PermissionDeny},
		{Selector: SelectorUser, Principal: "alice", TopicFilter: "sensors/public/#", Permission: PermissionRead},
	}

	grants := EffectiveGrants(rules, Identity{Username: "alice"})
	if len(grants) != 2 {
		t.Fatalf("EffectiveGrants() returned %d grants, want 2: %#v", len(grants), grants)
	}
	for _, grant := range grants {
		switch grant.TopicFilter {
		case "sensors/#":
			if grant.Denied {
				t.Fatalf("sensors/# reported as denied: the deny only covers sensors/secret/#")
			}
		case "sensors/public/#":
			if grant.Denied {
				t.Fatalf("sensors/public/# reported as denied, want allowed")
			}
		default:
			t.Fatalf("unexpected grant %q", grant.TopicFilter)
		}
	}
}

// TestFilterCovers pins the containment rules the effective view relies on
// to decide whether a deny rule removes the access of a grant.
func TestFilterCovers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		deny   string
		grant  string
		covers bool
	}{
		{deny: "sensors/secret/#", grant: "sensors/secret/1", covers: true},
		{deny: "sensors/secret/#", grant: "sensors/secret/a/b", covers: true},
		{deny: "sensors/secret/#", grant: "sensors/public/#", covers: false},
		{deny: "sensors/#", grant: "sensors/secret/1", covers: true},
		{deny: "sensors/#", grant: "sensors/+", covers: true},
		{deny: "sensors/+", grant: "sensors/secret/#", covers: false},
		{deny: "sensors/secret", grant: "sensors/secret", covers: true},
		{deny: "sensors/secret", grant: "sensors/other", covers: false},
		{deny: "sensors/secret", grant: "sensors/secret/1", covers: false},
		{deny: "#", grant: "anything/at/all", covers: true},
		{deny: "sensors/+", grant: "sensors/1", covers: true},
	}

	for _, tc := range cases {
		if got := FilterCovers(tc.deny, tc.grant); got != tc.covers {
			t.Fatalf("FilterCovers(%q, %q) = %v, want %v", tc.deny, tc.grant, got, tc.covers)
		}
	}
}

// TestTopicMatchesFilter pins the MQTT topic matching rules.
func TestTopicMatchesFilter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		filter string
		topic  string
		match  bool
	}{
		{filter: "sensors/1", topic: "sensors/1", match: true},
		{filter: "sensors/1", topic: "sensors/2", match: false},
		{filter: "sensors/+", topic: "sensors/1", match: true},
		{filter: "sensors/+", topic: "sensors/1/2", match: false},
		{filter: "sensors/#", topic: "sensors", match: true},
		{filter: "sensors/#", topic: "sensors/1/2", match: true},
		{filter: "#", topic: "a/b/c", match: true},
		{filter: "sensors/1", topic: "sensors/1/2", match: false},
		{filter: "sensors/1", topic: "sensors", match: false},
	}

	for _, tc := range cases {
		if got := TopicMatchesFilter(tc.filter, tc.topic); got != tc.match {
			t.Fatalf("TopicMatchesFilter(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.match)
		}
	}
}

// TestSubstitutePlaceholders pins the %u and %c substitution of a pattern
// rule, including the path separator Mosquitto refuses.
func TestSubstitutePlaceholders(t *testing.T) {
	t.Parallel()

	identity := Identity{Username: "alice", ClientID: "client-1"}
	if got := SubstitutePlaceholders("sensors/%u/%c", identity); got != "sensors/alice/client-1" {
		t.Fatalf("SubstitutePlaceholders() = %q, want %q", got, "sensors/alice/client-1")
	}

	separator := Identity{Username: "a/b", ClientID: "c"}
	if got := SubstitutePlaceholders("sensors/%u", separator); got != "sensors/a_b" {
		t.Fatalf("SubstitutePlaceholders() = %q, want %q", got, "sensors/a_b")
	}

	if got := SubstitutePlaceholders("sensors/plain", identity); got != "sensors/plain" {
		t.Fatalf("SubstitutePlaceholders() = %q, want %q", got, "sensors/plain")
	}
}
