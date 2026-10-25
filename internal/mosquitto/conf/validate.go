package conf

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
)

// ValidationIssue describes one problem found by Validate. Issue #298
// keeps the shape deliberately flat so the HTTP layer can render it as
// a JSON array without a separate "details" object.
type ValidationIssue struct {
	Kind      string `json:"kind"` // "unknown" | "type" | "scope" | "multiplicity" | "dependency" | "since_version"
	Line      int    `json:"line"`
	Directive string `json:"directive"`
	Message   string `json:"message"`
}

// Validate runs the catalog-driven checks against a parsed File. Issues
// are returned in source order (line ascending). The function never
// returns an error — every failure is a ValidationIssue so callers can
// surface them collectively to the operator.
//
// Unknown directives are reported as kind "unknown" but the importer
// preserves them byte-for-byte (Render is lossless). They are
// informational, not blockers.
func Validate(f *File, cat *catalog.Catalog) []ValidationIssue {
	if f == nil || cat == nil {
		return nil
	}
	var issues []ValidationIssue

	// Multiplicity accounting keyed by directive name.
	counts := make(map[string]int)

	// A directive belongs to a listener when Mosquitto would bind it to
	// one: inside a braced listener block, or after a brace-less
	// "listener <port>" line, which applies to the directives that
	// follow it until the next listener.
	inListener := listenerContext(f)

	for _, it := range f.Items {
		switch it.Kind {
		case ItemBlockOpen:
			spec, ok := cat.Lookup(it.Key)
			_ = spec
			if !ok {
				issues = append(issues, ValidationIssue{
					Kind: "unknown", Line: it.Line, Directive: it.Key,
					Message: fmt.Sprintf("block directive %q is not in the catalog for Mosquitto %s", it.Key, cat.VersionName()),
				})
				continue
			}
			counts[strings.ToLower(it.Key)]++

		case ItemDirective:
			spec, ok := cat.Lookup(it.Key)
			if !ok {
				issues = append(issues, ValidationIssue{
					Kind: "unknown", Line: it.Line, Directive: it.Key,
					Message: fmt.Sprintf("directive %q is not in the catalog for Mosquitto %s", it.Key, cat.VersionName()),
				})
				continue
			}
			counts[strings.ToLower(it.Key)]++

			// Scope check. A directive that opens a listener is by
			// definition written at the top level, so it is never
			// out of scope.
			if !isBlockDirective(it.Key) {
				if spec.Scope == catalog.ScopeListener && !inListener[it.Line] {
					issues = append(issues, ValidationIssue{
						Kind: "scope", Line: it.Line, Directive: it.Key,
						Message: fmt.Sprintf("%q only valid inside a listener block", it.Key),
					})
				}
				// Mosquitto applies a global directive globally
				// wherever it appears, so a global directive inside a
				// listener context is only worth a warning when the
				// broker does not accept it per listener at all.
				if spec.Scope == catalog.ScopeGlobal && inListener[it.Line] && spec.ListenerSinceVersion == "" {
					issues = append(issues, ValidationIssue{
						Kind: "scope", Line: it.Line, Directive: it.Key,
						Message: fmt.Sprintf("%q only valid at global scope", it.Key),
					})
				}
			}

			// Type check (best-effort; we do not understand quoted
			// args that contain commas etc.).
			switch {
			case len(it.Values) == 0 && spec.Type != catalog.TypeBoolean:
				issues = append(issues, ValidationIssue{
					Kind: "type", Line: it.Line, Directive: it.Key,
					Message: fmt.Sprintf("%q expects at least one value", it.Key),
				})
			case spec.Type == catalog.TypeInteger:
				// Mosquitto's listener directive takes a port plus an
				// optional bind address; only the first value must be
				// integer. Other integer-scope directives always
				// take exactly one value, so this special case is
				// the only one we need.
				for i, v := range it.Values {
					if spec.Name == "listener" && i > 0 {
						continue
					}
					n, err := strconv.Atoi(v)
					if err != nil {
						issues = append(issues, ValidationIssue{
							Kind: "type", Line: it.Line, Directive: it.Key,
							Message: fmt.Sprintf("%q expects an integer, got %q", it.Key, v),
						})
						break
					}
					if msg, ok := rangeCheck(spec, n); !ok {
						issues = append(issues, ValidationIssue{
							Kind: "range", Line: it.Line, Directive: it.Key,
							Message: msg,
						})
						break
					}
				}
			case spec.Type == catalog.TypeDuration:
				if len(it.Values) > 0 {
					if msg, ok := durationCheck(spec, it.Values[0]); !ok {
						issues = append(issues, ValidationIssue{
							Kind: "type", Line: it.Line, Directive: it.Key,
							Message: msg,
						})
					}
				}
			case spec.Type == catalog.TypeEnum && len(spec.AllowedValues) > 0:
				allowed := false
				for _, av := range spec.AllowedValues {
					if len(it.Values) > 0 && it.Values[0] == av {
						allowed = true
						break
					}
				}
				if !allowed && len(it.Values) > 0 {
					issues = append(issues, ValidationIssue{
						Kind: "type", Line: it.Line, Directive: it.Key,
						Message: fmt.Sprintf("%q first argument %q not in allowed values %v", it.Key, it.Values[0], spec.AllowedValues),
					})
				}
			}
		}
	}

	// Multiplicity post-pass.
	for _, spec := range cat.Specs() {
		count := counts[strings.ToLower(spec.Name)]
		if spec.Multiplicity == catalog.Once && count > 1 {
			issues = append(issues, ValidationIssue{
				Kind: "multiplicity", Directive: spec.Name,
				Message: fmt.Sprintf("%q must appear at most once but appeared %d times", spec.Name, count),
			})
		}
	}

	return issues
}

