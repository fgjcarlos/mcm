package mosquitto

import (
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// RenderACLFile produces a Mosquitto-compatible ACL file body.
// Rules are grouped by Principal (lexicographic sort), with "user <principal>"
// header per group and blank lines between groups.
// Empty input returns "".
func RenderACLFile(rules []acl.Rule) string {
	if len(rules) == 0 {
		return ""
	}

	// Group rules by Principal.
	groups := make(map[string][]acl.Rule)
	for _, r := range rules {
		groups[r.Principal] = append(groups[r.Principal], r)
	}

	// Sort principals lexicographically.
	principals := make([]string, 0, len(groups))
	for p := range groups {
		principals = append(principals, p)
	}
	sort.Strings(principals)

	var b strings.Builder
	for i, principal := range principals {
		if i > 0 {
			// Each group ends with a '\n' from the last topic line.
			// One extra '\n' creates the blank line separator.
			b.WriteByte('\n')
		}
		b.WriteString("user ")
		b.WriteString(principal)
		b.WriteByte('\n')
		for j, r := range groups[principal] {
			b.WriteString(r.MosquittoACL())
			// Write '\n' after every rule except the very last rule of the last group.
			if i < len(principals)-1 || j < len(groups[principal])-1 {
				b.WriteByte('\n')
			}
		}
	}

	return b.String()
}
