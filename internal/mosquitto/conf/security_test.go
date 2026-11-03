package conf

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestIncludeResolverRejectsEscapingFileSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.conf")
	if err := os.WriteFile(outside, []byte("remote_password private-value\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "parts"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "parts", "exfiltrate.conf")); err != nil {
		t.Fatal(err)
	}
	top, err := ParseString("include_dir parts\n", filepath.Join(root, "mosquitto.conf"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = NewIncludeResolver(root).Resolve(top)
	if !errors.Is(err, ErrIncludeSymlinkEscape) {
		t.Fatalf("escaping include was read: %v", err)
	}
}
