package diagnostics_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/diagnostics"
)

// TestProbeClientIDPrefixMatchesConnectionTestUI pins a cross-language
// contract: the onboarding connection test (issue #353) ignores broker
// connections whose client id starts with the activation probe prefix, so a
// probe connection never counts as the operator's client. The UI keeps its
// own copy of the prefix; if the two drift apart, the probe would be shown
// as "connection observed". Go cannot import the TypeScript constant, so
// this test reads the source and compares the literal.
func TestProbeClientIDPrefixMatchesConnectionTestUI(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "features", "mqtt-users", "ConnectionTestStep.tsx")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	want := "MCM_PROBE_CLIENT_PREFIX = '" + diagnostics.ProbeClientIDPrefix + "'"
	if !strings.Contains(string(source), want) {
		t.Fatalf("%s does not declare %q; keep it equal to diagnostics.ProbeClientIDPrefix", path, want)
	}
}
