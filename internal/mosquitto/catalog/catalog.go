// Package catalog describes the Mosquitto directives recognised by MCM
// per broker version. The catalog is the input to the version-aware
// validator (issue #298) and the source of truth for which directives
// require a broker restart before a SIGHUP can pick them up.
package catalog

import (
	"errors"
	"fmt"
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
)

// Multiplicity distinguishes directives that must appear once from those
// that may repeat (e.g. multiple log_dest lines).
type Multiplicity string

const (
	Once  Multiplicity = "once"
	Many  Multiplicity = "many"
	OncePerBlock Multiplicity = "once_per_block"
)

// ReloadKind is the broker-side effect of changing a directive. Most
// directives respond to SIGHUP (reload). The catalog marks the ones that
// only take effect after a broker restart so the apply path can refuse
// them without an explicit ?force=1.
type ReloadKind string

const (
	ReloadOK   ReloadKind = "reload"   // picked up by SIGHUP
	Restart    ReloadKind = "restart"  // only by full restart
	ReloadOrRestart ReloadKind = "both" // reload normally, restart on reload_required directive
)

// Scope is the namespace the directive lives in. Most are global; a
// handful only make sense inside a listener block.
type Scope string

const (
	ScopeGlobal    Scope = "global"
	ScopeListener  Scope = "listener"
	ScopeBridge    Scope = "bridge"
)

// DirectiveSpec is the catalog entry for one directive in one Mosquitto
// version. SinceVersion is the earliest broker version that recognises
// the directive; later versions keep the entry and may add new ones.
type DirectiveSpec struct {
	Name           string       `yaml:"name" json:"name"`
	Type           DirectiveType `yaml:"type" json:"type"`
	Scope          Scope        `yaml:"scope" json:"scope"`
	Multiplicity   Multiplicity `yaml:"multiplicity" json:"multiplicity"`
	ReloadKind     ReloadKind   `yaml:"reload_kind" json:"reload_kind"`
	SinceVersion   string       `yaml:"since_version" json:"since_version"`
	AllowedValues  []string     `yaml:"allowed_values,omitempty" json:"allowed_values,omitempty"`
	Deps           []string     `yaml:"deps,omitempty" json:"deps,omitempty"`
	Description    string       `yaml:"description,omitempty" json:"description,omitempty"`
}

// Catalog is the immutable set of directive specs known for one broker
// version. Lookups are O(1) and case-insensitive (Mosquitto itself
// lowercases directive names at parse time).
type Catalog struct {
	Version     string
	specs       map[string]DirectiveSpec
	knownNames  []string
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
	AllowedRel   map[ReloadKind]struct{}
	AllowedMul   map[Multiplicity]struct{}
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
	return nil
}

// DefaultSentinels is the canonical allow-list used by ValidateSentinels.
// It is exported so callers (and tests) can build a tailored validator
// without restating the maps.
var DefaultSentinels = ValidateSentinels{
	AllowedScopes: map[Scope]struct{}{
		ScopeGlobal: {}, ScopeListener: {}, ScopeBridge: {},
	},
	AllowedTypes: map[DirectiveType]struct{}{
		TypeString: {}, TypeInteger: {}, TypeBoolean: {},
		TypeEnum: {}, TypeStringList: {}, TypePath: {},
	},
	AllowedRel: map[ReloadKind]struct{}{
		ReloadOK: {}, Restart: {}, ReloadOrRestart: {},
	},
	AllowedMul: map[Multiplicity]struct{}{
		Once: {}, Many: {}, OncePerBlock: {},
	},
}