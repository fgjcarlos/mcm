// Package plugins models the Mosquitto plugins MCM manages (issue #304).
//
// Mosquitto loads a plugin with a `plugin` directive and configures it
// with the `plugin_opt_*` directives that follow it:
//
//	plugin /usr/lib/mosquitto_dynamic_security.so
//	plugin_opt_config_file /mosquitto/config/dynamic-security.json
//
// The `plugin_opt_*` options belong to the `plugin` directive that
// precedes them, and Mosquitto reads neither of them again on SIGHUP:
// a plugin is loaded once, at start-up. The catalog therefore marks every
// plugin as restart-required.
//
// The package keeps two things apart:
//
//   - the catalog, which lists the plugins MCM supports for a broker
//     version, and the options of those plugins MCM writes; and
//   - the document, which describes the plugins that are actually
//     configured in the managed mosquitto.conf.
//
// A plugin or an option the catalog does not know is preserved
// byte-for-byte: MCM never drops a directive it does not understand.
package plugins

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnknownPlugin is returned when a broker version has no plugin
// catalog, or when a document names a plugin the catalog does not know.
var ErrUnknownPlugin = errors.New("unknown plugin")

// ReloadKind describes when the broker picks a plugin change up.
//
// Every plugin is loaded at start-up, so a plugin change always needs a
// restart. The field exists so the catalog can say so explicitly instead
// of the apply path guessing.
type ReloadKind string

const (
	// ReloadRestart means the change only takes effect after the broker
	// is restarted. It is the only kind a plugin has.
	ReloadRestart ReloadKind = "restart"
)

// OptionSpec describes one option of a plugin.
//
// Name is the option without the `plugin_opt_` prefix, because that is
// how the plugin receives it.
type OptionSpec struct {
	// Name is the option name as the plugin sees it, without the
	// `plugin_opt_` prefix.
	Name string `yaml:"name" json:"name"`
	// Description explains the option in operator language.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Required marks an option the plugin needs to work. A plugin with a
	// missing required option cannot be enabled.
	Required bool `yaml:"required,omitempty" json:"required,omitempty"`
	// Default is the value MCM writes when the operator does not set one.
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// Placeholder marks an option that carries a path. The UI shows it
	// as a file path instead of a free-text field.
	Placeholder bool `yaml:"placeholder,omitempty" json:"placeholder,omitempty"`
}

// PluginSpec describes one plugin the catalog supports for a broker
// version.
type PluginSpec struct {
	// ID identifies the plugin inside MCM and never changes.
	ID string `yaml:"id" json:"id"`
	// Title is the name the UI shows.
	Title string `yaml:"title" json:"title"`
	// Description explains what the plugin does.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Module is the default path of the shared object the broker loads.
	Module string `yaml:"module" json:"module"`
	// Modules lists the paths the plugin is known to live at. The
	// operator can still set any other path; the list only tells the UI
	// what to suggest.
	Modules []string `yaml:"modules,omitempty" json:"modules,omitempty"`
	// SinceVersion is the earliest broker version that ships the plugin.
	SinceVersion string `yaml:"since_version" json:"since_version"`
	// ReloadKind says when the broker picks the plugin up.
	ReloadKind ReloadKind `yaml:"reload_kind" json:"reload_kind"`
	// Options are the options MCM writes for this plugin.
	Options []OptionSpec `yaml:"options,omitempty" json:"options,omitempty"`
}

// Option returns the spec of one managed option.
func (p PluginSpec) Option(name string) (OptionSpec, bool) {
	for _, opt := range p.Options {
		if strings.EqualFold(opt.Name, name) {
			return opt, true
		}
	}
	return OptionSpec{}, false
}

// ManagedOptionNames returns the names of the options MCM writes, in the
// order the catalog lists them.
func (p PluginSpec) ManagedOptionNames() []string {
	out := make([]string, 0, len(p.Options))
	for _, opt := range p.Options {
		out = append(out, opt.Name)
	}
	return out
}

