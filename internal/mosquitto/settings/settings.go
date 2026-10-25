// Package settings exposes the general broker settings of mosquitto.conf
// as an editable document (issue #302).
//
// The document is a flat list of directive occurrences, so it maps one to
// one onto the directives Mosquitto understands. A repeatable directive
// such as log_dest appears once per occurrence; a directive the operator
// wants the broker default for is listed with Unset set.
//
// The package never rewrites a directive whose value did not change, so
// loading the settings of a file and saving them back leaves the file
// byte-for-byte identical. Comments, unknown directives, listener blocks
// and the order of everything else in the file are preserved.
package settings

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

// Catalog is the subset of the versioned directive catalog the settings
// editor needs. Both catalog.Catalog and the deploy service's wiring
// satisfy it.
type Catalog interface {
	Lookup(name string) (catalog.DirectiveSpec, bool)
	Specs() []catalog.DirectiveSpec
}

// Entry is one directive occurrence in the settings document.
//
// Values holds the tokens after the directive name, e.g. ["file",
// "/var/log/mosquitto.log"] for "log_dest file /var/log/mosquitto.log".
// Unset marks a directive the operator wants the broker default for;
// such a directive is removed from the file.
type Entry struct {
	Directive string   `json:"directive"`
	Values    []string `json:"values"`
	Unset     bool     `json:"unset,omitempty"`
}

// Document is the set of settings the operator is editing. A directive
// the document does not mention is left untouched in the file.
type Document struct {
	Entries []Entry `json:"entries"`
}

// Values returns the values recorded for the first occurrence of
// directive, and whether the document mentions it at all. A directive the
// document marks as unset is mentioned but has no values.
func (d Document) Values(directive string) ([]string, bool) {
	for _, e := range d.Entries {
		if strings.EqualFold(e.Directive, directive) {
			if e.Unset {
				return nil, true
			}
			return e.Values, true
		}
	}
	return nil, false
}

// IsSet reports whether the document asks for the directive to be written
// to the file with a value.
func (d Document) IsSet(directive string) bool {
	for _, e := range d.Entries {
		if strings.EqualFold(e.Directive, directive) {
			return !e.Unset
		}
	}
	return false
}

// Field describes one editable settings field. It is derived from the
// catalog so the form shows the same type, unit and bounds the server
// enforces.
type Field struct {
	Directive        string                `json:"directive"`
	Section          string                `json:"section"`
	Type             catalog.DirectiveType `json:"type"`
	Unit             string                `json:"unit,omitempty"`
	Min              *int64                `json:"min,omitempty"`
	Max              *int64                `json:"max,omitempty"`
	Default          string                `json:"default,omitempty"`
	Multiplicity     catalog.Multiplicity  `json:"multiplicity"`
	ReloadKind       catalog.ReloadKind    `json:"reload_kind"`
	Scope            catalog.Scope         `json:"scope"`
	AllowedValues    []string              `json:"allowed_values,omitempty"`
	DurationSuffixes []string              `json:"duration_suffixes,omitempty"`
	Description      string                `json:"description"`
	Present          bool                  `json:"present"`
	Values           []string              `json:"values,omitempty"`
}

// Section groups the fields of one part of the form.
type Section struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Hint   string  `json:"hint,omitempty"`
	Fields []Field `json:"fields"`
}

// Issue describes one problem with a settings document. The kind mirrors
// the validation issues of the importer so the UI can group them.
type Issue struct {
	Kind      string `json:"kind"` // "unknown" | "type" | "range" | "multiplicity" | "duplicate"
	Directive string `json:"directive"`
	Message   string `json:"message"`
}

// sectionTitles are the headings the graphical editor uses. The ids come
// from the catalog so both sides agree on the vocabulary.
var sectionTitles = map[string]string{
	catalog.SectionPersistence: "Persistence and autosave",
	catalog.SectionQueues:      "Queues and messages",
	catalog.SectionSessions:    "Connections and sessions",
	catalog.SectionLogging:     "Logging",
}

// sectionHints explain what the section is for.
var sectionHints = map[string]string{
	catalog.SectionPersistence: "Where the broker keeps its state and how often it writes it to disk.",
	catalog.SectionQueues:      "How many messages the broker keeps for clients that cannot keep up.",
	catalog.SectionSessions:    "How many clients the broker accepts and how long it remembers them.",
	catalog.SectionLogging:     "Where the broker writes its log and which messages it records.",
}

