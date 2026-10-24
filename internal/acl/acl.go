package acl

import (
	"fmt"
	"strings"
	"unicode"
)

// Selector identifies the native Mosquitto ACL scope a rule applies to.
//
// A Mosquitto 2.0 ACL file distinguishes three scopes:
//
//   - user: the entry applies to the named username. This is the scope MCM
//     used before native selectors existed.
//   - anonymous: the entry applies to unauthenticated clients only. In a
//     Mosquitto ACL file these are the `topic` lines that appear before the
//     first `user` line.
//   - pattern: the entry applies to every user inside the ACL. In a
//     Mosquitto ACL file these are the `pattern` lines, which stay globally
//     applicable no matter which `user` section surrounds them.
type Selector string

const (
	// SelectorUser binds a rule to the rule principal (an MQTT username).
	SelectorUser Selector = "user"
	// SelectorAnonymous binds a rule to anonymous (unauthenticated) clients.
	SelectorAnonymous Selector = "anonymous"
	// SelectorPattern binds a rule to every user inside the ACL.
	SelectorPattern Selector = "pattern"
)

// Scope identifies the ACL file a rule is rendered into.
//
// Mosquitto resolves the access control per listener once
// `per_listener_settings true` is set: an `acl_file` declared inside a
// `listener` block applies to that listener only, and a listener block
// that declares no ACL file has no access control at all. MCM therefore
// writes the broker-wide ACL file into every listener that does not own a
// scoped file, so the same distinction holds:
//
//   - global: the rule is rendered into the broker-wide ACL file. It
//     applies to every listener.
//   - listener: the rule is rendered into the ACL file of exactly one
//     listener. It applies to that listener only.
type Scope string

const (
	// ScopeGlobal renders the rule into the broker-wide ACL file. Rules
	// stored before scoped rules existed carry an empty scope, which
	// means the global scope.
	ScopeGlobal Scope = "global"
	// ScopeListener renders the rule into the ACL file of one listener.
	ScopeListener Scope = "listener"
)

// validScopes is the set of scopes a rule may declare.
var validScopes = map[Scope]struct{}{
	ScopeGlobal:   {},
	ScopeListener: {},
}

// Permission describes the allowed MQTT action for an ACL rule.
type Permission string

const (
	PermissionRead      Permission = "read"
	PermissionWrite     Permission = "write"
	PermissionReadWrite Permission = "readwrite"
	// PermissionDeny denies the action. Mosquitto 2.0 introduced it.
	PermissionDeny Permission = "deny"
)

var validPermissions = map[Permission]struct{}{
	PermissionRead:      {},
	PermissionWrite:     {},
	PermissionReadWrite: {},
	PermissionDeny:      {},
}

// SupportedMosquittoVersion is the Mosquitto runtime MCM manages. Version
// dependent native ACL features are validated against it. MCM does not
// discover the runtime version of a broker on its own.
const SupportedMosquittoVersion = "2.0"

// MinimumDenyPermissionVersion is the earliest Mosquitto version that
// understands the "deny" ACL permission.
const MinimumDenyPermissionVersion = "2.0"

// Rule represents one ACL entry managed by MCM.
type Rule struct {
	ID string `json:"id"`
	// Selector chooses the native Mosquitto scope of the rule. An empty
	// selector means the legacy user scope, so rules stored before native
	// selectors existed keep their meaning.
	Selector Selector `json:"selector,omitempty"`
	// Scope chooses the ACL file the rule is rendered into. An empty scope
	// means the global scope, so rules stored before scoped rules existed
	// keep their meaning.
	Scope Scope `json:"scope,omitempty"`
	// ListenerID names the listener whose ACL file carries the rule. It is
	// required for the listener scope and must be empty for the global
	// scope.
	ListenerID  string     `json:"listener_id,omitempty"`
	Principal   string     `json:"principal"`
	TopicFilter string     `json:"topic_filter"`
	Permission  Permission `json:"permission"`
	Description string     `json:"description,omitempty"`
}