// Catalog is the immutable set of plugin specs known for one broker
// version.
type Catalog struct {
	Version string
	specs   map[string]PluginSpec
	order   []string
}

// Lookup returns the spec of the plugin with the given id.
func (c *Catalog) Lookup(id string) (PluginSpec, bool) {
	if c == nil {
		return PluginSpec{}, false
	}
	spec, ok := c.specs[strings.ToLower(strings.TrimSpace(id))]
	return spec, ok
}

// Specs returns the plugin specs of the catalog, sorted by id.
func (c *Catalog) Specs() []PluginSpec {
	if c == nil {
		return nil
	}
	out := make([]PluginSpec, 0, len(c.specs))
	for _, spec := range c.specs {
		out = append(out, spec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// VersionName returns the broker version this catalog describes.
func (c *Catalog) VersionName() string {
	if c == nil {
		return ""
	}
	return c.Version
}

// IDs returns the sorted ids of the catalog.
func (c *Catalog) IDs() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.order...)
}

// MatchModule returns the plugin whose module matches the given path.
//
// The match is on the file name, because the directory the plugin lives
// in depends on the distribution: the same plugin is
// `/usr/lib/mosquitto_dynamic_security.so` on Alpine and
// `/usr/lib/x86_64-linux-gnu/mosquitto_dynamic_security.so` on Debian.
func (c *Catalog) MatchModule(path string) (PluginSpec, bool) {
	if c == nil {
		return PluginSpec{}, false
	}
	name := moduleName(path)
	if name == "" {
		return PluginSpec{}, false
	}
	for _, spec := range c.specs {
		if moduleName(spec.Module) == name {
			return spec, true
		}
		for _, candidate := range spec.Modules {
			if moduleName(candidate) == name {
				return spec, true
			}
		}
	}
	return PluginSpec{}, false
}

// moduleName returns the file name of a module path.
func moduleName(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return trimmed
}

// OptionPrefix is the prefix Mosquitto uses for the options of a plugin.
const OptionPrefix = "plugin_opt_"

// DirectiveName returns the option name as it appears in the
// configuration file, with the `plugin_opt_` prefix.
func OptionName(option string) string {
	option = strings.TrimSpace(option)
	if strings.HasPrefix(option, OptionPrefix) {
		return option
	}
	return OptionPrefix + option
}

// StripOptionPrefix removes the `plugin_opt_` prefix from an option
// name.
func StripOptionPrefix(name string) string {
	return strings.TrimPrefix(strings.TrimSpace(name), OptionPrefix)
}

// Validate checks a catalog entry. The catalog is a static artefact, so a
// load-time failure is a bug in the YAML and not something to paper over.
func (p PluginSpec) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("plugin id is required")
	}
	if strings.TrimSpace(p.Title) == "" {
		return fmt.Errorf("plugin %q: title is required", p.ID)
	}
	if strings.TrimSpace(p.Module) == "" {
		return fmt.Errorf("plugin %q: module is required", p.ID)
	}
	if p.ReloadKind == "" {
		return fmt.Errorf("plugin %q: reload_kind is required", p.ID)
	}
	if p.ReloadKind != ReloadRestart {
		return fmt.Errorf("plugin %q: unknown reload_kind %q", p.ID, p.ReloadKind)
	}
	seen := map[string]bool{}
	for _, opt := range p.Options {
		name := strings.TrimSpace(opt.Name)
		if name == "" {
			return fmt.Errorf("plugin %q: option name is required", p.ID)
		}
		if strings.HasPrefix(name, OptionPrefix) {
			return fmt.Errorf("plugin %q: option %q must not carry the %q prefix", p.ID, name, OptionPrefix)
		}
		if seen[strings.ToLower(name)] {
			return fmt.Errorf("plugin %q: duplicate option %q", p.ID, name)
		}
		seen[strings.ToLower(name)] = true
	}
	return nil
}

// ModuleOf returns the default path of the shared object of a plugin.
func (c *Catalog) ModuleOf(id string) string {
	spec, ok := c.Lookup(id)
	if !ok {
		return ""
	}
	return spec.Module
}