// ManagedSpecs returns the catalog entries the settings form owns, sorted
// by name. A directive without a section is not editable here.
func ManagedSpecs(cat Catalog) []catalog.DirectiveSpec {
	if cat == nil {
		return nil
	}
	out := make([]catalog.DirectiveSpec, 0, len(cat.Specs()))
	for _, spec := range cat.Specs() {
		if spec.Section != "" {
			out = append(out, spec)
		}
	}
	return out
}

func managedMap(cat Catalog) map[string]catalog.DirectiveSpec {
	out := map[string]catalog.DirectiveSpec{}
	for _, spec := range ManagedSpecs(cat) {
		out[strings.ToLower(spec.Name)] = spec
	}
	return out
}

// Extract reads the settings the form owns out of a parsed
// configuration file.
//
// Only directives MCM manages are returned, and every managed directive
// is listed: one entry per occurrence, or a single entry marked Unset
// when the file does not set it. That makes the extracted document a
// complete description of the managed state, so saving it back without
// touching anything leaves the file unchanged.
func Extract(f *conf.File, cat Catalog) Document {
	doc := Document{}
	if f == nil || cat == nil {
		return doc
	}
	managed := managedMap(cat)
	occurrences := analyse(f, managed)

	// One entry per managed directive, in catalog order, so the form
	// always shows the same list.
	for _, spec := range ManagedSpecs(cat) {
		name := strings.ToLower(spec.Name)
		found := occurrences[name]
		if len(found) == 0 {
			doc.Entries = append(doc.Entries, Entry{Directive: spec.Name, Unset: true})
			continue
		}
		for _, occ := range found {
			doc.Entries = append(doc.Entries, Entry{
				Directive: spec.Name,
				Values:    append([]string(nil), occ.values...),
			})
		}
	}
	return doc
}

// Occurrences counts how often every managed directive appears in the
// file. The deploy service uses it to spot a setting that is also set in
// an included file.
func Occurrences(f *conf.File, cat Catalog) map[string]int {
	out := map[string]int{}
	if f == nil || cat == nil {
		return out
	}
	for name, occ := range analyse(f, managedMap(cat)) {
		out[name] = len(occ)
	}
	return out
}

// occurrence is one editable directive found in the file.
type occurrence struct {
	index  int
	values []string
}

// analyse finds the occurrences of the managed directives the form owns.
//
// A directive inside a listener block belongs to that listener and is
// edited in the listener panel, not here. A directive Mosquitto binds to
// the listener in scope (max_connections) is only editable in the global
// part of the file, before the first listener.
func analyse(f *conf.File, managed map[string]catalog.DirectiveSpec) map[string][]occurrence {
	out := map[string][]occurrence{}
	if f == nil {
		return out
	}
	inBlock := blockItems(f)
	globalRegion := true
	for i := range f.Items {
		it := &f.Items[i]
		switch it.Kind {
		case conf.ItemBlockOpen:
			globalRegion = false
			continue
		case conf.ItemBlockClose:
			continue
		case conf.ItemDirective:
		default:
			continue
		}
		if it.Key == "listener" || it.Key == "bridge" {
			globalRegion = false
			continue
		}
		spec, ok := managed[strings.ToLower(it.Key)]
		if !ok {
			continue
		}
		if inBlock[i] {
			continue
		}
		if spec.Scope == catalog.ScopeAny && !globalRegion {
			// Mosquitto binds this directive to the listener in
			// scope, so it is not a global setting.
			continue
		}
		name := strings.ToLower(it.Key)
		out[name] = append(out[name], occurrence{index: i, values: it.Values})
	}
	return out
}

// blockItems marks the items that sit inside a braced listener or bridge
// block.
func blockItems(f *conf.File) map[int]bool {
	out := map[int]bool{}
	depth := 0
	for i := range f.Items {
		switch f.Items[i].Kind {
		case conf.ItemBlockOpen:
			depth++
		case conf.ItemBlockClose:
			depth--
		case conf.ItemDirective:
			if depth > 0 {
				out[i] = true
			}
		}
	}
	return out
}

