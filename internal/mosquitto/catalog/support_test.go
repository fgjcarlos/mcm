package catalog

import (
	"strings"
	"testing"
)

// TestUpstreamDirectives_Loaded proves the upstream inventory of every
// shipped broker version loads and is sorted and deduplicated.
func TestUpstreamDirectives_Loaded(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		names, err := UpstreamDirectives(version)
		if err != nil {
			t.Fatalf("UpstreamDirectives(%q): %v", version, err)
		}
		if len(names) == 0 {
			t.Fatalf("UpstreamDirectives(%q): empty inventory", version)
		}
		for i, n := range names {
			if n == "" {
				t.Errorf("UpstreamDirectives(%q)[%d]: empty name", version, i)
			}
			if n != strings.ToLower(n) {
				t.Errorf("UpstreamDirectives(%q)[%d] = %q: must be lower-case", version, i, n)
			}
			if i > 0 && names[i-1] >= n {
				t.Errorf("UpstreamDirectives(%q): not sorted/deduplicated at %d (%q after %q)",
					version, i, n, names[i-1])
			}
		}
	}
}

// TestUpstreamDirectives_UnknownVersion proves an unsupported broker
// version fails instead of returning an empty universe, which would make
// the matrix claim that MCM manages nothing.
func TestUpstreamDirectives_UnknownVersion(t *testing.T) {
	if _, err := UpstreamDirectives("9.9"); err == nil {
		t.Fatal("UpstreamDirectives(\"9.9\") = nil error, want an error")
	}
	if _, err := UpstreamDirectives("../etc"); err == nil {
		t.Fatal("UpstreamDirectives(\"../etc\") = nil error, want an error")
	}
}

// TestSupportMatrix_CoversEveryUpstreamDirective proves the matrix
// classifies every directive the upstream manual documents exactly once.
// A directive that fell out of the matrix would silently disappear from
// the published support statement.
//
// The matrix is the union of the upstream inventory and the catalog, so
// it may hold more entries than the upstream list: a directive MCM
// manages that the manual does not document still belongs in the matrix.
func TestSupportMatrix_CoversEveryUpstreamDirective(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		cat, err := LoadVersion(version)
		if err != nil {
			t.Fatalf("LoadVersion(%q): %v", version, err)
		}
		upstream, err := UpstreamDirectives(version)
		if err != nil {
			t.Fatalf("UpstreamDirectives(%q): %v", version, err)
		}
		matrix, err := SupportMatrix(cat, upstream)
		if err != nil {
			t.Fatalf("SupportMatrix(%q): %v", version, err)
		}
		if len(matrix.Entries) < len(upstream) {
			t.Errorf("version %s: matrix has %d entries, upstream has %d",
				version, len(matrix.Entries), len(upstream))
		}
		counts := matrix.Counts()
		total := counts[SupportManaged] + counts[SupportPartial] + counts[SupportUnsupported]
		if total != len(matrix.Entries) {
			t.Errorf("version %s: support levels sum to %d, matrix has %d entries",
				version, total, len(matrix.Entries))
		}
		seen := map[string]bool{}
		for _, e := range matrix.Entries {
			if seen[e.Directive] {
				t.Errorf("version %s: directive %q appears twice", version, e.Directive)
			}
			seen[e.Directive] = true
			switch e.Level {
			case SupportManaged:
				if e.Surface == "" {
					t.Errorf("version %s: directive %q is managed but has no surface", version, e.Directive)
				}
			case SupportPartial:
				// A partial directive is edited in the advanced root
				// editor, so it names that surface.
				if e.Surface != SurfaceAdvanced {
					t.Errorf("version %s: directive %q is partial but names surface %q",
						version, e.Directive, e.Surface)
				}
			case SupportUnsupported:
				if e.Surface != "" {
					t.Errorf("version %s: directive %q is unsupported but names surface %q",
						version, e.Directive, e.Surface)
				}
			default:
				t.Errorf("version %s: directive %q has unknown level %q", version, e.Directive, e.Level)
			}
		}
		// Every upstream directive is present.
		for _, name := range upstream {
			if !seen[name] {
				t.Errorf("version %s: upstream directive %q is missing from the matrix", version, name)
			}
		}
	}
}

