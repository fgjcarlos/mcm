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

// dataFS holds the embedded catalog YAML files. Each file describes one
// broker version; the file name is "<version>.yaml".
//
//go:embed data/*.yaml
var dataFS embed.FS

// LoadVersion loads the catalog for one broker version (e.g. "2.0").
// Returns ErrUnknownDirective when the version has no catalog file.
// The returned *Catalog is immutable and safe for concurrent use.
func LoadVersion(version string) (*Catalog, error) {
	return loadVersion(version, map[string]bool{})
}

// loadVersion loads a catalog, following the "extends" chain. visited
// guards against a cycle between two catalog files.
func loadVersion(version string, visited map[string]bool) (*Catalog, error) {
	name := strings.TrimSpace(version)
	if name == "" {
		return nil, errors.New("catalog version is required")
	}
	if !validVersionName(name) {
		return nil, fmt.Errorf("catalog version %q: invalid name", name)
	}
	if visited[name] {
		return nil, fmt.Errorf("catalog version %q: extends cycle", name)
	}
	visited[name] = true
	body, err := fs.ReadFile(dataFS, path.Join("data", name+".yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: version %q has no catalog", ErrUnknownDirective, name)
		}
		return nil, fmt.Errorf("read catalog data/%s.yaml: %w", name, err)
	}
	return parseVersion(name, body, visited)
}

// catalogDoc is one YAML document of a catalog file. The optional
// "extends" key names the catalog version a file inherits from; every
// other key describes one directive.
type catalogDoc struct {
	Extends       string `yaml:"extends"`
	DirectiveSpec `yaml:",inline"`
}

// LoadAll returns every embedded catalog keyed by version. Used by tests
// and the /api/v1/broker/config/catalog endpoint.
func LoadAll() (map[string]*Catalog, error) {
	entries, err := fs.ReadDir(dataFS, "data")
	if err != nil {
		return nil, fmt.Errorf("read catalog data dir: %w", err)
	}
	out := make(map[string]*Catalog, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		version := strings.TrimSuffix(e.Name(), ".yaml")
		body, err := fs.ReadFile(dataFS, path.Join("data", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read catalog %s: %w", e.Name(), err)
		}
		cat, err := parseVersion(version, body, map[string]bool{})
		if err != nil {
			return nil, err
		}
		out[version] = cat
	}
	return out, nil
}

// validVersionName caps what Load accepts so a typo cannot trigger a
// filesystem path traversal. Mosquitto versions are "<major>.<minor>".
func validVersionName(s string) bool {
	if len(s) == 0 || len(s) > 16 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '.':
			if i == 0 || i == len(s)-1 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func parseVersion(version string, body []byte, visited map[string]bool) (*Catalog, error) {
	var entries []DirectiveSpec
	var extends string
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	for {
		var d catalogDoc
		if err := dec.Decode(&d); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse catalog %s: %w", version, err)
		}
		if d.Name == "" && d.Type == "" {
			if d.Extends != "" {
				if extends != "" {
					return nil, fmt.Errorf("catalog %s: duplicate extends header", version)
				}
				if len(entries) > 0 {
					return nil, fmt.Errorf("catalog %s: extends header must be the first document", version)
				}
				extends = d.Extends
			}
			continue
		}
		if err := d.Validate(DefaultSentinels); err != nil {
			return nil, fmt.Errorf("catalog %s: %w", version, err)
		}
		entries = append(entries, d.DirectiveSpec)
	}
	// A version may inherit the directives of an earlier one and override
	// single entries. The inherited entries come first so a child can
	// refine a directive (e.g. add the 2.1 duration suffix).
	var parent *Catalog
	if extends != "" {
		p, err := loadVersion(extends, visited)
		if err != nil {
			return nil, fmt.Errorf("catalog %s: %w", version, err)
		}
		parent = p
	}
	if len(entries) == 0 && parent == nil {
		// An empty / stub catalog is valid — it just means "no
		// MCM-managed directives for this version yet". Loader
		// callers (e.g. LoadAll) still want the Catalog back so they
		// can enumerate versions.
		entries = nil
	}
	specs := make(map[string]DirectiveSpec)
	names := make([]string, 0, len(entries))
	if parent != nil {
		for key, spec := range parent.specs {
			specs[key] = spec
		}
	}
	own := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		key := strings.ToLower(e.Name)
		if _, dup := own[key]; dup {
			return nil, fmt.Errorf("catalog %s: duplicate directive %q", version, e.Name)
		}
		own[key] = struct{}{}
		// An entry with the same name as an inherited one replaces it:
		// that is how a version changes a default or widens a range.
		specs[key] = e
	}
	if parent != nil {
		for _, name := range parent.knownNames {
			if _, ok := own[strings.ToLower(name)]; ok {
				continue
			}
			names = append(names, name)
		}
	}
	for _, e := range entries {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return &Catalog{Version: version, specs: specs, knownNames: names}, nil
}
