// Package catalog describes the Mosquitto directives recognised by MCM
// per broker version. The catalog is the input to the version-aware
// validator (issue #298) and the source of truth for which directives
// require a broker restart before a SIGHUP can pick them up.
package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// DirectiveType describes the value shape expected for one directive.
// Mosquitto itself is lenient about types, but the catalog narrows the
// expected shape so the UI and validator can give actionable errors.
type DirectiveType string

const (
	TypeString     DirectiveType = "string"
	TypeInteger    DirectiveType = "integer"
	TypeBoolean    DirectiveType = "boolean"
	TypeEnum       DirectiveType = "enum"
	TypeStringList DirectiveType = "string_list"
	TypePath       DirectiveType = "path"
	// TypeDuration is an integer followed by a unit suffix, e.g. "2h" or
	// "14d". The accepted suffixes are listed per directive in
	// DurationSuffixes because they differ between broker versions.
	TypeDuration DirectiveType = "duration"
)

// Settings sections group directives into the forms offered by the
// graphical broker editor (issue #302). A directive without a section is
// known to the broker but is not exposed as an editable settings field.
const (
	SectionPersistence = "persistence"
	SectionQueues      = "queues"
	SectionSessions    = "sessions"
	SectionLogging     = "logging"
)

// KnownSections lists every settings section the catalog may assign, in
// the order the graphical editor presents them.
func KnownSections() []string {
	return []string{SectionPersistence, SectionQueues, SectionSessions, SectionLogging}
}

// Multiplicity distinguishes directives that must appear once from those
// that may repeat (e.g. multiple log_dest lines).
type Multiplicity string

const (
	Once         Multiplicity = "once"
	Many         Multiplicity = "many"
	OncePerBlock Multiplicity = "once_per_block"
)

// ReloadKind is the broker-side effect of changing a directive. Most
// directives respond to SIGHUP (reload). The catalog marks the ones that
// only take effect after a broker restart so the apply path can refuse
// them without an explicit ?force=1.
type ReloadKind string

const (
	ReloadOK        ReloadKind = "reload"  // picked up by SIGHUP
	Restart         ReloadKind = "restart" // only by full restart
	ReloadOrRestart ReloadKind = "both"    // reload normally, restart on reload_required directive
)

// Scope is the namespace the directive lives in. Most are global; a
// handful only make sense inside a listener block.
type Scope string

const (
	ScopeGlobal   Scope = "global"
	ScopeListener Scope = "listener"
	ScopeBridge   Scope = "bridge"
	// ScopeAny marks a directive Mosquitto accepts both at global scope
	// and inside a listener block. max_connections is the canonical
	// example: outside a block it configures the default listener,
	// inside one it configures that listener.
	ScopeAny Scope = "any"
)

// DirectiveSpec is the catalog entry for one directive in one Mosquitto
// version. SinceVersion is the earliest broker version that recognises
// the directive; later versions keep the entry and may add new ones.
type DirectiveSpec struct {
	Name         string        `yaml:"name" json:"name"`
	Type         DirectiveType `yaml:"type" json:"type"`
	Scope        Scope         `yaml:"scope" json:"scope"`
	Multiplicity Multiplicity  `yaml:"multiplicity" json:"multiplicity"`
	ReloadKind   ReloadKind    `yaml:"reload_kind" json:"reload_kind"`
	SinceVersion string        `yaml:"since_version" json:"since_version"`
	// ListenerSinceVersion is the earliest broker version that accepts the
	// directive inside a listener block. It is empty when the directive is
	// not available per listener.
	ListenerSinceVersion string   `yaml:"listener_since_version" json:"listener_since_version"`
	AllowedValues        []string `yaml:"allowed_values,omitempty" json:"allowed_values,omitempty"`
	Deps                 []string `yaml:"deps,omitempty" json:"deps,omitempty"`
	Description          string   `yaml:"description,omitempty" json:"description,omitempty"`
	// Section groups the directive into one of the graphical settings
	// forms. It is empty for directives that are recognised by the broker
	// but edited elsewhere (or not editable at all).
	Section string `yaml:"section,omitempty" json:"section,omitempty"`
	// Unit names the unit of an integer value ("seconds", "bytes",
	// "messages"). It is shown next to the field and quoted in
	// validation messages so the operator knows what the number means.
	Unit string `yaml:"unit,omitempty" json:"unit,omitempty"`
	// Min and Max bound an integer directive. A nil bound means the
	// value is unbounded on that side.
	Min *int64 `yaml:"min,omitempty" json:"min,omitempty"`
	Max *int64 `yaml:"max,omitempty" json:"max,omitempty"`
	// Default is the broker default for the directive. It is shown as
	// help text next to the field and marks the value MCM writes when
	// the operator asks for the broker default explicitly.
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// DurationSuffixes lists the unit suffixes a duration directive
	// accepts, e.g. ["h", "d"] for persistent_client_expiration. The
	// suffix set is version specific: Mosquitto 2.0 rejects "30s" while
	// 2.1 accepts it.
	DurationSuffixes []string `yaml:"duration_suffixes,omitempty" json:"duration_suffixes,omitempty"`
}

