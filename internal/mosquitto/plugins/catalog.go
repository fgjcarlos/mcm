package plugins

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

// dataFS holds the embedded plugin catalog files. Each file describes the
// plugins MCM supports for one broker version; the file name is
// "<version>.yaml".
//
//go:embed data/*.yaml
var dataFS embed.FS

// LoadVersion loads the plugin catalog of one broker version (e.g.
// "2.0"). It returns ErrUnknownPlugin when the version has no catalog.
func LoadVersion(version string) (*Catalog, error) {
	return loadVersion(version, map[string]bool{})
}

// loadVersion loads a catalog, following the "extends" chain. visited
// guards against a cycle between two catalog files.
func loadVersion(version string, visited map[string]bool) (*Catalog, error) {
	name := strings.TrimSpace(version)
	if name == "" {
		return nil, errors.New("plugin catalog version is required")
	}
	if !validVersionName(name) {
		return nil, fmt.Errorf("plugin catalog version %q: invalid name", name)
	}
	if visited[name] {
		return nil, fmt.Errorf("plugin catalog version %q: extends cycle", name)
	}
	visited[name] = true
	body, err := fs.ReadFile(dataFS, path.Join("data", name+".yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: version %q has no plugin catalog", ErrUnknownPlugin, name)
		}
		return nil, fmt.Errorf("read plugin catalog data/%s.yaml: %w", name, err)
	}
	return parseVersion(name, body, visited)
}

// LoadAll returns every embedded plugin catalog keyed by version.
func LoadAll() (map[string]*Catalog, error) {
	entries, err := fs.ReadDir(dataFS, "data")
	if err != nil {
		return nil, fmt.Errorf("read plugin catalog data dir: %w", err)
	}
	out := make(map[string]*Catalog, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		version := strings.TrimSuffix(e.Name(), ".yaml")
		body, err := fs.ReadFile(dataFS, path.Join("data", e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read plugin catalog %s: %w", e.Name(), err)
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
// filesystem path traversal.
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

// catalogDoc is one YAML document of a catalog file. The optional
// "extends" key names the catalog version a file inherits from; every
// other key describes one plugin.
type catalogDoc struct {
	Extends    string `yaml:"extends"`
	PluginSpec `yaml:",inline"`
}

func parseVersion(version string, body []byte, visited map[string]bool) (*Catalog, error) {
	var (
		entries []PluginSpec
		extends string
	)
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	for {
		var d catalogDoc
		if err := dec.Decode(&d); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse plugin catalog %s: %w", version, err)
		}
		if d.ID == "" && d.Title == "" && d.Module == "" {
			if d.Extends != "" {
				if extends != "" {
					return nil, fmt.Errorf("plugin catalog %s: duplicate extends header", version)
				}
				if len(entries) > 0 {
					return nil, fmt.Errorf("plugin catalog %s: extends header must be the first document", version)
				}
				extends = d.Extends
			}
			continue
		}
		if err := d.Validate(); err != nil {
			return nil, fmt.Errorf("plugin catalog %s: %w", version, err)
		}
		entries = append(entries, d.PluginSpec)
	}

	var parent *Catalog
	if extends != "" {
		p, err := loadVersion(extends, visited)
		if err != nil {
			return nil, fmt.Errorf("plugin catalog %s: %w", version, err)
		}
		parent = p
	}

	specs := map[string]PluginSpec{}
	if parent != nil {
		for key, spec := range parent.specs {
			specs[key] = spec
		}
	}
	own := map[string]struct{}{}
	for _, e := range entries {
		key := strings.ToLower(e.ID)
		if _, dup := own[key]; dup {
			return nil, fmt.Errorf("plugin catalog %s: duplicate plugin %q", version, e.ID)
		}
		own[key] = struct{}{}
		specs[key] = e
	}
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return &Catalog{Version: version, specs: specs, order: ids}, nil
}
