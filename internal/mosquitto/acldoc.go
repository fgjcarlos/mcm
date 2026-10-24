package mosquitto

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// ACLEntryKind classifies one line of a Mosquitto ACL document.
//
// The kinds mirror the directives the Mosquitto 2.0 ACL grammar defines,
// plus the two structural kinds (blank, comment) and the catch-all kind
// (unknown) for a line MCM cannot interpret.
type ACLEntryKind string

const (
	// ACLEntryBlank is an empty (or whitespace-only) line.
	ACLEntryBlank ACLEntryKind = "blank"
	// ACLEntryComment is a comment line.
	ACLEntryComment ACLEntryKind = "comment"
	// ACLEntryUser is a `user <principal>` section header.
	ACLEntryUser ACLEntryKind = "user"
	// ACLEntryTopic is a `topic <perm> <filter>` grant or deny.
	ACLEntryTopic ACLEntryKind = "topic"
	// ACLEntryPattern is a `pattern <perm> <filter>` entry.
	ACLEntryPattern ACLEntryKind = "pattern"
	// ACLEntryUnknown is a directive MCM does not model.
	ACLEntryUnknown ACLEntryKind = "unknown"
)

// ACLEntry is one line of an ACL document.
//
// Raw always keeps the verbatim source line (without the line terminator),
// so a document can be rendered back byte-identically. Rule carries the
// typed rule when MCM could interpret the line; Issue explains why an
// unrecognised or malformed line could not be interpreted.
type ACLEntry struct {
	Kind      ACLEntryKind `json:"kind"`
	Raw       string       `json:"raw"`
	Principal string       `json:"principal,omitempty"`
	Rule      *acl.Rule    `json:"rule,omitempty"`
	Issue     string       `json:"issue,omitempty"`
}

// ACLDocument is a lossless model of a Mosquitto ACL file.
//
// Every line of the parsed body becomes one entry, in order. The entries
// carry the verbatim source text, so rendering the document reproduces the
// original bytes — including comments, blank lines and directives MCM does
// not model.
type ACLDocument struct {
	Entries         []ACLEntry `json:"entries"`
	EndsWithNewline bool       `json:"ends_with_newline"`
}

// ParseACLDocument parses an ACL file body into a lossless document.
//
// Every line of the body becomes one entry, in order:
//
//   - a blank line is ACLEntryBlank;
//   - a `#` line is ACLEntryComment;
//   - `user <principal>` is ACLEntryUser and opens a scope;
//   - `topic <perm> <filter>` is ACLEntryTopic carrying the scope in effect
//     (an empty scope means anonymous) and, when the permission and the
//     filter are valid, a typed *acl.Rule;
//   - `pattern <perm> <filter>` is ACLEntryPattern and carries no principal;
//   - every other line is ACLEntryUnknown with an Issue explaining why it
//     could not be interpreted.
//
// Recognition tolerates surrounding whitespace and a trailing carriage
// return without changing the verbatim Raw line.
func ParseACLDocument(body string) ACLDocument {
	doc := ACLDocument{}
	if body == "" {
		return doc
	}

	lines := strings.Split(body, "\n")
	if strings.HasSuffix(body, "\n") {
		doc.EndsWithNewline = true
		lines = lines[:len(lines)-1]
	}

	scope := ""
	for _, raw := range lines {
		doc.Entries = append(doc.Entries, parseACLEntry(raw, &scope))
	}
	return doc
}