// ValidationError holds all ACL validation failures.
type ValidationError struct {
	Problems []string `json:"details"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("acl validation failed: %s", strings.Join(e.Problems, "; "))
}

// EffectiveSelector returns the selector the rule applies under. Rules
// stored before native selectors existed carry an empty selector, which
// means the legacy user scope.
func (r Rule) EffectiveSelector() Selector {
	if r.Selector == "" {
		return SelectorUser
	}
	return r.Selector
}

// EffectiveScope returns the scope the rule applies under. Rules stored
// before scoped rules existed carry an empty scope, which means the global
// scope.
func (r Rule) EffectiveScope() Scope {
	if r.Scope == "" {
		return ScopeGlobal
	}
	return r.Scope
}

// IsListenerScoped reports whether the rule is rendered into the ACL file
// of a single listener instead of the broker-wide ACL file.
func (r Rule) IsListenerScoped() bool {
	return r.EffectiveScope() == ScopeListener
}

// IsUserScoped reports whether the rule is bound to an MQTT username. Only
// user-scoped rules follow MQTT user renames and can become orphans when
// their user disappears.
func (r Rule) IsUserScoped() bool {
	return r.EffectiveSelector() == SelectorUser
}

// Normalize returns the rule with its selector and scope resolved to
// explicit values. Stores persist the normalized rule so a legacy rule and
// an explicitly user-scoped, globally scoped rule are stored identically.
func (r Rule) Normalize() Rule {
	r.Selector = r.EffectiveSelector()
	r.Scope = r.EffectiveScope()
	if r.Scope != ScopeListener {
		// A global rule never carries a listener reference; dropping it
		// keeps the stored row unambiguous.
		r.ListenerID = ""
	}
	return r
}

// ValidateRule validates a rule before it is persisted against the
// Mosquitto version MCM supports.
func ValidateRule(rule Rule) error {
	return ValidateRuleForMosquittoVersion(rule, SupportedMosquittoVersion)
}

// ValidateRuleForMosquittoVersion validates a rule against a specific
// Mosquitto version. It exists so a version dependent native feature
// (today: the "deny" permission) can be checked without inventing runtime
// version discovery.
func ValidateRuleForMosquittoVersion(rule Rule, version string) error {
	var problems []string

	switch rule.EffectiveSelector() {
	case SelectorUser:
		if strings.TrimSpace(rule.Principal) == "" {
			problems = append(problems, "principal is required")
		} else if containsControlCharacter(rule.Principal) {
			problems = append(problems, "principal must not contain control characters")
		}
	case SelectorAnonymous:
		if rule.Principal != "" {
			problems = append(problems, "principal must be empty for anonymous rules")
		}
	case SelectorPattern:
		if rule.Principal != "" {
			problems = append(problems, "principal must be empty for pattern rules")
		}
	default:
		problems = append(problems, `selector must be one of: "user", "anonymous", "pattern"`)
	}

	switch rule.EffectiveScope() {
	case ScopeGlobal:
		if rule.ListenerID != "" {
			problems = append(problems, "listener_id must be empty for global rules")
		}
	case ScopeListener:
		if err := ValidateListenerID(rule.ListenerID); err != nil {
			problems = append(problems, err.Error())
		}
	default:
		problems = append(problems, `scope must be one of: "global", "listener"`)
	}

	if strings.TrimSpace(rule.TopicFilter) == "" {
		problems = append(problems, "topic_filter is required")
	} else if err := ValidateTopicFilter(rule.TopicFilter); err != nil {
		problems = append(problems, err.Error())
	} else if rule.EffectiveSelector() == SelectorPattern {
		// Placeholder substitution is a pattern-only feature: Mosquitto
		// replaces %u/%c when it evaluates a pattern entry. A user-scoped
		// topic filter keeps a literal %u/%c untouched.
		if err := ValidatePatternPlaceholders(rule.TopicFilter); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if _, ok := validPermissions[rule.Permission]; !ok {
		problems = append(problems, `permission must be one of: "read", "write", "readwrite", "deny"`)
	} else if rule.Permission == PermissionDeny && !mosquittoVersionAtLeast(version, MinimumDenyPermissionVersion) {
		problems = append(problems, fmt.Sprintf("permission %q requires mosquitto %s or later", PermissionDeny, MinimumDenyPermissionVersion))
	}

	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}

	return nil
}

// ValidateTopicFilter validates an MQTT topic filter with MQTT wildcard rules.
func ValidateTopicFilter(filter string) error {
	if filter == "" {
		return fmt.Errorf("topic_filter is required")
	}
	if strings.ContainsRune(filter, '\x00') {
		return fmt.Errorf("topic_filter must not contain NUL")
	}
	if containsControlCharacter(filter) {
		return fmt.Errorf("topic_filter must not contain control characters")
	}

	levels := strings.Split(filter, "/")
	for idx, level := range levels {
		if strings.Contains(level, "#") {
			if level != "#" {
				return fmt.Errorf(`topic_filter %q is invalid: "#" must occupy an entire topic level`, filter)
			}
			if idx != len(levels)-1 {
				return fmt.Errorf(`topic_filter %q is invalid: "#" must only appear in the final topic level`, filter)
			}
		}

		if strings.Contains(level, "+") && level != "+" {
			return fmt.Errorf(`topic_filter %q is invalid: "+" must occupy an entire topic level`, filter)
		}
	}

	return nil
}

// ValidatePatternPlaceholders validates the %u and %c placeholders of a
// pattern topic filter. Mosquitto substitutes the connecting username (%u)
// and the client id (%c) when it evaluates a pattern entry, and the
// documented form places every placeholder on its own topic level.
func ValidatePatternPlaceholders(filter string) error {
	for _, level := range strings.Split(filter, "/") {
		if !strings.Contains(level, "%u") && !strings.Contains(level, "%c") {
			continue
		}
		if level != "%u" && level != "%c" {
			return fmt.Errorf(`topic_filter %q is invalid: "%%u" and "%%c" must each occupy an entire topic level`, filter)
		}
	}
	return nil
}

// maxListenerIDLength bounds the listener reference so the derived ACL file
// name stays well inside the limits of every filesystem MCM supports.
const maxListenerIDLength = 64

// ValidateListenerID validates the listener reference of a listener-scoped
// rule. The reference ends up in the name of the ACL file MCM writes for
// that listener, so it must not be able to escape the ACL directory.
func ValidateListenerID(id string) error {
	if id == "" {
		return fmt.Errorf("listener_id is required for listener scoped rules")
	}
	if len(id) > maxListenerIDLength {
		return fmt.Errorf("listener_id must be at most %d characters", maxListenerIDLength)
	}
	if containsControlCharacter(id) {
		return fmt.Errorf("listener_id must not contain control characters")
	}
	if strings.ContainsAny(id, `/\`) {
		return fmt.Errorf("listener_id must not contain path separators")
	}
	if id == "." || id == ".." {
		return fmt.Errorf("listener_id %q is not a valid listener reference", id)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return fmt.Errorf("listener_id %q must only contain letters, digits, '-', '_' and '.'", id)
		}
	}
	return nil
}

// mosquittoVersionAtLeast reports whether a dotted numeric Mosquitto
// version is greater than or equal to a minimum version. An unparsable
// version never satisfies the minimum.
func mosquittoVersionAtLeast(version string, minimum string) bool {
	current, ok := parseMosquittoVersion(version)
	if !ok {
		return false
	}
	required, ok := parseMosquittoVersion(minimum)
	if !ok {
		return false
	}
	for idx := 0; idx < len(current) || idx < len(required); idx++ {
		var currentPart, requiredPart int
		if idx < len(current) {
			currentPart = current[idx]
		}
		if idx < len(required) {
			requiredPart = required[idx]
		}
		if currentPart != requiredPart {
			return currentPart > requiredPart
		}
	}
	return true
}

// parseMosquittoVersion parses a dotted numeric version such as "2.0".
func parseMosquittoVersion(version string) ([]int, bool) {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return nil, false
	}
	parts := strings.Split(trimmed, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number := 0
		if part == "" {
			return nil, false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return nil, false
			}
			number = number*10 + int(r-'0')
		}
		numbers = append(numbers, number)
	}
	return numbers, true
}

func containsControlCharacter(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// MosquittoACL returns the Mosquitto ACL line for a rule. User-scoped and
// anonymous rules both render as `topic` lines; the section they are
// rendered in carries the difference.
func (r Rule) MosquittoACL() string {
	return fmt.Sprintf("topic %s %s", r.Permission, r.TopicFilter)
}

// MosquittoPatternACL returns the Mosquitto pattern ACL line for a rule.
// Pattern entries apply to every user inside the ACL.
func (r Rule) MosquittoPatternACL() string {
	return fmt.Sprintf("pattern %s %s", r.Permission, r.TopicFilter)
}