// Apply writes the desired document into a copy of the parsed file.
//
// The returned file is a copy: the input is never modified. The second
// return value lists the directives whose value changed, which is what
// decides between a SIGHUP and a full restart. Directives the document
// does not mention are left alone, and a directive whose value did not
// change keeps its original line, so the file stays byte-for-byte
// identical when nothing was edited.
func Apply(f *conf.File, doc Document, cat Catalog) (*conf.File, []string, error) {
	if f == nil {
		return nil, nil, errors.New("settings: no configuration file")
	}
	if cat == nil {
		return nil, nil, errors.New("settings: no catalog")
	}
	managed := managedMap(cat)

	out := &conf.File{Path: f.Path, Items: make([]conf.Item, len(f.Items))}
	copy(out.Items, f.Items)
	for i := range out.Items {
		out.Items[i].Values = append([]string(nil), out.Items[i].Values...)
	}

	current := analyse(out, managed)

	// Group the document by directive, keeping the document order so a
	// directive that is added to the file lands where the operator
	// expects it.
	desired := map[string][][]string{}
	unset := map[string]bool{}
	var docOrder []string
	seen := map[string]bool{}
	for _, e := range doc.Entries {
		name := strings.ToLower(strings.TrimSpace(e.Directive))
		if name == "" {
			continue
		}
		spec, ok := managed[name]
		if !ok {
			return nil, nil, fmt.Errorf("settings: %q is not an editable setting", e.Directive)
		}
		if !seen[name] {
			seen[name] = true
			docOrder = append(docOrder, name)
		}
		if e.Unset {
			if len(desired[name]) > 0 {
				return nil, nil, fmt.Errorf("settings: %q is marked unset and set at the same time", spec.Name)
			}
			unset[name] = true
			delete(desired, name)
			continue
		}
		if unset[name] {
			return nil, nil, fmt.Errorf("settings: %q is marked unset and set at the same time", spec.Name)
		}
		desired[name] = append(desired[name], append([]string(nil), e.Values...))
	}

	changed := map[string]bool{}
	remove := map[int]bool{}
	rewrite := map[int]conf.Item{}

	for _, name := range docOrder {
		occ := current[name]
		if unset[name] {
			// The operator wants the broker default: the directive
			// must not be in the file.
			if len(occ) > 0 {
				for _, o := range occ {
					remove[o.index] = true
				}
				changed[name] = true
			}
			continue
		}
		want := desired[name]
		if len(want) > 1 && managed[name].Multiplicity != catalog.Many {
			return nil, nil, fmt.Errorf("settings: %q may only be set once", managed[name].Name)
		}
		if equalOccurrences(occ, want) {
			continue
		}
		changed[name] = true
		// Keep as many existing lines as there are desired values and
		// rewrite the ones whose value changed.
		common := len(occ)
		if len(want) < common {
			common = len(want)
		}
		for i := 0; i < common; i++ {
			if equalValues(occ[i].values, want[i]) {
				continue
			}
			item := out.Items[occ[i].index]
			item.Values = append([]string(nil), want[i]...)
			item.Text = renderDirective(directiveName(item.Text, managed[name].Name), want[i], item.Indent)
			rewrite[occ[i].index] = item
		}
		// Occurrences beyond the desired count go away.
		for i := common; i < len(occ); i++ {
			remove[occ[i].index] = true
		}
	}

	// Apply the rewrites and drop the removed lines.
	for i, item := range rewrite {
		out.Items[i] = item
	}
	if len(remove) > 0 {
		kept := make([]conf.Item, 0, len(out.Items))
		for i := range out.Items {
			if remove[i] {
				continue
			}
			kept = append(kept, out.Items[i])
		}
		out.Items = kept
	}

	// Add the occurrences that did not exist yet. They go next to the
	// directive they belong to, or into the global part of the file when
	// the directive is new.
	for _, name := range docOrder {
		want := desired[name]
		// The indices are recomputed for every directive: an
		// insertion shifts the items that follow it.
		occ := analyse(out, managed)[name]
		if len(want) <= len(occ) {
			continue
		}
		missing := want[len(occ):]
		at := insertionIndex(out.Items)
		if len(occ) > 0 {
			at = occ[len(occ)-1].index + 1
		}
		for _, values := range missing {
			item := conf.Item{
				Kind:   conf.ItemDirective,
				Key:    name,
				Values: append([]string(nil), values...),
				Line:   -1,
			}
			item.Text = renderDirective(managed[name].Name, values, 0)
			out.Items = insertItem(out.Items, at, item)
			at++
		}
	}

	names := make([]string, 0, len(changed))
	for name := range changed {
		names = append(names, name)
	}
	sort.Strings(names)
	return out, names, nil
}

// insertItem puts item at index at, keeping the order of the rest.
func insertItem(items []conf.Item, at int, item conf.Item) []conf.Item {
	if at < 0 {
		at = 0
	}
	if at > len(items) {
		at = len(items)
	}
	items = append(items, conf.Item{})
	copy(items[at+1:], items[at:])
	items[at] = item
	return items
}

func equalOccurrences(occ []occurrence, want [][]string) bool {
	if len(occ) != len(want) {
		return false
	}
	for i := range occ {
		if !equalValues(occ[i].values, want[i]) {
			return false
		}
	}
	return true
}

