package acl

import (
	"fmt"
	"sort"
	"strings"
)

// Access is the action a client wants to perform on a topic.
type Access string

const (
	// AccessRead is the subscribe action. Mosquitto grants it with the
	// "read" permission.
	AccessRead Access = "read"
	// AccessWrite is the publish action. Mosquitto grants it with the
	// "write" permission.
	AccessWrite Access = "write"
)

// IsValid reports whether a is a supported access.
func (a Access) IsValid() bool {
	return a == AccessRead || a == AccessWrite
}

// Identity describes the client an effective-permission question is about.
type Identity struct {
	// Username is the MQTT username of the client. An empty username
	// means the client is anonymous.
	Username string `json:"username,omitempty"`
	// ClientID is the client id of the client. It is the value the %c
	// placeholder substitutes.
	ClientID string `json:"client_id,omitempty"`
}

// IsAnonymous reports whether the identity is an unauthenticated client.
func (i Identity) IsAnonymous() bool {
	return i.Username == ""
}

// Label returns a human readable name of the identity.
func (i Identity) Label() string {
	if i.IsAnonymous() {
		return "anonymous"
	}
	return i.Username
}

// Decision is the answer of the effective-permission resolver.
type Decision struct {
	// Allowed reports whether the access is granted.
	Allowed bool `json:"allowed"`
	// Reason explains the verdict in one sentence.
	Reason string `json:"reason"`
	// MatchingRules are the rules that matched the topic, in the order
	// they were evaluated.
	MatchingRules []Rule `json:"matching_rules,omitempty"`
}

// EffectiveGrant is one grant rule of an identity together with the
// verdict the deny rules have on it.
type EffectiveGrant struct {
	// TopicFilter is the topic filter of the grant.
	TopicFilter string `json:"topic_filter"`
	// Permission is the granted permission.
	Permission Permission `json:"permission"`
	// Selector is the native scope the grant comes from: the user
	// section, the anonymous section or the pattern section.
	Selector Selector `json:"selector"`
	// Principal is the user the grant belongs to. It is empty for
	// anonymous and pattern rules.
	Principal string `json:"principal,omitempty"`
	// Denied reports whether a deny rule covers the whole topic filter.
	Denied bool `json:"denied"`
	// DeniedBy names the deny rules that cover the grant.
	DeniedBy []string `json:"denied_by,omitempty"`
}

// EffectivePermission resolves the access control Mosquitto would apply.
//
// The rules follow the documented Mosquitto 2.0 semantics:
//
//   - the anonymous section applies to unauthenticated clients only, and it
//     is not a fallback for the authenticated ones;
//   - the user section applies to the named user only;
//   - pattern rules apply to every client, with %u and %c substituted;
//   - a deny rule is handled before a grant, so a deny that matches the
//     topic wins over any grant that matches it too.
//
// The client is denied when no rule grants the access.
func EffectivePermission(rules []Rule, identity Identity, topic string, access Access) Decision {
	matched := make([]Rule, 0, 4)
	denied := false
	granted := false
	for _, rule := range applicableRules(rules, identity) {
		filter := ruleFilter(rule, identity)
		if !TopicMatchesFilter(filter, topic) {
			continue
		}
		matched = append(matched, rule)
		if rule.Permission == PermissionDeny {
			denied = true
			continue
		}
		if PermissionGrants(rule.Permission, access) {
			granted = true
		}
	}
	switch {
	case denied:
		return Decision{
			Allowed:       false,
			Reason:        "a deny rule covers the topic",
			MatchingRules: matched,
		}
	case granted:
		return Decision{
			Allowed:       true,
			Reason:        fmt.Sprintf("a %s rule grants %s on the topic", access, access),
			MatchingRules: matched,
		}
	default:
		return Decision{
			Allowed:       false,
			Reason:        fmt.Sprintf("no rule grants %s on the topic", access),
			MatchingRules: matched,
		}
	}
}

// EffectiveGrants returns the grants that apply to an identity together
// with the verdict the deny rules have on them.
//
// A grant is reported as denied when a deny rule of the same identity
// covers its whole topic filter: the deny is handled before the grant, so
// the access the grant describes would never be granted.
func EffectiveGrants(rules []Rule, identity Identity) []EffectiveGrant {
	applicable := applicableRules(rules, identity)
	var denies []Rule
	for _, rule := range applicable {
		if rule.Permission == PermissionDeny {
			denies = append(denies, rule)
		}
	}

	grants := make([]EffectiveGrant, 0, len(applicable))
	for _, rule := range applicable {
		if rule.Permission == PermissionDeny {
			continue
		}
		filter := ruleFilter(rule, identity)
		grant := EffectiveGrant{
			TopicFilter: filter,
			Permission:  rule.Permission,
			Selector:    rule.EffectiveSelector(),
			Principal:   rule.Principal,
		}
		for _, deny := range denies {
			denyFilter := ruleFilter(deny, identity)
			if !FilterCovers(denyFilter, filter) {
				continue
			}
			grant.Denied = true
			grant.DeniedBy = append(grant.DeniedBy, denyFilter)
		}
		grants = append(grants, grant)
	}
	sort.SliceStable(grants, func(i, j int) bool {
		if grants[i].Selector != grants[j].Selector {
			return grants[i].Selector < grants[j].Selector
		}
		if grants[i].Principal != grants[j].Principal {
			return grants[i].Principal < grants[j].Principal
		}
		return grants[i].TopicFilter < grants[j].TopicFilter
	})
	return grants
}

