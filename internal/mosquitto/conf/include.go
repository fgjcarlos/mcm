package conf

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IncludeSnapshot is the serialisable summary of every file pulled in
// by an include_dir tree. The HTTP API returns it as JSON so the
// operator can audit what was actually imported.
type IncludeSnapshot struct {
	Root  string             `json:"root"`
	Files []IncludeFileEntry `json:"files"`
}

// IncludeFileEntry is one file in the include tree: relative path, byte
// size, and SHA-256 hash of the on-disk content at import time. The
// preview/apply cycle records this so a later drift check can refuse to
// apply against a tree that no longer matches the snapshot.
type IncludeFileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
	SHA  string `json:"sha256"`
}

// ErrIncludeSymlinkEscape is returned when an include_dir traversal
// follows a symlink that exits the configured root. We refuse to read
// outside the root because the importer must present a deterministic
// snapshot regardless of the host filesystem's link layout.
var ErrIncludeSymlinkEscape = errors.New("include_dir traversal would escape root")

// IncludeResolver walks a configuration file's include_dir directives
// and concatenates every included file's parsed AST after the top-level
// file. The original include_dir directives are kept in the returned
// File.Items so a round-trip preserves them (operators usually want the
// includes to remain visible).
//
// root is the directory the top-level file lives in (used as the base
// for relative include_dir paths and as the symlink-escape boundary).
type IncludeResolver struct {
	root string
}

// NewIncludeResolver pins the resolution root. Pass the directory
// containing the top-level mosquitto.conf — the resolver refuses to
// follow symlinks that exit this root.
func NewIncludeResolver(root string) *IncludeResolver {
	return &IncludeResolver{root: filepath.Clean(root)}
}

// Resolve parses top, follows its include_dir directives, and returns a
// single *File with every include's Items appended in lexical order.
// Files are read with os.ReadFile (not a Reader) because include_dir is
// intrinsically a filesystem concept.
//
// A visited-file set avoids adding the same root-relative file twice when
// include_dir entries overlap.
func (r *IncludeResolver) Resolve(top *File) (*File, IncludeSnapshot, error) {
	if r.root == "" {
		return nil, IncludeSnapshot{}, errors.New("include resolver: root is empty")
	}
	snapshot := IncludeSnapshot{Root: r.root}
	visited := make(map[string]string) // root-relative path → SHA

	out := &File{Path: top.Path}
	out.Items = append(out.Items, top.Items...)

	for _, it := range top.Items {
		if it.Kind != ItemDirective {
			continue
		}
		if it.Key != "include_dir" {
			continue
		}
		// include_dir syntax: include_dir path [pattern]. Mosquitto
		// applies an optional glob after the path. We honour the glob
		// when present and fall back to "everything" otherwise.
		if len(it.Values) == 0 {
			return nil, snapshot, fmt.Errorf("include_dir at line %d has no path", it.Line)
		}
		dir := it.Values[0]
		pattern := ""
		if len(it.Values) > 1 {
			pattern = strings.Join(it.Values[1:], " ")
		}
		if err := r.walk(out, dir, pattern, &snapshot, visited); err != nil {
			return nil, snapshot, err
		}
	}
	// Snapshot was populated in lexical order by walk().
	sort.SliceStable(snapshot.Files, func(i, j int) bool {
		return snapshot.Files[i].Path < snapshot.Files[j].Path
	})
	return out, snapshot, nil
}

// walk reads only direct regular .conf children of one include_dir, in
// lexical order. Mosquitto does not recurse into subdirectories. Symlinked
// .conf files are evaluated and refused if they escape the configured root.
func (r *IncludeResolver) walk(dst *File, dir, pattern string, snapshot *IncludeSnapshot, visited map[string]string) error {
	abs, err := filepath.Abs(filepath.Join(r.root, dir))
	if err != nil {
		return fmt.Errorf("include_dir %q: %w", dir, err)
	}
	// filepath.Join treats the second arg as relative even when it's
	// absolute; collapse the accidental duplication so an absolute
	// include_dir path resolves to itself.
	if filepath.IsAbs(dir) {
		abs = filepath.Clean(dir)
	}
	// EvalSymlinks guards the root boundary.
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Missing include_dir is non-fatal — Mosquitto itself
			// tolerates a missing include. Skip silently.
			return nil
		}
		return fmt.Errorf("include_dir %q: resolve %w", dir, err)
	}
	if !strings.HasPrefix(resolved, r.root+string(filepath.Separator)) && resolved != r.root {
		return fmt.Errorf("%w: include_dir %q resolves to %q (root %q)", ErrIncludeSymlinkEscape, dir, resolved, r.root)
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return fmt.Errorf("read include_dir %q: %w", resolved, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		childPath := filepath.Join(resolved, name)
		info, err := os.Stat(childPath)
		if err != nil {
			return fmt.Errorf("stat %q: %w", childPath, err)
		}
		// Mosquitto ignores subdirectories. Check this before resolving
		// symlinks so an ignored nested directory cannot affect import.
		if info.IsDir() || !info.Mode().IsRegular() || filepath.Ext(name) != ".conf" {
			continue
		}
		actualPath, err := filepath.EvalSymlinks(childPath)
		if err != nil {
			return err
		}
		if actualPath != r.root && !strings.HasPrefix(actualPath, r.root+string(filepath.Separator)) {
			return fmt.Errorf("%w: included file %q", ErrIncludeSymlinkEscape, name)
		}
		if filepath.Ext(actualPath) != ".conf" {
			continue
		}
		if pattern != "" {
			ok, err := filepath.Match(pattern, name)
			if err != nil {
				return fmt.Errorf("include_dir glob %q: %w", pattern, err)
			}
			if !ok {
				continue
			}
		}
		rel, _ := filepath.Rel(r.root, childPath)
		if _, dup := visited[rel]; dup {
			continue
		}
		// OpenRoot also enforces confinement at open time, closing the
		// symlink-swap gap between EvalSymlinks and reading the bytes.
		readRoot, err := os.OpenRoot(r.root)
		if err != nil {
			return err
		}
		content, err := readRoot.ReadFile(rel)
		_ = readRoot.Close()
		if err != nil {
			return fmt.Errorf("read include %q: %w", childPath, err)
		}
		parsed, err := Parse(bytesReader(content), rel)
		if err != nil {
			return fmt.Errorf("parse include %q: %w", rel, err)
		}
		// Skip empty / comment-only files silently — Mosquitto does.
		if hasNoDirectives(parsed) {
			continue
		}
		dst.Items = append(dst.Items, parsed.Items...)
		visited[rel] = hashSHA256(content)
		snapshot.Files = append(snapshot.Files, IncludeFileEntry{
			Path: rel,
			Size: info.Size(),
			SHA:  visited[rel],
		})
	}
	return nil
}

// hasNoDirectives returns true when the file contains only comments / blank
// lines. Mosquitto itself considers such files inert.
func hasNoDirectives(f *File) bool {
	for _, it := range f.Items {
		if it.Kind == ItemDirective || it.Kind == ItemBlockOpen {
			return false
		}
	}
	return true
}
