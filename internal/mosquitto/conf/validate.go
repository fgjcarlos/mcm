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

	// listener blocks need their inner items validated against
	// ScopeListener; the catalog-lookup for listener-only directives
	// expects to find the enclosing block. We track which items are
	// inside any listener block so the validator can pass scope
	// correctly.
	inListener := make(map[int]bool)
	for _, b := range f.ListenerBlocks() {
		for i := range b.Body {
			// body Items are not indexed by global position so we
			// use a map keyed by Line instead.
			inListener[b.Body[i].Line] = true
		}
	}

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

			// Scope check.
			if spec.Scope == catalog.ScopeListener && !inListener[it.Line] {
				issues = append(issues, ValidationIssue{
					Kind: "scope", Line: it.Line, Directive: it.Key,
					Message: fmt.Sprintf("%q only valid inside a listener block", it.Key),
				})
			}
			if spec.Scope == catalog.ScopeGlobal && inListener[it.Line] {
				issues = append(issues, ValidationIssue{
					Kind: "scope", Line: it.Line, Directive: it.Key,
					Message: fmt.Sprintf("%q only valid at global scope", it.Key),
				})
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
					if _, err := strconv.Atoi(v); err != nil {
						issues = append(issues, ValidationIssue{
							Kind: "type", Line: it.Line, Directive: it.Key,
							Message: fmt.Sprintf("%q expects an integer, got %q", it.Key, v),
						})
						break
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