// parseACLEntry classifies one raw line and advances the scope when the line
// opens a new user section.
func parseACLEntry(raw string, scope *string) ACLEntry {
	entry := ACLEntry{Kind: ACLEntryBlank, Raw: raw}

	trimmed := strings.TrimSpace(raw)
	switch {
	case trimmed == "":
		return entry
	case strings.HasPrefix(trimmed, "#"):
		entry.Kind = ACLEntryComment
		return entry
	}

	fields := strings.Fields(trimmed)
	switch strings.ToLower(fields[0]) {
	case "user":
		if len(fields) != 2 {
			entry.Kind = ACLEntryUnknown
			entry.Issue = "user directive takes exactly one principal"
			return entry
		}
		entry.Kind = ACLEntryUser
		entry.Principal = fields[1]
		*scope = fields[1]
		return entry
	case "topic":
		if len(fields) < 3 {
			entry.Kind = ACLEntryUnknown
			entry.Issue = "topic directive requires a permission and a topic filter"
			return entry
		}
		entry.Kind = ACLEntryTopic
		entry.Principal = *scope
		selector := acl.SelectorUser
		if *scope == "" {
			selector = acl.SelectorAnonymous
		}
		entry.Rule, entry.Issue = buildACLRule(selector, *scope, fields[1], strings.Join(fields[2:], " "))
		return entry
	case "pattern":
		if len(fields) < 3 {
			entry.Kind = ACLEntryUnknown
			entry.Issue = "pattern directive requires a permission and a topic filter"
			return entry
		}
		entry.Kind = ACLEntryPattern
		entry.Rule, entry.Issue = buildACLRule(acl.SelectorPattern, "", fields[1], strings.Join(fields[2:], " "))
		return entry
	default:
		entry.Kind = ACLEntryUnknown
		entry.Issue = fmt.Sprintf("unsupported directive %q", fields[0])
		return entry
	}
}

// buildACLRule turns a Mosquitto permission keyword and a topic filter into
// a typed rule. It returns a nil rule and an issue when the permission or
// the filter is not valid.
func buildACLRule(selector acl.Selector, principal, permission, filter string) (*acl.Rule, string) {
	rule := acl.Rule{
		Selector:    selector,
		Principal:   principal,
		TopicFilter: filter,
		Permission:  acl.Permission(mosquittoPermToACLPerm(strings.ToLower(permission))),
	}
	if rule.Permission == "" {
		return nil, fmt.Sprintf("unsupported permission %q", permission)
	}
	if err := acl.ValidateRule(rule); err != nil {
		return nil, err.Error()
	}
	return &rule, ""
}

// Render reproduces the original bytes of the document.
//
// The entries are joined with a newline and a trailing newline is appended
// when the source body ended with one. ParseACLDocument(body).Render() is
// byte-identical to body for every body.
func (d ACLDocument) Render() string {
	if len(d.Entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(d.Entries))
	for _, entry := range d.Entries {
		lines = append(lines, entry.Raw)
	}
	out := strings.Join(lines, "\n")
	if d.EndsWithNewline {
		out += "\n"
	}
	return out
}

// TypedRules returns the recognised rules of the document, in document order.
func (d ACLDocument) TypedRules() []acl.Rule {
	rules := make([]acl.Rule, 0, len(d.Entries))
	for _, entry := range d.Entries {
		if entry.Rule != nil {
			rules = append(rules, *entry.Rule)
		}
	}
	return rules
}

// Principals returns the principals the document declares with a `user`
// header, in order and without duplicates.
func (d ACLDocument) Principals() []string {
	var principals []string
	seen := make(map[string]struct{})
	for _, entry := range d.Entries {
		if entry.Kind != ACLEntryUser || entry.Principal == "" {
			continue
		}
		if _, ok := seen[entry.Principal]; ok {
			continue
		}
		seen[entry.Principal] = struct{}{}
		principals = append(principals, entry.Principal)
	}
	return principals
}

// UnknownEntries returns the entries MCM cannot interpret.
func (d ACLDocument) UnknownEntries() []ACLEntry {
	var unknown []ACLEntry
	for _, entry := range d.Entries {
		if entry.Kind == ACLEntryUnknown {
			unknown = append(unknown, entry)
		}
	}
	return unknown
}

