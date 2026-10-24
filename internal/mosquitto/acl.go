package mosquitto

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// ListenerACLPath returns the path of the ACL file that carries the rules
// of one listener.
//
// Mosquitto resolves `acl_file` per listener once
// `per_listener_settings true` is set: the global file applies to every
// listener that does not declare its own, and a listener that declares one
// uses only that file. MCM therefore keeps the global file at its
// configured path and writes the per-listener files next to it, named after
// the listener they belong to.
//
// The listener reference is validated by the acl package before it reaches
// this function, so it cannot escape the ACL directory.
func ListenerACLPath(globalACLPath, listenerID string) string {
	if globalACLPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(globalACLPath), "acl."+listenerID)
}

// RenderACLFile produces a Mosquitto-compatible ACL file body.
//
// The layout follows the Mosquitto 2.0 ACL grammar:
//
//   - The anonymous section comes first. A `topic` line that appears before
//     the first `user` line applies to unauthenticated clients only; it is
//     not a fallback for authenticated users.
//   - The pattern section follows. `pattern` lines apply to every user
//     inside the ACL, so they stay globally applicable no matter which
//     `user` section surrounds them.
//   - The user sections come last, grouped by principal (lexicographic
//     sort) with a "user <principal>" header per group.
//
// Empty input returns "".
func RenderACLFile(rules []acl.Rule) string {
	if len(rules) == 0 {
		return ""
	}

	var (
		anonymous []acl.Rule
		patterns  []acl.Rule
		groups    = make(map[string][]acl.Rule)
	)
	for _, r := range rules {
		switch r.EffectiveSelector() {
		case acl.SelectorAnonymous:
			anonymous = append(anonymous, r)
		case acl.SelectorPattern:
			patterns = append(patterns, r)
		default:
			groups[r.Principal] = append(groups[r.Principal], r)
		}
	}

	// Sort principals lexicographically.
	principals := make([]string, 0, len(groups))
	for p := range groups {
		principals = append(principals, p)
	}
	sort.Strings(principals)

	var sections []string
	if len(anonymous) > 0 {
		sections = append(sections, renderACLSection(anonymous, func(r acl.Rule) string {
			return r.MosquittoACL()
		}))
	}
	if len(patterns) > 0 {
		sections = append(sections, renderACLSection(patterns, func(r acl.Rule) string {
			return r.MosquittoPatternACL()
		}))
	}
	for _, principal := range principals {
		var b strings.Builder
		b.WriteString("user ")
		b.WriteString(principal)
		b.WriteByte('\n')
		for _, r := range groups[principal] {
			b.WriteString(r.MosquittoACL())
			b.WriteByte('\n')
		}
		sections = append(sections, strings.TrimSuffix(b.String(), "\n"))
	}

	return strings.Join(sections, "\n\n")
}

// renderACLSection renders the body of one ACL section. Every line is
// terminated by a newline; the trailing newline is trimmed so sections can
// be joined with a blank line separator.
func renderACLSection(rules []acl.Rule, line func(acl.Rule) string) string {
	var b strings.Builder
	for _, r := range rules {
		b.WriteString(line(r))
		b.WriteByte('\n')
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// ParseACLFile parses a Mosquitto ACL file body and returns the rules
// it contains. Used by the deploy verifier to pick a test subject (a
// non-service user + a granted topic filter + a denied topic filter).
//
// The Mosquitto ACL grammar we support:
//
//	user <principal>
//	topic {read|readwrite|write|deny} <topic_filter>
//	pattern {read|readwrite|write|deny} <topic_filter>
//
// Comments (#) and blank lines are skipped. Unknown directives are
// ignored. A `topic` line that appears before the first `user` line
// applies to anonymous clients; every later `topic` line inherits the most
// recent "user" line. A `pattern` line applies to every user inside the
// ACL and carries no principal.
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
			if len(fields) < 3 {
				continue
			}
			perm := strings.ToLower(fields[1])
			topic := strings.Join(fields[2:], " ")
			rule := acl.Rule{
				// A parsed file is always the broker-wide ACL file: the
				// per-listener files are rendered by MCM, not parsed from
				// an arbitrary source.
				Scope:       acl.ScopeGlobal,
				TopicFilter: topic,
				Permission:  acl.Permission(mosquittoPermToACLPerm(perm)),
			}
			if currentUser == "" {
				rule.Selector = acl.SelectorAnonymous
			} else {
				rule.Selector = acl.SelectorUser
				rule.Principal = currentUser
			}
			rules = append(rules, rule)
		case "pattern":
			if len(fields) < 3 {
				continue
			}
			perm := strings.ToLower(fields[1])
			topic := strings.Join(fields[2:], " ")
			rules = append(rules, acl.Rule{
				Selector:    acl.SelectorPattern,
				Scope:       acl.ScopeGlobal,
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
		return string(acl.PermissionDeny)
	default:
		return ""
	}
}
