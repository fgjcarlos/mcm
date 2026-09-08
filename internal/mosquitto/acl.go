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

// ParseACLFile parses a Mosquitto ACL file body and returns the rules
// it contains. Used by the deploy verifier to pick a test subject (a
// non-service user + a granted topic filter + a denied topic filter).
//
// The Mosquitto ACL grammar we support:
//
//	user <principal>
//	topic {read|readwrite|write|deny} <topic_filter>
//
// Comments (#) and blank lines are skipped. Unknown directives are
// ignored. The principal of each topic line is inherited from the most
// recent "user" line in the file.
func ParseACLFile(body string) []acl.Rule {
	var rules []acl.Rule
	var currentUser string
	for _, raw := range strings.Split(body, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		switch strings.ToLower(fields[0]) {
		case "user":
			currentUser = fields[1]
		case "topic":
			if currentUser == "" || len(fields) < 3 {
				continue
			}
			perm := strings.ToLower(fields[1])
			topic := strings.Join(fields[2:], " ")
			rules = append(rules, acl.Rule{
				Principal:   currentUser,
				TopicFilter: topic,
				Permission:  acl.Permission(mosquittoPermToACLPerm(perm)),
			})
		}
	}
	return rules
}

// mosquittoPermToACLPerm translates the Mosquitto permission keyword
// to the canonical acl.Permission value.
func mosquittoPermToACLPerm(perm string) string {
	switch perm {
	case "read":
		return string(acl.PermissionRead)
	case "write":
		return string(acl.PermissionWrite)
	case "readwrite":
		return string(acl.PermissionReadWrite)
	case "deny":
		return "deny"
	default:
		return ""
	}
}
