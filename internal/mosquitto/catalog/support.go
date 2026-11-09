package catalog

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// upstreamFS holds the upstream directive inventories, one file per broker
// version ("upstream-<version>.yaml"). Each file lists the directives the
// Mosquitto configuration manual of that version documents.
//
//go:embed upstream/*.yaml
var upstreamFS embed.FS

// SupportLevel classifies how completely MCM manages a directive of a
// broker version.
//
// The levels describe MCM, not the broker: a directive Mosquitto supports
// is "unsupported" here when MCM does not manage it for that version. MCM
// never claims compatibility it does not have.
type SupportLevel string

const (
	// SupportManaged means MCM owns the directive end to end: it is edited
	// through a dedicated graphical control, validated against the
	// catalog, and written through a reviewed apply revision.
	SupportManaged SupportLevel = "managed"
	// SupportPartial means MCM knows the directive and preserves it
	// losslessly on import and round-trip, and validates it against the
	// catalog, but exposes no dedicated graphical control for it: the
	// operator edits it in the advanced root editor.
	SupportPartial SupportLevel = "partial"
	// SupportUnsupported means the directive is documented upstream for
	// that broker version but MCM does not manage it. MCM preserves it
	// byte-for-byte on import so a foreign configuration is never
	// silently dropped, but it offers no editor and no validation.
	SupportUnsupported SupportLevel = "unsupported"
)

// SupportEntry is one row of the support matrix: a directive of one broker
// version with the level of support MCM gives it.
type SupportEntry struct {
	Version   string       `json:"version"`
	Directive string       `json:"directive"`
	Level     SupportLevel `json:"level"`
	// Surface names the MCM control that manages the directive. It is
	// empty for a directive MCM does not manage.
	Surface Surface `json:"surface,omitempty"`
}

// Matrix is the versioned support matrix of every directive MCM knows
// about, for one broker version.
type Matrix struct {
	Version string `json:"version"`
	// Entries are sorted by directive name.
	Entries []SupportEntry `json:"entries"`
}

// Counts returns the number of directives per support level.
func (m *Matrix) Counts() map[SupportLevel]int {
	out := map[SupportLevel]int{
		SupportManaged:     0,
		SupportPartial:     0,
		SupportUnsupported: 0,
	}
	for _, e := range m.Entries {
		out[e.Level]++
	}
	return out
}

// Directives returns the directives of one support level, sorted.
func (m *Matrix) Directives(level SupportLevel) []string {
	var out []string
	for _, e := range m.Entries {
		if e.Level == level {
			out = append(out, e.Directive)
		}
	}
	sort.Strings(out)
	return out
}

// SurfaceDirectives returns the directives managed by one MCM surface,
// sorted. A directive MCM does not manage belongs to no surface.
func (m *Matrix) SurfaceDirectives(surface Surface) []string {
	var out []string
	for _, e := range m.Entries {
		if e.Surface == surface && e.Level == SupportManaged {
			out = append(out, e.Directive)
		}
	}
	sort.Strings(out)
	return out
}

// UpstreamDirectives returns the directives the Mosquitto configuration
// manual documents for the given broker version, sorted. The list is the
// upstream truth and is independent of what MCM manages.
func UpstreamDirectives(version string) ([]string, error) {
	name := strings.TrimSpace(version)
	if !validVersionName(name) {
		return nil, fmt.Errorf("upstream directives %q: invalid version name", version)
	}
	body, err := fs.ReadFile(upstreamFS, path.Join("upstream", name+".yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: version %q has no upstream inventory", ErrUnknownDirective, version)
		}
		return nil, fmt.Errorf("read upstream inventory %q: %w", version, err)
	}
	return parseUpstreamNames(body)
}

// parseUpstreamNames reads the directive names out of an upstream
// inventory file. The file is a YAML stream of one-name documents; the
// names are the only thing it carries.
func parseUpstreamNames(body []byte) ([]string, error) {
	names, err := upstreamNames(body)
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	return names, nil
}

// upstreamNames is a thin wrapper kept separate so the YAML decoding of
// the inventory and of the catalog share one error vocabulary.
func upstreamNames(body []byte) ([]string, error) {
	dec := yaml.NewDecoder(bytes.NewReader(body))
	var out []string
	for {
		var doc struct {
			Name string `yaml:"name"`
		}
		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse upstream inventory: %w", err)
		}
		if doc.Name == "" {
			continue
		}
		out = append(out, strings.ToLower(doc.Name))
	}
	return out, nil
}
