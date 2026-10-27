package plugins

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

// Issue kinds.
const (
	// IssueKindUnknownPlugin marks a plugin the catalog does not know.
	IssueKindUnknownPlugin = "unknown_plugin"
	// IssueKindDuplicate marks a plugin listed more than once.
	IssueKindDuplicate = "duplicate"
	// IssueKindMissingOption marks a required option the document does
	// not set.
	IssueKindMissingOption = "missing_option"
	// IssueKindValue marks an option value the plugin cannot use.
	IssueKindValue = "value"
)

// Issue describes one problem with a plugin document. The validator never
// returns an error; every problem is an Issue so the operator sees all of
// them at once.
type Issue struct {
	// Kind groups the finding: "unknown_plugin", "duplicate",
	// "missing_option" or "value".
	Kind string `json:"kind"`
	// Plugin is the id of the plugin the finding belongs to.
	Plugin string `json:"plugin,omitempty"`
	// Option is the option the finding is about.
	Option string `json:"option,omitempty"`
	// Message explains the finding in operator language.
	Message string `json:"message"`
}

// HasErrors reports whether the findings contain at least one problem.
func HasErrors(issues []Issue) bool {
	return len(issues) > 0
}

// Plugin is one plugin configured in the managed configuration file.
//
// ID is the catalog id of the plugin ("dynamic-security"). It is empty
// for a plugin the catalog does not know: such a plugin is preserved
// byte-for-byte and never edited by MCM.
type Plugin struct {
	// ID identifies the plugin inside MCM. Empty for an unknown plugin.
	ID string `json:"id,omitempty"`
	// Module is the path of the shared object the broker loads.
	Module string `json:"module"`
	// Options are the options MCM writes for this plugin, keyed by the
	// option name without the `plugin_opt_` prefix.
	Options map[string]string `json:"options,omitempty"`
	// UnknownOptions are the `plugin_opt_*` options the catalog does not
	// know. They are preserved byte-for-byte.
	UnknownOptions map[string]string `json:"unknown_options,omitempty"`
}

// Known reports whether the catalog manages this plugin.
func (p Plugin) Known() bool { return strings.TrimSpace(p.ID) != "" }

// Document is the set of plugins the operator is editing.
//
// The document is the desired state of the plugin section of the
// configuration file: the operator sends the plugins they want, and MCM
// replaces the plugin directives of the file with them. Everything else
// in the file is preserved.
type Document struct {
	// Plugins are the configured plugins, in configuration order.
	Plugins []Plugin `json:"plugins"`
}

// Clone returns a deep copy of the document.
func (d Document) Clone() Document {
	out := Document{Plugins: make([]Plugin, 0, len(d.Plugins))}
	for _, p := range d.Plugins {
		clone := p
		clone.Options = copyMap(p.Options)
		clone.UnknownOptions = copyMap(p.UnknownOptions)
		out.Plugins = append(out.Plugins, clone)
	}
	return out
}

func copyMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// sortedKeys returns the keys of a map in a stable order, so a rendered
// configuration does not depend on the order the map happens to have.
func sortedKeys(in map[string]string) []string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Find returns the plugin with the given catalog id.
func (d Document) Find(id string) (Plugin, bool) {
	for _, p := range d.Plugins {
		if strings.EqualFold(p.ID, id) {
			return p, true
		}
	}
	return Plugin{}, false
}

// Managed returns the plugins the catalog knows.
func (d Document) Managed() []Plugin {
	out := make([]Plugin, 0, len(d.Plugins))
	for _, p := range d.Plugins {
		if p.Known() {
			out = append(out, p)
		}
	}
	return out
}

// group is one `plugin` directive with the `plugin_opt_*` directives that
// belong to it.
type group struct {
	// moduleIndex is the index of the `plugin` item.
	moduleIndex int
	// module is the path the `plugin` directive carries.
	module string
	// optionIndexes maps an option name (without the prefix) to the
	// index of its item.
	optionIndexes map[string]int
	// optionOrder keeps the options in the order of the file.
	optionOrder []string
}