// TestSupportMatrix_CatalogDirectivesAreClassified proves every
// directive the catalog knows is classified as managed or partial, never
// as unsupported. The catalog is the set MCM administers.
func TestSupportMatrix_CatalogDirectivesAreClassified(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		cat, err := LoadVersion(version)
		if err != nil {
			t.Fatalf("LoadVersion(%q): %v", version, err)
		}
		upstream, err := UpstreamDirectives(version)
		if err != nil {
			t.Fatalf("UpstreamDirectives(%q): %v", version, err)
		}
		matrix, err := SupportMatrix(cat, upstream)
		if err != nil {
			t.Fatalf("SupportMatrix(%q): %v", version, err)
		}
		level := map[string]SupportLevel{}
		for _, e := range matrix.Entries {
			level[e.Directive] = e.Level
		}
		for _, spec := range cat.Specs() {
			name := strings.ToLower(spec.Name)
			got, ok := level[name]
			if !ok {
				t.Errorf("version %s: catalog directive %q is missing from the matrix", version, name)
				continue
			}
			if got == SupportUnsupported {
				t.Errorf("version %s: catalog directive %q must not be unsupported", version, name)
			}
		}
	}
}

// TestSupportMatrix_ManagedDirectivesHaveADedicatedSurface proves the
// classification reflects the editors MCM actually ships: a managed
// directive is either a settings field, a listener, bridge, plugin or
// access directive.
func TestSupportMatrix_ManagedDirectivesHaveADedicatedSurface(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		cat, err := LoadVersion(version)
		if err != nil {
			t.Fatalf("LoadVersion(%q): %v", version, err)
		}
		upstream, err := UpstreamDirectives(version)
		if err != nil {
			t.Fatalf("UpstreamDirectives(%q): %v", version, err)
		}
		matrix, err := SupportMatrix(cat, upstream)
		if err != nil {
			t.Fatalf("SupportMatrix(%q): %v", version, err)
		}
		for _, e := range matrix.Entries {
			if e.Level != SupportManaged {
				continue
			}
			switch e.Surface {
			case SurfaceSettings, SurfaceListeners, SurfaceBridges, SurfacePlugins, SurfaceAccess:
			default:
				t.Errorf("version %s: directive %q is managed by unknown surface %q",
					version, e.Directive, e.Surface)
			}
		}
	}
}

// TestSupportMatrix_SectionDirectivesAreManaged proves the settings form
// and the matrix agree: every catalog directive with a section is a
// managed directive of the settings surface.
func TestSupportMatrix_SectionDirectivesAreManaged(t *testing.T) {
	for _, version := range []string{"2.0", "2.1"} {
		cat, err := LoadVersion(version)
		if err != nil {
			t.Fatalf("LoadVersion(%q): %v", version, err)
		}
		upstream, err := UpstreamDirectives(version)
		if err != nil {
			t.Fatalf("UpstreamDirectives(%q): %v", version, err)
		}
		matrix, err := SupportMatrix(cat, upstream)
		if err != nil {
			t.Fatalf("SupportMatrix(%q): %v", version, err)
		}
		surface := map[string]Surface{}
		for _, e := range matrix.Entries {
			surface[e.Directive] = e.Surface
		}
		for _, spec := range cat.Specs() {
			if spec.Section == "" {
				continue
			}
			name := strings.ToLower(spec.Name)
			if got := surface[name]; got != SurfaceSettings {
				t.Errorf("version %s: directive %q has section %q but surface %q",
					version, name, spec.Section, got)
			}
		}
	}
}