// listenerContext marks the items that Mosquitto binds to a listener.
//
// A listener is opened either by a braced block ("listener 1883 { … }")
// or by a brace-less "listener <port>" line, which applies to the
// directives that follow it until the next listener. The map is keyed by
// the source line of the item.
func listenerContext(f *File) map[int]bool {
	out := make(map[int]bool)
	if f == nil {
		return out
	}
	depth := 0
	inListener := false
	for i := range f.Items {
		it := &f.Items[i]
		switch it.Kind {
		case ItemBlockOpen:
			depth++
			inListener = true
			continue
		case ItemBlockClose:
			depth--
			if depth <= 0 {
				depth = 0
				inListener = false
			}
			continue
		case ItemDirective:
		default:
			continue
		}
		if isBlockDirective(it.Key) {
			// The brace-less form: the directives that follow
			// belong to this listener.
			inListener = true
			continue
		}
		if inListener {
			out[it.Line] = true
		}
	}
	return out
}

// rangeCheck reports whether an integer value stays inside the bounds the
// catalog records for the directive. The bounds mirror what the broker
// itself accepts, so an out-of-range value is refused before the file
// reaches Mosquitto.
func rangeCheck(spec catalog.DirectiveSpec, value int) (string, bool) {
	if spec.Min != nil && int64(value) < *spec.Min {
		return fmt.Sprintf("%q must be %s or more, got %d", spec.Name, formatBound(*spec.Min, spec.Unit), value), false
	}
	if spec.Max != nil && int64(value) > *spec.Max {
		return fmt.Sprintf("%q must be %s or less, got %d", spec.Name, formatBound(*spec.Max, spec.Unit), value), false
	}
	return "", true
}

// formatBound renders a bound with its unit so the message reads like
// "must be 20 bytes or more".
func formatBound(value int64, unit string) string {
	if unit == "" {
		return strconv.FormatInt(value, 10)
	}
	return strconv.FormatInt(value, 10) + " " + unit
}

// durationCheck validates a duration value such as "2h". The broker only
// accepts a positive integer followed by one of the unit suffixes the
// catalog lists for the version.
func durationCheck(spec catalog.DirectiveSpec, value string) (string, bool) {
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
