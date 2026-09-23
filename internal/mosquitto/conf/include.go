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

// maxIncludeDepth bounds recursive include_dir expansion. Eight is enough
// for any plausible Mosquitto deployment and prevents infinite recursion
// from a self-referential include_dir.
const maxIncludeDepth = 8

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

// ErrIncludeCycle is returned when include_dir expansion loops back on a
// directory already visited in the chain.
var ErrIncludeCycle = errors.New("include_dir cycle detected")

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
// The visited set is keyed by the resolved inode (device+inode) so a
// cycle created by symlinks or hardlinks is detected even when paths
// differ.
func (r *IncludeResolver) Resolve(top *File) (*File, IncludeSnapshot, error) {
	if r.root == "" {
		return nil, IncludeSnapshot{}, errors.New("include resolver: root is empty")
	}
	snapshot := IncludeSnapshot{Root: r.root}
	visited := make(map[string]string) // resolved path → SHA
	visitedInodes := make(map[uint64]string)

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
		if err := r.walk(out, dir, pattern, &snapshot, visited, visitedInodes, 0); err != nil {
			return nil, snapshot, err
		}
	}
	// Snapshot was populated in lexical order by walk().
	sort.SliceStable(snapshot.Files, func(i, j int) bool {
		return snapshot.Files[i].Path < snapshot.Files[j].Path
	})
	return out, snapshot, nil
}

// walk descends one include_dir step, recursively visiting sub-directories
// up to maxIncludeDepth. It writes the parsed Items of every regular file
// matching the optional glob into dst, in lexical order. Symlinks are
// evaluated and the resolved inode is recorded — if it falls outside the
// configured root or has already been visited in this chain, the walk
// aborts.
func (r *IncludeResolver) walk(dst *File, dir, pattern string, snapshot *IncludeSnapshot, visited map[string]string, visitedInodes map[uint64]string, depth int) error {
	if depth > maxIncludeDepth {
		return fmt.Errorf("include_dir: max depth %d exceeded at %q", maxIncludeDepth, dir)
	}
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
	inode, err := inodeOf(resolved)
	if err != nil {
		return err
	}
	if _, dup := visitedInodes[inode]; dup {
		return fmt.Errorf("%w: directory %q already visited", ErrIncludeCycle, resolved)
	}
	visitedInodes[inode] = resolved

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
		if info.IsDir() {
			// include_dir is not recursive by default in Mosquitto,
			// but we recurse to honour nested layouts operators
			// commonly use for ACL/cert files.
			subdir, _ := filepath.Rel(r.root, childPath)
			if err := r.walk(dst, subdir, pattern, snapshot, visited, visitedInodes, depth+1); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
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
		content, err := os.ReadFile(childPath)
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