func equalValues(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
	if indent > 0 {
		b.WriteString(strings.Repeat(" ", indent))
	}
	b.WriteString(name)
	for _, v := range values {
		b.WriteString(" ")
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
			if it.Key == "listener" || it.Key == "bridge" {
				return last + 1
			}
			last = i
		case conf.ItemBlockOpen:
			return last + 1
		}
	}
	return last + 1
}

// Validate checks a settings document against the catalog: known
// directives, the right number of values, the accepted range and the
// multiplicity. It never returns an error; every problem is an Issue so
// the operator sees all of them at once.
func Validate(doc Document, cat Catalog) []Issue {
	if cat == nil {
		return nil
	}
	managed := managedMap(cat)
	var issues []Issue
	counts := map[string]int{}
	setDirectives := map[string]bool{}
	unsetDirectives := map[string]bool{}
	fileDests := 0

	for _, e := range doc.Entries {
		name := strings.ToLower(strings.TrimSpace(e.Directive))
		if name == "" {
			issues = append(issues, Issue{Kind: "unknown", Message: "a setting without a name was sent"})
			continue
		}
		spec, ok := managed[name]
		if !ok {
			issues = append(issues, Issue{
				Kind: "unknown", Directive: e.Directive,
				Message: fmt.Sprintf("%q is not an editable setting", e.Directive),
			})
			continue
		}
		if e.Unset {
			unsetDirectives[name] = true
			continue
		}
		setDirectives[name] = true
		counts[name]++
		if spec.Multiplicity != catalog.Many && counts[name] > 1 {
			issues = append(issues, Issue{
				Kind: "multiplicity", Directive: spec.Name,
				Message: fmt.Sprintf("%q may only be set once", spec.Name),
			})
			continue
		}
		issues = append(issues, validateValues(spec, e.Values)...)
		if name == "log_dest" && len(e.Values) > 0 && e.Values[0] == "file" {
			fileDests++
			if len(e.Values) < 2 || strings.TrimSpace(e.Values[1]) == "" {
				issues = append(issues, Issue{
					Kind: "type", Directive: spec.Name,
					Message: "log_dest file needs the path of the log file",
				})
			}
		}
	}
	if fileDests > 1 {
		issues = append(issues, Issue{
			Kind: "duplicate", Directive: "log_dest",
			Message: "only one log_dest file destination is allowed",
		})
	}
	// A directive that is both set and unset has no meaning: the
	// operator asked for a value and for the broker default at once.
	for name := range setDirectives {
		if unsetDirectives[name] {
			issues = append(issues, Issue{
				Kind: "conflict", Directive: managed[name].Name,
				Message: fmt.Sprintf("%q is set and unset at the same time", managed[name].Name),
			})
		}
	}
	return issues
}

// validateValues checks the value shape of one occurrence against the
// catalog type, unit and bounds.
func validateValues(spec catalog.DirectiveSpec, values []string) []Issue {
	var issues []Issue
	if len(values) == 0 {
		if spec.Type != catalog.TypeBoolean {
			issues = append(issues, Issue{
				Kind: "type", Directive: spec.Name,
				Message: fmt.Sprintf("%q expects a value", spec.Name),
			})
		}
		return issues
	}
	switch spec.Type {
	case catalog.TypeBoolean:
		if len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			issues = append(issues, Issue{
				Kind: "type", Directive: spec.Name,
				Message: fmt.Sprintf("%q expects true or false", spec.Name),
			})
		}
	case catalog.TypeInteger:
		if len(values) != 1 {
			issues = append(issues, Issue{
				Kind: "type", Directive: spec.Name,
				Message: fmt.Sprintf("%q expects a single number", spec.Name),
			})
			break
		}
		n, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			issues = append(issues, Issue{
				Kind: "type", Directive: spec.Name,
				Message: fmt.Sprintf("%q expects a number, got %q", spec.Name, values[0]),
			})
			break
		}
		if spec.Min != nil && n < *spec.Min {
			issues = append(issues, Issue{
				Kind: "range", Directive: spec.Name,
				Message: fmt.Sprintf("%q must be %s or more", spec.Name, boundText(*spec.Min, spec.Unit)),
			})
		}
		if spec.Max != nil && n > *spec.Max {
			issues = append(issues, Issue{
				Kind: "range", Directive: spec.Name,
				Message: fmt.Sprintf("%q must be %s or less", spec.Name, boundText(*spec.Max, spec.Unit)),
			})
		}
	case catalog.TypeDuration:
		if len(values) != 1 {
			issues = append(issues, Issue{
				Kind: "type", Directive: spec.Name,
				Message: fmt.Sprintf("%q expects a single duration", spec.Name),
			})
			break
		}
		if msg, ok := durationMessage(spec, values[0]); !ok {
			issues = append(issues, Issue{Kind: "type", Directive: spec.Name, Message: msg})
		}
	case catalog.TypeEnum:
		if len(values[0]) > 0 {
			allowed := false
			for _, a := range spec.AllowedValues {
				if values[0] == a {
					allowed = true
					break
				}
			}
			if !allowed {
				issues = append(issues, Issue{
					Kind: "type", Directive: spec.Name,
					Message: fmt.Sprintf("%q does not accept %q, expected one of %s", spec.Name, values[0], strings.Join(spec.AllowedValues, ", ")),
				})
			}
		}
	}
	return issues
}