// SemanticKey returns the canonical comparable key of one entry.
//
// The key describes the access the entry grants, not its formatting:
//
//	user <principal> | topic <perm> <filter>
//	anonymous | topic <perm> <filter>
//	pattern | <perm> <filter>
//	unknown | <trimmed raw line>
//
// Blank lines, comments and `user` headers carry no semantics and return an
// empty key.
func (e ACLEntry) SemanticKey() string {
	switch e.Kind {
	case ACLEntryTopic:
		perm, filter := e.permissionAndFilter()
		if e.Principal == "" {
			return "anonymous | topic " + perm + " " + filter
		}
		return "user " + e.Principal + " | topic " + perm + " " + filter
	case ACLEntryPattern:
		perm, filter := e.permissionAndFilter()
		return "pattern | " + perm + " " + filter
	case ACLEntryUnknown:
		return "unknown | " + strings.TrimSpace(e.Raw)
	default:
		return ""
	}
}

// permissionAndFilter returns the permission keyword and the topic filter of
// a topic or pattern entry. The typed rule is preferred; a malformed entry
// (one MCM could not turn into a rule) falls back to the raw line.
func (e ACLEntry) permissionAndFilter() (string, string) {
	if e.Rule != nil {
		return string(e.Rule.Permission), e.Rule.TopicFilter
	}
	fields := strings.Fields(strings.TrimSpace(e.Raw))
	if len(fields) < 3 {
		return "", ""
	}
	return strings.ToLower(fields[1]), strings.Join(fields[2:], " ")
}

