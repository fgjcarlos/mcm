package catalog

import (
	"sort"
	"strings"
)

// Surface names the MCM control that manages a directive.
type Surface string

const (
	// SurfaceSettings is the catalog-driven general settings form
	// (persistence, queues, sessions and logging).
	SurfaceSettings Surface = "settings"
	// SurfaceListeners is the listener editor (MQTT and WebSocket
	// listeners, including their TLS material and per-listener access
	// directives).
	SurfaceListeners Surface = "listeners"
	// SurfaceBridges is the bridge editor.
	SurfaceBridges Surface = "bridges"
	// SurfacePlugins is the plugin editor and the Dynamic Security
	// adapter.
	SurfacePlugins Surface = "plugins"
	// SurfaceAccess is the MQTT user and ACL editor, which owns the
	// password and ACL files and the directives that point at them.
	SurfaceAccess Surface = "access"
	// SurfaceAdvanced is the advanced root configuration editor. A
	// directive with this surface is edited as raw text and validated
	// against the catalog, not through a dedicated graphical control.
	SurfaceAdvanced Surface = "advanced"
)

// listenerScopeDirectives are the directives the listener editor owns
// directly. They are the directives that describe a listener block
// itself.
var listenerScopeDirectives = map[string]struct{}{
	"listener": {},
	"protocol": {},
}

// accessDirectives are the directives the MQTT user and ACL editor owns.
// The editor renders the password and ACL files and keeps the directives
// that point at them consistent.
var accessDirectives = map[string]struct{}{
	"password_file":         {},
	"acl_file":              {},
	"allow_anonymous":       {},
	"per_listener_settings": {},
}

// pluginDirectives are the directives the plugin editor owns.
var pluginDirectives = map[string]struct{}{
	"plugin":                 {},
	"plugin_opt_config_file": {},
}

// listenerSecurityDirectives are the directives the listener editor
// writes into a listener block when a certificate profile secures it.
// They are global in Mosquitto's grammar but scoped to the listener in
// practice.
var listenerSecurityDirectives = map[string]struct{}{
	"cafile":              {},
	"capath":              {},
	"certfile":            {},
	"keyfile":             {},
	"require_certificate": {},
	"tls_version":         {},
}

// SupportMatrix builds the versioned support matrix of the given broker
// version.
//
// The classification is derived from the catalog and the upstream
// inventory, never hand-written:
//
//   - a directive the catalog assigns a section to is managed by the
//     settings forms;
//   - a directive the catalog scopes to a bridge is managed by the
//     bridge editor;
//   - a directive the catalog scopes to a listener, plus the listener
//     security directives, is managed by the listener editor;
//   - the password, ACL and anonymous-access directives are managed by
//     the MQTT user and ACL editor;
//   - the plugin directives are managed by the plugin editor;
//   - any other directive the catalog knows is validated and preserved
//     but has no dedicated control, so it is partial support: the
//     operator edits it in the advanced root editor;
//   - a directive the upstream manual documents but the catalog does not
//     know is unsupported for that version.
//
// The matrix enumerates the union of the upstream inventory and the
// catalog: a directive MCM manages that the manual does not document —
// a plugin option such as `plugin_opt_config_file` — is still part of
// the support statement, so it appears as managed or partial rather
// than being dropped from the matrix.
func SupportMatrix(cat *Catalog, upstream []string) (*Matrix, error) {
	if cat == nil {
		return nil, ErrUnknownDirective
	}

	matrix := &Matrix{Version: cat.Version}
	seen := map[string]bool{}
	add := func(name string) {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		entry := SupportEntry{Version: cat.Version, Directive: key}
		spec, isKnown := cat.Lookup(key)
		if !isKnown {
			entry.Level = SupportUnsupported
		} else {
			entry.Level, entry.Surface = classify(spec)
		}
		matrix.Entries = append(matrix.Entries, entry)
	}

	for _, name := range upstream {
		add(name)
	}
	// The catalog may know a directive the manual does not document, for
	// example a plugin option. It still belongs in the matrix.
	for _, spec := range cat.Specs() {
		add(spec.Name)
	}
	sort.Slice(matrix.Entries, func(i, j int) bool {
		return matrix.Entries[i].Directive < matrix.Entries[j].Directive
	})
	return matrix, nil
}

// classify returns the support level and the managing surface of one
// catalog directive.
//
// A managed directive names the dedicated control that writes it. A
// partial directive names the advanced root editor, which is where the
// operator edits it.
func classify(spec DirectiveSpec) (SupportLevel, Surface) {
	name := strings.ToLower(spec.Name)
	// A directive with a section is exposed as an editable field of the
	// catalog-driven settings form.
	if spec.Section != "" {
		return SupportManaged, SurfaceSettings
	}
	// A directive the catalog scopes to a bridge belongs to the bridge
	// editor.
	if spec.Scope == ScopeBridge {
		return SupportManaged, SurfaceBridges
	}
	if _, ok := listenerScopeDirectives[name]; ok {
		return SupportManaged, SurfaceListeners
	}
	if _, ok := listenerSecurityDirectives[name]; ok {
		return SupportManaged, SurfaceListeners
	}
	if _, ok := accessDirectives[name]; ok {
		return SupportManaged, SurfaceAccess
	}
	if _, ok := pluginDirectives[name]; ok {
		return SupportManaged, SurfacePlugins
	}
	// The catalog knows the directive, so MCM validates it and preserves
	// it losslessly, but there is no dedicated graphical control for it.
	return SupportPartial, SurfaceAdvanced
}