// Catalog is the immutable set of directive specs known for one broker
// version. Lookups are O(1) and case-insensitive (Mosquitto itself
// lowercases directive names at parse time).
type Catalog struct {
	Version    string
	specs      map[string]DirectiveSpec
	knownNames []string
}

// Lookup returns the spec for directive (lower-cased) and whether it is
// in the catalog. Unknown directives return (zero, false); the validator
// surfaces them as ValidationIssue{Kind: Unknown}.
func (c *Catalog) Lookup(directive string) (DirectiveSpec, bool) {
	if c == nil {
		return DirectiveSpec{}, false
	}
	spec, ok := c.specs[lower(directive)]
	return spec, ok
}

// Specs returns the catalog's directive specs in deterministic order
// (sorted by name). It is the input to the test harness and the
// /api/v1/broker/config/catalog endpoint.
func (c *Catalog) Specs() []DirectiveSpec {
	out := make([]DirectiveSpec, 0, len(c.specs))
	for _, s := range c.specs {
		out = append(out, s)
	}
	// sorted by name — caller-side render is stable.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Names returns the sorted list of catalog names. Used by the validator
// for diagnostics that need to mention all known directives.
func (c *Catalog) Names() []string { return c.knownNames }

// SupportsListenerDirective reports whether the directive can be used
// inside a listener block with this broker version.
//
// A directive is listener-capable when the catalog declares the earliest
// broker version that accepts it in a listener block. Directives without
// that marker are never listener-capable, so a caller can fail closed
// instead of rendering a block the broker would refuse to start.
func (c *Catalog) SupportsListenerDirective(directive string) bool {
	if c == nil {
		return false
	}
	spec, ok := c.Lookup(directive)
	if !ok || spec.ListenerSinceVersion == "" {
		return false
	}
	return versionAtLeast(c.Version, spec.ListenerSinceVersion)
}

// versionAtLeast reports whether version is at least since. Both values
// are dotted numeric versions such as "2.0".
func versionAtLeast(version string, since string) bool {
	current, ok := parseVersionNumber(version)
	if !ok {
		return false
	}
	target, ok := parseVersionNumber(since)
	if !ok {
		return false
	}
	for i := 0; i < len(target); i++ {
		var left, right int
		if i < len(current) {
			left = current[i]
		}
		if i < len(target) {
			right = target[i]
		}
		if left != right {
			return left > right
		}
	}
	return true
}

// parseVersionNumber parses a dotted numeric version.
func parseVersionNumber(version string) ([]int, bool) {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return nil, false
	}
	parts := strings.Split(trimmed, ".")
	numbers := make([]int, 0, len(parts))
	for _, part := range parts {
		number, err := strconv.Atoi(part)
		if err != nil || number < 0 {
			return nil, false
		}
		numbers = append(numbers, number)
	}
	return numbers, true
}

// VersionName returns the broker version this catalog describes.
func (c *Catalog) VersionName() string {
	if c == nil {
		return ""
	}
	return c.Version
}