// SemanticKeys returns the canonical comparable key of every semantic entry
// of the document, sorted. Duplicate keys are collapsed: the result is a
// comparable set of the access the document grants.
func (d ACLDocument) SemanticKeys() []string {
	var keys []string
	seen := make(map[string]struct{})
	for _, entry := range d.Entries {
		key := entry.SemanticKey()
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// MissingSemanticEntries returns the semantic keys the base document grants
// and the next document does not, sorted. It is the access a render would
// drop.
func MissingSemanticEntries(base ACLDocument, next ACLDocument) []string {
	nextKeys := make(map[string]struct{})
	for _, key := range next.SemanticKeys() {
		nextKeys[key] = struct{}{}
	}
	var missing []string
	for _, key := range base.SemanticKeys() {
		if _, ok := nextKeys[key]; ok {
			continue
		}
		missing = append(missing, key)
	}
	sort.Strings(missing)
	return missing
}

// managedRuleKey returns the canonical comparable key of a managed rule. It
// uses the same shape as ACLEntry.SemanticKey so a managed rule and the
// equivalent source line compare equal.
func managedRuleKey(rule acl.Rule) string {
	perm := string(rule.Permission)
	filter := rule.TopicFilter
	switch rule.EffectiveSelector() {
	case acl.SelectorAnonymous:
		return "anonymous | topic " + perm + " " + filter
	case acl.SelectorPattern:
		return "pattern | " + perm + " " + filter
	default:
		return "user " + rule.Principal + " | topic " + perm + " " + filter
	}
}

// ComposeACLDocument merges the managed rules into an adopted document.
//
// The document keeps every original line verbatim — comments, blank lines
// and directives MCM does not model included. The managed rules are placed
// where Mosquitto expects them:
//
//   - a user-scoped rule joins the section of the matching `user` principal,
//     or opens a new section at the end of the document;
//   - a pattern rule is inserted before the first `user` entry;
//   - an anonymous rule is inserted after the last anonymous `topic` entry,
//     or at the very beginning of the document, so an anonymous line always
//     precedes the first `user` line.
//
// A managed rule whose semantic key already exists in the document is not
// inserted again. The returned warnings name every insertion and every
// skipped duplicate.
func ComposeACLDocument(doc ACLDocument, managed []acl.Rule) (ACLDocument, []string) {
	composed := ACLDocument{EndsWithNewline: doc.EndsWithNewline}
	composed.Entries = append(composed.Entries, doc.Entries...)

	var warnings []string
	existing := make(map[string]struct{})
	for _, entry := range composed.Entries {
		if key := entry.SemanticKey(); key != "" {
			existing[key] = struct{}{}
		}
	}

	for _, rule := range managed {
		key := managedRuleKey(rule)
		if _, ok := existing[key]; ok {
			warnings = append(warnings, fmt.Sprintf("skipped managed rule already present in the ACL source: %s", key))
			continue
		}
		existing[key] = struct{}{}

		normalized := rule.Normalize()
		switch normalized.EffectiveSelector() {
		case acl.SelectorPattern:
			entry := ACLEntry{
				Kind: ACLEntryPattern,
				Rule: &normalized,
				Raw:  normalized.MosquittoPatternACL(),
			}
			composed.Entries = insertACLEntry(composed.Entries, composeInsertIndex(composed.Entries, normalized), entry)
		case acl.SelectorAnonymous:
			entry := ACLEntry{
				Kind: ACLEntryTopic,
				Rule: &normalized,
				Raw:  normalized.MosquittoACL(),
			}
			composed.Entries = insertACLEntry(composed.Entries, composeInsertIndex(composed.Entries, normalized), entry)
		default:
			// A user-scoped rule joins the section of its principal. A
			// principal without a section gets one: the header entry
			// opens the scope the rule entry belongs to.
			index, needsHeader := composeUserInsertIndex(composed.Entries, normalized)
			if needsHeader {
				header := ACLEntry{
					Kind:      ACLEntryUser,
					Principal: normalized.Principal,
					Raw:       "user " + normalized.Principal,
				}
				composed.Entries = insertACLEntry(composed.Entries, index, header)
				index++
			}
			entry := ACLEntry{
				Kind:      ACLEntryTopic,
				Principal: normalized.Principal,
				Rule:      &normalized,
				Raw:       normalized.MosquittoACL(),
			}
			composed.Entries = insertACLEntry(composed.Entries, index, entry)
		}
		warnings = append(warnings, fmt.Sprintf("inserted managed rule into the ACL source: %s", key))
	}

	return composed, warnings
}

// composeUserInsertIndex returns the index at which a user-scoped rule must
// be inserted and reports whether the principal needs a new section header.
func composeUserInsertIndex(entries []ACLEntry, rule acl.Rule) (int, bool) {
	for i, entry := range entries {
		if entry.Kind != ACLEntryUser || entry.Principal != rule.Principal {
			continue
		}
		// The section ends with its last topic entry; the rule joins the
		// other rules of that section.
		insertAt := i + 1
		for j := i + 1; j < len(entries); j++ {
			if entries[j].Kind == ACLEntryUser {
				break
			}
			if entries[j].Kind == ACLEntryTopic && entries[j].Principal == rule.Principal {
				insertAt = j + 1
			}
		}
		return insertAt, false
	}
	// The principal has no section yet: append the section and the rule.
	return len(entries), true
}

// composeInsertIndex returns the index at which a managed rule must be
// inserted into the document.
func composeInsertIndex(entries []ACLEntry, rule acl.Rule) int {
	switch rule.EffectiveSelector() {
	case acl.SelectorPattern:
		for i, entry := range entries {
			if entry.Kind == ACLEntryUser {
				return i
			}
		}
		return len(entries)
	case acl.SelectorAnonymous:
		last := -1
		for i, entry := range entries {
			if entry.Kind == ACLEntryUser {
				break
			}
			if entry.Kind == ACLEntryTopic {
				last = i
			}
		}
		if last < 0 {
			return 0
		}
		return last + 1
	default:
		index, _ := composeUserInsertIndex(entries, rule)
		return index
	}
}

// insertACLEntry inserts an entry at the given index.
func insertACLEntry(entries []ACLEntry, index int, entry ACLEntry) []ACLEntry {
	entries = append(entries, ACLEntry{})
	copy(entries[index+1:], entries[index:])
	entries[index] = entry
	return entries
}
