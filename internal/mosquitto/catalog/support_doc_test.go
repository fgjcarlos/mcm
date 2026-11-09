package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// supportMatrixDocPath is the published matrix page, relative to this
// package directory.
const supportMatrixDocPath = "../../../docs/support-matrix.md"

// TestSupportMatrixDocIsCurrent proves the published matrix page matches
// the canonical catalog and upstream inventories. Without this guard the
// page would drift from the code and could claim a support level the
// product does not deliver.
//
// The comparison ignores the per-directive lists: the page is long and
// the counts plus the section headers are what an operator reads first.
// The counts are derived from the same matrix, so a mismatch in them
// means the page is stale.
func TestSupportMatrixDocIsCurrent(t *testing.T) {
	path, err := filepath.Abs(supportMatrixDocPath)
	if err != nil {
		t.Fatalf("resolve %s: %v", supportMatrixDocPath, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (regenerate with `go run ./scripts/gen-support-matrix`)", path, err)
	}
	doc := string(data)

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
		counts := matrix.Counts()
		want := "The Mosquitto " + version + " manual documents " +
			itoa(len(upstream)) + " directives. MCM manages " +
			itoa(counts[SupportManaged]) + " of them,\n" +
			"partially supports " + itoa(counts[SupportPartial]) +
			" and does not manage " + itoa(counts[SupportUnsupported]) + "."
		if !strings.Contains(doc, want) {
			t.Errorf("%s is out of sync with the catalog. Regenerate with `go run ./scripts/gen-support-matrix`.\nwant the line:\n%s", path, want)
		}
	}
}

// TestSupportMatrixDocStatesNoUniversalCompatibility proves the page
// keeps the promise the epic makes: MCM states what it does not support
// instead of claiming universal compatibility.
func TestSupportMatrixDocStatesNoUniversalCompatibility(t *testing.T) {
	path, err := filepath.Abs(supportMatrixDocPath)
	if err != nil {
		t.Fatalf("resolve %s: %v", supportMatrixDocPath, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc := string(data)
	for _, phrase := range []string{
		"does not promise compatibility with every Mosquitto release",
		"### Managed",
		"### Partial",
		"### Unsupported",
	} {
		if !strings.Contains(doc, phrase) {
			t.Errorf("%s must contain %q", path, phrase)
		}
	}
}

// itoa is a small helper so the test does not pull strconv into the
// comparison strings.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
