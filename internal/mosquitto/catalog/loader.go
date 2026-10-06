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
	name := strings.TrimSpace(version)
	if name == "" {
		return nil, errors.New("catalog version is required")
	}
	if !validVersionName(name) {
		return nil, fmt.Errorf("catalog version %q: invalid name", name)
	}
	body, err := fs.ReadFile(dataFS, path.Join("data", name+".yaml"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: version %q has no catalog", ErrUnknownDirective, name)
		}
		return nil, fmt.Errorf("read catalog data/%s.yaml: %w", name, err)
	}
	return parseVersion(name, body)
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
		cat, err := parseVersion(version, body)
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

func parseVersion(version string, body []byte) (*Catalog, error) {
	var entries []DirectiveSpec
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	for {
		var d DirectiveSpec
		if err := dec.Decode(&d); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse catalog %s: %w", version, err)
		}
		if d.Name == "" && d.Type == "" {
			continue
		}
		if err := d.Validate(DefaultSentinels); err != nil {
			return nil, fmt.Errorf("catalog %s: %w", version, err)
		}
		entries = append(entries, d)
	}
	if len(entries) == 0 {
		// An empty / stub catalog is valid — it just means "no
		// MCM-managed directives for this version yet". Loader
		// callers (e.g. LoadAll) still want the Catalog back so they
		// can enumerate versions.
		entries = nil
	}
	specs := make(map[string]DirectiveSpec, len(entries))
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		key := strings.ToLower(e.Name)
		if _, dup := specs[key]; dup {
			return nil, fmt.Errorf("catalog %s: duplicate directive %q", version, e.Name)
		}
		specs[key] = e
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return &Catalog{Version: version, specs: specs, knownNames: names}, nil
}
