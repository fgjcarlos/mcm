package mosquitto

import (
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

// TestRenderParseACLFileRoundTripsDeny pins that a rendered deny rule
// parses back into the same rule.
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
		if parsed[idx] != rules[idx] {
			t.Fatalf("round-trip rule[%d] = %#v, want %#v", idx, parsed[idx], rules[idx])
		}
	}
}