// durationMessage validates a duration value such as "2h" against the
// suffixes the broker version accepts.
func durationMessage(spec catalog.DirectiveSpec, value string) (string, bool) {
	if len(value) < 2 {
		return fmt.Sprintf("%q expects a number followed by a unit (%s)", spec.Name, strings.Join(spec.DurationSuffixes, ", ")), false
	}
	digits, suffix := value[:len(value)-1], value[len(value)-1:]
	n, err := strconv.Atoi(digits)
	if err != nil {
		return fmt.Sprintf("%q expects a number followed by a unit (%s), got %q", spec.Name, strings.Join(spec.DurationSuffixes, ", "), value), false
	}
	if n <= 0 {
		return fmt.Sprintf("%q must be greater than zero, got %q", spec.Name, value), false
	}
	for _, allowed := range spec.DurationSuffixes {
		if suffix == allowed {
			return "", true
		}
	}
	return fmt.Sprintf("%q does not accept the %q unit, expected one of %s", spec.Name, string(suffix), strings.Join(spec.DurationSuffixes, ", ")), false
}

// boundText renders a bound with its unit.
func boundText(value int64, unit string) string {
	if unit == "" {
		return strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10) + " " + unit
}

// BuildSections describes the form: every managed directive with the
// metadata the UI needs, grouped into the sections of the catalog.
func BuildSections(cat Catalog, doc Document) []Section {
	managed := managedMap(cat)
	values := map[string][][]string{}
	for _, e := range doc.Entries {
		name := strings.ToLower(strings.TrimSpace(e.Directive))
		if name == "" {
			continue
		}
		if _, ok := managed[name]; !ok {
			continue
		}
		if e.Unset {
			continue
		}
		values[name] = append(values[name], e.Values)
	}

	sections := make([]Section, 0, len(catalog.KnownSections()))
	for _, id := range catalog.KnownSections() {
		section := Section{
			ID:    id,
			Title: sectionTitles[id],
			Hint:  sectionHints[id],
		}
		for _, spec := range ManagedSpecs(cat) {
			if spec.Section != id {
				continue
			}
			name := strings.ToLower(spec.Name)
			field := Field{
				Directive:        spec.Name,
				Section:          spec.Section,
				Type:             spec.Type,
				Unit:             spec.Unit,
				Min:              spec.Min,
				Max:              spec.Max,
				Default:          spec.Default,
				Multiplicity:     spec.Multiplicity,
				ReloadKind:       spec.ReloadKind,
				Scope:            spec.Scope,
				AllowedValues:    spec.AllowedValues,
				DurationSuffixes: spec.DurationSuffixes,
				Description:      spec.Description,
			}
			if occ := values[name]; len(occ) > 0 {
				field.Present = true
				field.Values = occ[0]
			}
			section.Fields = append(section.Fields, field)
		}
		sections = append(sections, section)
	}
	return sections
}

// ChangedDirectives compares the settings of two files and returns the
// managed directives whose value differs. It is used to tell a SIGHUP
// apart from a full restart.
func ChangedDirectives(before, after *conf.File, cat Catalog) []string {
	if cat == nil {
		return nil
	}
	managed := managedMap(cat)
	oldValues := map[string][][]string{}
	newValues := map[string][][]string{}
	for name, occ := range analyse(before, managed) {
		for _, o := range occ {
			oldValues[name] = append(oldValues[name], o.values)
		}
	}
	for name, occ := range analyse(after, managed) {
		for _, o := range occ {
			newValues[name] = append(newValues[name], o.values)
		}
	}
	changed := map[string]bool{}
	for name := range managed {
		if !equalValueLists(oldValues[name], newValues[name]) {
			changed[name] = true
		}
	}
	out := make([]string, 0, len(changed))
	for name := range changed {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func equalValueLists(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !equalValues(a[i], b[i]) {
			return false
		}
	}
	return true
}