// lastOptionIndex returns the index of the last option of the group, or
// the index of the `plugin` directive when the group has no option yet.
func (g *group) lastOptionIndex() int {
	last := g.moduleIndex
	for _, name := range g.optionOrder {
		if idx := g.optionIndexes[name]; idx > last {
			last = idx
		}
	}
	return last
}

// groups finds the plugin groups of a parsed file.
//
// A `plugin_opt_*` directive belongs to the `plugin` directive that
// precedes it, which is how Mosquitto itself reads them.
func groups(f *conf.File) []*group {
	if f == nil {
		return nil
	}
	var (
		out    []*group
		active *group
	)
	for i := range f.Items {
		it := &f.Items[i]
		if it.Kind != conf.ItemDirective {
			continue
		}
		switch {
		case it.Key == "plugin":
			if len(it.Values) == 0 {
				active = nil
				continue
			}
			active = &group{
				moduleIndex:   i,
				module:        it.Values[0],
				optionIndexes: map[string]int{},
			}
			out = append(out, active)
		case strings.HasPrefix(it.Key, OptionPrefix):
			if active == nil {
				continue
			}
			name := StripOptionPrefix(it.Key)
			if _, seen := active.optionIndexes[name]; seen {
				continue
			}
			active.optionIndexes[name] = i
			active.optionOrder = append(active.optionOrder, name)
		}
	}
	return out
}

// Extract reads the plugin configuration of a parsed file.
//
// Every `plugin` directive becomes one entry, whether the catalog knows
// it or not. The options of a known plugin are split into the ones MCM
// writes and the ones it only preserves.
func Extract(f *conf.File, cat *Catalog) Document {
	doc := Document{}
	for _, g := range groups(f) {
		entry := Plugin{Module: g.module}
		if cat != nil {
			if spec, ok := cat.MatchModule(g.module); ok {
				entry.ID = spec.ID
			}
		}
		for _, name := range g.optionOrder {
			value := optionValue(f, g, name)
			if entry.Known() {
				if spec, ok := cat.Lookup(entry.ID); ok {
					if _, managed := spec.Option(name); managed {
						if entry.Options == nil {
							entry.Options = map[string]string{}
						}
						entry.Options[name] = value
						continue
					}
				}
			}
			if entry.UnknownOptions == nil {
				entry.UnknownOptions = map[string]string{}
			}
			entry.UnknownOptions[name] = value
		}
		doc.Plugins = append(doc.Plugins, entry)
	}
	return doc
}

// optionValue returns the value of one option of a group.
func optionValue(f *conf.File, g *group, name string) string {
	idx, ok := g.optionIndexes[name]
	if !ok {
		return ""
	}
	it := f.Items[idx]
	if len(it.Values) == 0 {
		return ""
	}
	return strings.Join(it.Values, " ")
}