// applicableRules returns the rules that apply to an identity.
//
// The anonymous section applies to unauthenticated clients only, the user
// section applies to the named user only, and the pattern rules apply to
// every client.
func applicableRules(rules []Rule, identity Identity) []Rule {
	out := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		switch rule.EffectiveSelector() {
		case SelectorPattern:
			out = append(out, rule)
		case SelectorAnonymous:
			if identity.IsAnonymous() {
				out = append(out, rule)
			}
		case SelectorUser:
			if !identity.IsAnonymous() && rule.Principal == identity.Username {
				out = append(out, rule)
			}
		}
	}
	return out
}

// ruleFilter returns the topic filter of a rule as it applies to an
// identity. The pattern rules carry the %u and %c placeholders, which
// Mosquitto substitutes with the username and the client id of the
// connecting client.
func ruleFilter(rule Rule, identity Identity) string {
	if rule.EffectiveSelector() != SelectorPattern {
		return rule.TopicFilter
	}
	return SubstitutePlaceholders(rule.TopicFilter, identity)
}

// SubstitutePlaceholders replaces the %u and %c placeholders of a pattern
// topic filter with the username and the client id of a client.
//
// Mosquitto rejects a client whose username or client id contains a
// path separator when pattern rules are in use, so the separator is
// replaced: a client that Mosquitto would refuse can never match a
// pattern rule either.
func SubstitutePlaceholders(filter string, identity Identity) string {
	if !strings.Contains(filter, "%u") && !strings.Contains(filter, "%c") {
		return filter
	}
	levels := strings.Split(filter, "/")
	for idx, level := range levels {
		switch level {
		case "%u":
			levels[idx] = strings.ReplaceAll(identity.Username, "/", "_")
		case "%c":
			levels[idx] = strings.ReplaceAll(identity.ClientID, "/", "_")
		}
	}
	return strings.Join(levels, "/")
}

// PermissionGrants reports whether a permission grants an access.
func PermissionGrants(permission Permission, access Access) bool {
	switch access {
	case AccessRead:
		return permission == PermissionRead || permission == PermissionReadWrite
	case AccessWrite:
		return permission == PermissionWrite || permission == PermissionReadWrite
	default:
		return false
	}
}

// TopicMatchesFilter reports whether an MQTT topic matches a topic filter.
//
// The rules are the ones of MQTT 3.1.1 and 5.0: the multi-level wildcard
// "#" must be the last level and matches the remaining levels (including
// none, so "sport/#" matches "sport"), and the single-level wildcard "+"
// matches exactly one level.
func TopicMatchesFilter(filter, topic string) bool {
	if filter == "" || topic == "" {
		return false
	}
	filterLevels := strings.Split(filter, "/")
	topicLevels := strings.Split(topic, "/")
	idx := 0
	for idx < len(filterLevels) {
		level := filterLevels[idx]
		if level == "#" {
			// "#" is only valid as the last level and matches the rest
			// of the topic, including an empty rest.
			return idx == len(filterLevels)-1
		}
		if idx >= len(topicLevels) {
			return false
		}
		if level != "+" && level != topicLevels[idx] {
			return false
		}
		idx++
	}
	return idx == len(topicLevels)
}

// FilterCovers reports whether every topic matched by grantFilter is also
// matched by denyFilter.
//
// It answers the question a deny rule raises about a grant: a deny that
// covers the whole topic filter of a grant removes the access the grant
// describes, because Mosquitto handles the deny rules before the grants.
func FilterCovers(denyFilter, grantFilter string) bool {
	if denyFilter == "" || grantFilter == "" {
		return false
	}
	denyLevels := strings.Split(denyFilter, "/")
	grantLevels := strings.Split(grantFilter, "/")

	// A deny filter that ends in "#" covers the whole subtree below its
	// prefix.
	if denyLevels[len(denyLevels)-1] == "#" {
		prefix := denyLevels[:len(denyLevels)-1]
		if len(grantLevels) < len(prefix) {
			return false
		}
		for idx, level := range prefix {
			if !levelCovers(level, grantLevels[idx]) {
				return false
			}
		}
		return true
	}

	// Otherwise the deny filter must match the same number of levels and
	// cover every one of them.
	if len(denyLevels) != len(grantLevels) {
		return false
	}
	for idx, level := range denyLevels {
		if !levelCovers(level, grantLevels[idx]) {
			return false
		}
	}
	return true
}

// levelCovers reports whether a level of a deny filter covers a level of a
// grant filter.
func levelCovers(denyLevel, grantLevel string) bool {
	if denyLevel == "+" {
		// "+" matches exactly one level, so it never covers the
		// multi-level wildcard.
		return grantLevel != "#"
	}
	if grantLevel == "#" {
		// The multi-level wildcard is only covered by "#" itself, which
		// the caller handled.
		return false
	}
	return denyLevel == grantLevel
}