// ErrUnknownDirective is returned by Load when a YAML file is missing
// or invalid. It is also wrapped in ValidationIssue for individual
// unknown directives inside a parsed file.
var ErrUnknownDirective = errors.New("unknown directive")

// lower is a tiny local alias so this package does not depend on
// strings at import time.
func lower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

// ValidateSentinels validate the catalog once at load time so a typo in
// YAML fails fast instead of producing silent runtime issues.
type ValidateSentinels struct {
	AllowedScopes map[Scope]struct{}
	AllowedTypes  map[DirectiveType]struct{}
	AllowedRel    map[ReloadKind]struct{}
	AllowedMul    map[Multiplicity]struct{}
}

// Validate runs the cross-field sanity checks every catalog entry should
// satisfy. It returns the first failure, not all of them — the catalog
// is a static artefact and a load-time failure should be fixed in the
// YAML, not papered over.
func (s DirectiveSpec) Validate(c ValidateSentinels) error {
	if s.Name == "" {
		return errors.New("directive name is required")
	}
	if _, ok := c.AllowedTypes[s.Type]; !ok {
		return fmt.Errorf("directive %q: unknown type %q", s.Name, s.Type)
	}
	if _, ok := c.AllowedScopes[s.Scope]; !ok {
		return fmt.Errorf("directive %q: unknown scope %q", s.Name, s.Scope)
	}
	if _, ok := c.AllowedRel[s.ReloadKind]; !ok {
		return fmt.Errorf("directive %q: unknown reload_kind %q", s.Name, s.ReloadKind)
	}
	if _, ok := c.AllowedMul[s.Multiplicity]; !ok {
		return fmt.Errorf("directive %q: unknown multiplicity %q", s.Name, s.Multiplicity)
	}
	if s.Type == TypeEnum && len(s.AllowedValues) == 0 {
		return fmt.Errorf("directive %q: enum type requires allowed_values", s.Name)
	}
	if s.ListenerSinceVersion != "" {
		if _, ok := parseVersionNumber(s.ListenerSinceVersion); !ok {
			return fmt.Errorf("directive %q: invalid listener_since_version %q", s.Name, s.ListenerSinceVersion)
		}
	}
	if s.Section != "" && !slices.Contains(KnownSections(), s.Section) {
		return fmt.Errorf("directive %q: unknown section %q", s.Name, s.Section)
	}
	if (s.Min != nil || s.Max != nil) && s.Type != TypeInteger {
		return fmt.Errorf("directive %q: min/max only apply to integer directives", s.Name)
	}
	if s.Min != nil && s.Max != nil && *s.Min > *s.Max {
		return fmt.Errorf("directive %q: min %d is greater than max %d", s.Name, *s.Min, *s.Max)
	}
	if s.Type == TypeDuration {
		if len(s.DurationSuffixes) == 0 {
			return fmt.Errorf("directive %q: duration type requires duration_suffixes", s.Name)
		}
		if s.Min != nil || s.Max != nil {
			return fmt.Errorf("directive %q: min/max do not apply to duration directives", s.Name)
		}
	} else if len(s.DurationSuffixes) > 0 {
		return fmt.Errorf("directive %q: duration_suffixes only apply to duration directives", s.Name)
	}
	return nil
}

// DefaultSentinels is the canonical allow-list used by ValidateSentinels.
// It is exported so callers (and tests) can build a tailored validator
// without restating the maps.
var DefaultSentinels = ValidateSentinels{
	AllowedScopes: map[Scope]struct{}{
		ScopeGlobal: {}, ScopeListener: {}, ScopeBridge: {}, ScopeAny: {},
	},
	AllowedTypes: map[DirectiveType]struct{}{
		TypeString: {}, TypeInteger: {}, TypeBoolean: {},
		TypeEnum: {}, TypeStringList: {}, TypePath: {}, TypeDuration: {},
	},
	AllowedRel: map[ReloadKind]struct{}{
		ReloadOK: {}, Restart: {}, ReloadOrRestart: {},
	},
	AllowedMul: map[Multiplicity]struct{}{
		Once: {}, Many: {}, OncePerBlock: {},
	},
}