// Validate checks a plugin document against the catalog.
//
// It never returns an error; every problem is an Issue so the operator
// sees all of them at once.
func Validate(doc Document, cat *Catalog) []Issue {
	var issues []Issue
	if cat == nil {
		return issues
	}
	seen := map[string]bool{}
	for _, p := range doc.Plugins {
		if !p.Known() {
			// An unknown plugin is preserved as it is. MCM cannot
			// judge what it needs, so it does not refuse it.
			if strings.TrimSpace(p.Module) == "" {
				issues = append(issues, Issue{
					Kind:    IssueKindValue,
					Message: "the plugin does not declare the shared object to load",
				})
			}
			if strings.ContainsAny(p.Module, "\n\r") {
				issues = append(issues, Issue{
					Kind:    IssueKindValue,
					Message: "the shared object of the plugin must be a single line",
				})
			}
			// The options of a plugin the catalog does not know are
			// written as they are, so they have to be a directive
			// MCM can render without breaking the file.
			for _, name := range sortedKeys(p.UnknownOptions) {
				if !validOptionName(name) {
					issues = append(issues, Issue{
						Kind:    IssueKindValue,
						Plugin:  p.Module,
						Option:  name,
						Message: fmt.Sprintf("the option %q of the plugin %q is not a valid directive name", name, p.Module),
					})
				}
				if strings.ContainsAny(p.UnknownOptions[name], "\n\r") {
					issues = append(issues, Issue{
						Kind:    IssueKindValue,
						Plugin:  p.Module,
						Option:  name,
						Message: fmt.Sprintf("the option %q of the plugin %q must be a single line", name, p.Module),
					})
				}
			}
			continue
		}
		spec, ok := cat.Lookup(p.ID)
		if !ok {
			issues = append(issues, Issue{
				Kind:    IssueKindUnknownPlugin,
				Plugin:  p.ID,
				Message: fmt.Sprintf("%q is not a supported plugin for Mosquitto %s", p.ID, cat.VersionName()),
			})
			continue
		}
		if seen[strings.ToLower(spec.ID)] {
			issues = append(issues, Issue{
				Kind:    IssueKindDuplicate,
				Plugin:  spec.ID,
				Message: fmt.Sprintf("the plugin %q is listed more than once", spec.ID),
			})
			continue
		}
		seen[strings.ToLower(spec.ID)] = true
		if strings.TrimSpace(p.Module) == "" {
			issues = append(issues, Issue{
				Kind:    IssueKindValue,
				Plugin:  spec.ID,
				Message: fmt.Sprintf("the plugin %q does not declare the shared object to load", spec.ID),
			})
		}
		for _, opt := range spec.Options {
			value, ok := p.Options[opt.Name]
			if !ok || strings.TrimSpace(value) == "" {
				if opt.Required {
					issues = append(issues, Issue{
						Kind:    IssueKindMissingOption,
						Plugin:  spec.ID,
						Option:  opt.Name,
						Message: fmt.Sprintf("the plugin %q needs the option %q", spec.ID, OptionName(opt.Name)),
					})
				}
				continue
			}
			if strings.ContainsAny(value, "\n\r") {
				issues = append(issues, Issue{
					Kind:    IssueKindValue,
					Plugin:  spec.ID,
					Option:  opt.Name,
					Message: fmt.Sprintf("the option %q of the plugin %q must be a single line", OptionName(opt.Name), spec.ID),
				})
			}
		}
	}
	return issues
}

