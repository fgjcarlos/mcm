package conf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIncludeResolver_MissingDirIsSilent asserts that an include_dir
// pointing at a directory that does not exist is not an error. Mosquitto
// itself is tolerant of missing include_dir targets.
func TestIncludeResolver_MissingDirIsSilent(t *testing.T) {
	root := t.TempDir()
	top, err := ParseString("include_dir /missing\nlistener 1883\n", filepath.Join(root, "mosquitto.conf"))
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	res := NewIncludeResolver(root)
	merged, snap, err := res.Resolve(top)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(snap.Files) != 0 {
		t.Errorf("missing include_dir must produce empty snapshot, got %d files", len(snap.Files))
	}
	if len(merged.Items) == 0 {
		t.Error("merged file must still carry the top-level directives")
	}
}

// TestIncludeResolver_CollectsFiles imports direct .conf children in lexical
// order and leaves nested directories alone, matching Mosquitto include_dir.
func TestIncludeResolver_CollectsFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.conf"), []byte("listener 1883 0.0.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "b.conf"), []byte("allow_anonymous true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	top, err := ParseString("include_dir .\n", filepath.Join(root, "mosquitto.conf"))
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	res := NewIncludeResolver(root)
	merged, snap, err := res.Resolve(top)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(snap.Files) != 1 || snap.Files[0].Path != "a.conf" {
		t.Errorf("snapshot files = %+v, want only direct child a.conf", snap.Files)
	}
	var sawDirect, sawNested bool
	for _, item := range merged.Items {
		if item.Kind != ItemDirective {
			continue
		}
		sawDirect = sawDirect || item.Key == "listener"
		sawNested = sawNested || item.Key == "allow_anonymous"
	}
	if !sawDirect {
		t.Error("direct .conf child was not imported")
	}
	if sawNested {
		t.Error("nested .conf file was imported, but Mosquitto include_dir is non-recursive")
	}
}

// TestIncludeResolver_RefusesSymlinkEscape asserts that an include_dir
// whose symlink resolves outside the configured root is refused.
func TestIncludeResolver_RefusesSymlinkEscape(t *testing.T) {
	if os.Getenv("GOOS") == "windows" {
		t.Skip("symlink semantics differ on Windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evil.conf"), []byte("evil yes\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	top, err := ParseString("include_dir escape\n", filepath.Join(root, "mosquitto.conf"))
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	res := NewIncludeResolver(root)
	if _, _, err := res.Resolve(top); err == nil {
		t.Fatal("Resolve should have refused the symlink escape")
	} else if !strings.Contains(err.Error(), "escape") {
		t.Errorf("error did not mention escape: %v", err)
	}
}
