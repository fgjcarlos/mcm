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