// validOptionName reports whether an option name can be used as the name
// of a directive in the configuration file.
//
// Mosquitto reads a directive name as a single token, so a name with a
// space or a newline would break the file.
func validOptionName(name string) bool {
	name = strings.TrimSpace(StripOptionPrefix(name))
	if name == "" {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}

// pluginMatch records which existing group a desired plugin was matched
// to, and the module path it must carry afterwards.
type pluginMatch struct {
	// groupIndex is the index into the existing groups, or -1 when the
	// plugin is new.
	groupIndex int
	// module is the path the `plugin` directive must carry.
	module string
	// new marks a plugin that is not in the file yet.
	new bool
}

// Apply writes the desired plugin document into a copy of the parsed
// file.
//
// The returned file is a copy: the input is never modified. The second
// return value lists the directives whose value changed, which is what
// decides between a SIGHUP and a full restart.
//
// A plugin or an option the catalog does not know is preserved
// byte-for-byte: MCM never drops a directive it does not understand.
func Apply(f *conf.File, cat *Catalog, doc Document) (*conf.File, []string, error) {
	if f == nil {
		return nil, nil, errors.New("plugins: no configuration file")
	}
	if cat == nil {
		return nil, nil, errors.New("plugins: no catalog")
	}
	if issues := Validate(doc, cat); HasErrors(issues) {
		return nil, nil, &ValidationError{Issues: issues}
	}

	out := &conf.File{Path: f.Path, Items: make([]conf.Item, len(f.Items))}
	copy(out.Items, f.Items)
	for i := range out.Items {
		out.Items[i].Values = append([]string(nil), out.Items[i].Values...)
	}

	existing := groups(out)
	claimed := make([]bool, len(existing))
	matches := make([]pluginMatch, len(doc.Plugins))

	for i, p := range doc.Plugins {
		if !p.Known() {
			// An unknown plugin keeps the module path the file has.
			// It is matched by that path, so the options of the
			// plugin are never touched. A plugin the file does not
			// carry yet is added with the options the document
			// declares.
			found := false
			for gi, g := range existing {
				if claimed[gi] {
					continue
				}
				if g.module == strings.TrimSpace(p.Module) {
					claimed[gi] = true
					matches[i] = pluginMatch{groupIndex: gi, module: g.module}
					found = true
					break
				}
			}
			if !found {
				matches[i] = pluginMatch{groupIndex: -1, module: strings.TrimSpace(p.Module), new: true}
			}
			continue
		}
		spec, ok := cat.Lookup(p.ID)
		if !ok {
			return nil, nil, fmt.Errorf("plugins: %q is not a supported plugin", p.ID)
		}
		module := strings.TrimSpace(p.Module)
		if module == "" {
			module = spec.Module
		}
		found := false
		for gi, g := range existing {
			if claimed[gi] {
				continue
			}
			matched, isPlugin := cat.MatchModule(g.module)
			if !isPlugin || !strings.EqualFold(matched.ID, spec.ID) {
				continue
			}
			claimed[gi] = true
			matches[i] = pluginMatch{groupIndex: gi, module: module}
			found = true
			break
		}
		if !found {
			matches[i] = pluginMatch{groupIndex: -1, module: module, new: true}
		}
	}

	changed := map[string]bool{}
	rewrite := map[int]conf.Item{}
	remove := map[int]bool{}
	insertAfter := map[int][]conf.Item{}

	for i, p := range doc.Plugins {
		m := matches[i]
		if m.new {
			continue
		}
		g := existing[m.groupIndex]
		if m.module != g.module {
			item := out.Items[g.moduleIndex]
			item.Values = []string{m.module}
			item.Text = renderDirective(directiveName(item.Text, "plugin"), []string{m.module}, item.Indent)
			rewrite[g.moduleIndex] = item
			changed["plugin"] = true
		}
		if !p.Known() {
			// Unknown plugin: the options are preserved as they are.
			continue
		}
		spec, _ := cat.Lookup(p.ID)
		desired := map[string]string{}
		for _, opt := range spec.Options {
			value, ok := p.Options[opt.Name]
			if !ok || strings.TrimSpace(value) == "" {
				continue
			}
			desired[opt.Name] = value
		}
		// Remove the managed options the document does not set any more.
		for _, name := range g.optionOrder {
			if _, managed := spec.Option(name); !managed {
				continue
			}
			if _, keep := desired[name]; keep {
				continue
			}
			remove[g.optionIndexes[name]] = true
			changed[OptionName(name)] = true
		}
		// Write the managed options the document sets.
		for _, opt := range spec.Options {
			value, ok := desired[opt.Name]
			if !ok {
				continue
			}
			idx, present := g.optionIndexes[opt.Name]
			if present {
				item := out.Items[idx]
				if optionValue(out, g, opt.Name) == value {
					continue
				}
				item.Values = []string{value}
				item.Text = renderDirective(directiveName(item.Text, OptionName(opt.Name)), []string{value}, item.Indent)
				rewrite[idx] = item
				changed[OptionName(opt.Name)] = true
				continue
			}
			newItem := conf.Item{
				Kind:   conf.ItemDirective,
				Key:    OptionName(opt.Name),
				Values: []string{value},
				Line:   -1,
			}
			newItem.Text = renderDirective(OptionName(opt.Name), []string{value}, 0)
			insertAfter[g.lastOptionIndex()] = append(insertAfter[g.lastOptionIndex()], newItem)
			changed[OptionName(opt.Name)] = true
		}
	}

	// Drop the groups no desired plugin claimed: the operator removed
	// them from the document.
	//
	// A plugin the catalog does not know is never dropped: MCM does not
	// manage it, so it does not remove it either. The operator who runs
	// a plugin MCM knows nothing about keeps it working.
	for gi, g := range existing {
		if claimed[gi] {
			continue
		}
		if _, known := cat.MatchModule(g.module); !known {
			continue
		}
		remove[g.moduleIndex] = true
		changed["plugin"] = true
		for _, name := range g.optionOrder {
			remove[g.optionIndexes[name]] = true
			changed[OptionName(name)] = true
		}
	}

	// Build the blocks of the plugins that are new.
	var newBlocks []conf.Item
	for i, p := range doc.Plugins {
		m := matches[i]
		if !m.new {
			continue
		}
		moduleItem := conf.Item{
			Kind:   conf.ItemDirective,
			Key:    "plugin",
			Values: []string{m.module},
			Line:   -1,
		}
		moduleItem.Text = renderDirective("plugin", []string{m.module}, 0)
		block := []conf.Item{moduleItem}
		changed["plugin"] = true
		if p.Known() {
			spec, _ := cat.Lookup(p.ID)
			for _, opt := range spec.Options {
				value, ok := p.Options[opt.Name]
				if !ok || strings.TrimSpace(value) == "" {
					continue
				}
				item := conf.Item{
					Kind:   conf.ItemDirective,
					Key:    OptionName(opt.Name),
					Values: []string{value},
					Line:   -1,
				}
				item.Text = renderDirective(OptionName(opt.Name), []string{value}, 0)
				block = append(block, item)
				changed[OptionName(opt.Name)] = true
			}
		} else {
			// The options of a plugin the catalog does not know are
			// written as they are: MCM does not manage them, so it
			// does not rename them either.
			for _, name := range sortedKeys(p.UnknownOptions) {
				value := p.UnknownOptions[name]
				if strings.TrimSpace(value) == "" {
					continue
				}
				item := conf.Item{
					Kind:   conf.ItemDirective,
					Key:    OptionName(name),
					Values: []string{value},
					Line:   -1,
				}
				item.Text = renderDirective(OptionName(name), []string{value}, 0)
				block = append(block, item)
				changed[OptionName(name)] = true
			}
		}
		newBlocks = append(newBlocks, block...)
	}

	// Rebuild the item list with the edits applied.
	items := make([]conf.Item, 0, len(out.Items)+len(newBlocks))
	for i := range out.Items {
		if remove[i] {
			continue
		}
		if item, ok := rewrite[i]; ok {
			items = append(items, item)
		} else {
			items = append(items, out.Items[i])
		}
		if extra, ok := insertAfter[i]; ok {
			items = append(items, extra...)
		}
	}
	if len(newBlocks) > 0 {
		at := insertionIndex(items)
		merged := make([]conf.Item, 0, len(items)+len(newBlocks))
		merged = append(merged, items[:at]...)
		merged = append(merged, newBlocks...)
		merged = append(merged, items[at:]...)
		items = merged
	}
	out.Items = items

	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	return out, names, nil
}

// ValidationError carries the issues of a plugin document the catalog
// rejected.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	if len(e.Issues) == 0 {
		return "plugin configuration is invalid"
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		parts = append(parts, issue.Message)
	}
	return "plugin configuration is invalid: " + strings.Join(parts, "; ")
}

// directiveName returns the spelling of the directive used in the
// original line so a rewritten line keeps the casing of the file.
func directiveName(text, fallback string) string {
	fields := strings.Fields(text)
	if len(fields) > 0 {
		return fields[0]
	}
	return fallback
}

// renderDirective builds the line for a directive occurrence.
func renderDirective(name string, values []string, indent int) string {
	var b strings.Builder
	for i := 0; i < indent; i++ {
		b.WriteByte(' ')
	}
	b.WriteString(name)
	for _, v := range values {
		b.WriteByte(' ')
		b.WriteString(v)
	}
	b.WriteString("\n")
	return b.String()
}

// insertionIndex returns the position new directives are written to: right
// after the last global directive, so they stay in the global part of the
// file and never end up inside a listener.
func insertionIndex(items []conf.Item) int {
	last := -1
	for i := range items {
		it := &items[i]
		switch it.Kind {
		case conf.ItemDirective:
			if it.Key == "listener" || it.Key == "bridge" || it.Key == "connection" {
				return last + 1
			}
			last = i
		case conf.ItemBlockOpen:
			return last + 1
		}
	}
	return last + 1
}
