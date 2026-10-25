package mosquitto

import (
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

func TestRenderACLFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		rules []acl.Rule
		want  string
	}{
		{
			name:  "empty input returns empty string",
			rules: []acl.Rule{},
			want:  "",
		},
		{
			name: "single user single rule",
			rules: []acl.Rule{
				{Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
			},
			want: "user alice\ntopic read sensors/#",
		},
		{
			name: "single user multiple rules preserves input order",
			rules: []acl.Rule{
				{Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
				{Principal: "alice", TopicFilter: "cmd/#", Permission: acl.PermissionWrite},
			},
			want: "user alice\ntopic read sensors/#\ntopic write cmd/#",
		},
		{
			name: "multiple users sorted lexicographically with blank line separator",
			rules: []acl.Rule{
				{Principal: "zebra", TopicFilter: "z/#", Permission: acl.PermissionRead},
				{Principal: "alice", TopicFilter: "a/#", Permission: acl.PermissionWrite},
			},
			want: "user alice\ntopic write a/#\n\nuser zebra\ntopic read z/#",
		},
		{
			name: "three users sorted lexicographically",
			rules: []acl.Rule{
				{Principal: "charlie", TopicFilter: "c/+", Permission: acl.PermissionReadWrite},
				{Principal: "alice", TopicFilter: "a/#", Permission: acl.PermissionRead},
				{Principal: "bob", TopicFilter: "b/+/sensor", Permission: acl.PermissionWrite},
			},
			want: "user alice\ntopic read a/#\n\nuser bob\ntopic write b/+/sensor\n\nuser charlie\ntopic readwrite c/+",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := RenderACLFile(tc.rules)
			if got != tc.want {
				t.Fatalf("RenderACLFile() =\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// TestRenderACLFileRendersDenyPermission pins that a deny rule renders as
// a native Mosquitto `topic deny` line inside the user section.
func TestRenderACLFileRendersDenyPermission(t *testing.T) {
	t.Parallel()

	rules := []acl.Rule{
		{Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionDeny},
	}

	want := "user alice\ntopic deny sensors/#"

	if got := RenderACLFile(rules); got != want {
		t.Fatalf("RenderACLFile() =\n%q\nwant:\n%q", got, want)
	}
}

// TestParseACLFileReadsDenyPermission pins that the parser recognises the
// deny permission keyword.
func TestParseACLFileReadsDenyPermission(t *testing.T) {
	t.Parallel()

	body := "user alice\ntopic deny sensors/#"

	rules := ParseACLFile(body)

	if len(rules) != 1 {
		t.Fatalf("ParseACLFile() returned %d rules, want 1: %#v", len(rules), rules)
	}
	if rules[0].Permission != acl.PermissionDeny {
		t.Fatalf("Permission = %q, want %q", rules[0].Permission, acl.PermissionDeny)
	}
	if rules[0].Principal != "alice" || rules[0].TopicFilter != "sensors/#" {
		t.Fatalf("parsed rule = %#v, want principal alice and topic sensors/#", rules[0])
	}
}

// TestParseACLFileReadsAnonymousTopicLines pins that a topic line before the
// first user line is read as a rule of its own. The native anonymous scope
// is exactly that shape.
func TestParseACLFileReadsAnonymousTopicLines(t *testing.T) {
	t.Parallel()

	rules := ParseACLFile("topic read public/#\n")

	if len(rules) != 1 {
		t.Fatalf("ParseACLFile() returned %d rules, want 1: %#v", len(rules), rules)
	}
	if rules[0].TopicFilter != "public/#" {
		t.Fatalf("parsed topic filter = %q, want %q", rules[0].TopicFilter, "public/#")
	}
	if rules[0].Permission != acl.PermissionRead {
		t.Fatalf("parsed permission = %q, want %q", rules[0].Permission, acl.PermissionRead)
	}
}

// TestRenderParseACLFileRoundTripsDeny pins that a rendered deny rule
// parses back into the same rule.
//
// The parser now reports the native scope explicitly, so the parsed rule
// carries the user scope where the hand-written rule left the selector
// empty. The round trip is compared against the normalized rule.
func TestRenderParseACLFileRoundTripsDeny(t *testing.T) {
	t.Parallel()

	rules := []acl.Rule{
		{Principal: "alice", TopicFilter: "sensors/+/temperature", Permission: acl.PermissionDeny},
		{Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
	}

	parsed := ParseACLFile(RenderACLFile(rules))

	if len(parsed) != len(rules) {
		t.Fatalf("round-trip returned %d rules, want %d: %#v", len(parsed), len(rules), parsed)
	}
	for idx := range rules {
		if want := rules[idx].Normalize(); parsed[idx] != want {
			t.Fatalf("round-trip rule[%d] = %#v, want %#v", idx, parsed[idx], want)
		}
	}
}

// TestRenderACLFileRendersNativeSelectors pins the canonical layout of a
// native ACL file: the anonymous topic section comes first, the pattern
// section follows, and the user sections come last.
func TestRenderACLFileRendersNativeSelectors(t *testing.T) {
	t.Parallel()

	rules := []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
		{Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
		{Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionDeny},
	}

	want := strings.Join([]string{
		"topic read public/#",
		"",
		"pattern deny clients/%u/state",
		"",
		"user alice",
		"topic read sensors/#",
	}, "\n")

	if got := RenderACLFile(rules); got != want {
		t.Fatalf("RenderACLFile() =\n%q\nwant:\n%q", got, want)
	}
}

// TestRenderACLFileKeepsLegacyOutputUnchanged pins that rules without an
// explicit selector still render exactly like the pre-native renderer.
func TestRenderACLFileKeepsLegacyOutputUnchanged(t *testing.T) {
	t.Parallel()

	rules := []acl.Rule{
		{Principal: "zebra", TopicFilter: "z/#", Permission: acl.PermissionRead},
		{Principal: "alice", TopicFilter: "a/#", Permission: acl.PermissionWrite},
	}

	want := "user alice\ntopic write a/#\n\nuser zebra\ntopic read z/#"

	if got := RenderACLFile(rules); got != want {
		t.Fatalf("RenderACLFile() =\n%q\nwant:\n%q", got, want)
	}
}

// TestParseACLFileRecognizesNativeDirectives pins that the parser
// understands the directives MCM emits: anonymous topic lines, pattern
// lines and the deny permission.
func TestParseACLFileRecognizesNativeDirectives(t *testing.T) {
	t.Parallel()

	body := strings.Join([]string{
		"# managed by mcm",
		"topic read public/#",
		"pattern deny clients/%u/state",
		"",
		"user alice",
		"topic read sensors/#",
		"topic write cmd/#",
	}, "\n")

	rules := ParseACLFile(body)

	// A parsed file is always the broker-wide ACL file, so every parsed
	// rule carries the global scope.
	want := []acl.Rule{
		{Selector: acl.SelectorAnonymous, Scope: acl.ScopeGlobal, TopicFilter: "public/#", Permission: acl.PermissionRead},
		{Selector: acl.SelectorPattern, Scope: acl.ScopeGlobal, TopicFilter: "clients/%u/state", Permission: acl.PermissionDeny},
		{Selector: acl.SelectorUser, Scope: acl.ScopeGlobal, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
		{Selector: acl.SelectorUser, Scope: acl.ScopeGlobal, Principal: "alice", TopicFilter: "cmd/#", Permission: acl.PermissionWrite},
	}

	if len(rules) != len(want) {
		t.Fatalf("ParseACLFile() returned %d rules, want %d: %#v", len(rules), len(want), rules)
	}
	for idx := range want {
		if rules[idx] != want[idx] {
			t.Fatalf("rule[%d] = %#v, want %#v", idx, rules[idx], want[idx])
		}
	}
}

// TestRenderParseACLFileRoundTrip pins that a freshly rendered native ACL
// file parses back into the same rules.
func TestRenderParseACLFileRoundTrip(t *testing.T) {
	t.Parallel()

	rules := []acl.Rule{
		{Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
		{Selector: acl.SelectorPattern, TopicFilter: "clients/%u/state", Permission: acl.PermissionDeny},
		{Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
	}

	parsed := ParseACLFile(RenderACLFile(rules))

	if len(parsed) != len(rules) {
		t.Fatalf("round-trip returned %d rules, want %d: %#v", len(parsed), len(rules), parsed)
	}
	for idx := range rules {
		want := rules[idx]
		got := parsed[idx]
		if got.Selector != want.Selector || got.Principal != want.Principal ||
			got.TopicFilter != want.TopicFilter || got.Permission != want.Permission {
			t.Fatalf("round-trip rule[%d] = %#v, want %#v", idx, got, want)
		}
	}
}
