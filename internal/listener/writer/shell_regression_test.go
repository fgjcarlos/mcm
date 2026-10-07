//go:build shellregression

package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// TestShellRegressionApplyAndClear is the driver invoked by
// scripts/tests/test-e2e-listener-writer.sh. It is excluded from the
// default `go test ./...` run via the shellregression build tag so the
// always-on test surface stays focused on unit-level invariants.
//
// The driver exercises:
//
//   - apply preserves every non-listener directive
//   - clear removes every listener block and protocol continuation
//   - two consecutive applies are byte-identical (idempotence)
//   - snapshot/restore round-trip works against a real temp directory
//   - non-directory ConfigDir parent surfaces as an explicit error
func TestShellRegressionApplyAndClear(t *testing.T) {
	confPath := os.Getenv("LISTENER_WRITER_CONF")
	if confPath == "" {
		t.Skip("LISTENER_WRITER_CONF not set; shell harness controls")
	}
	specs := []listeners.ListenerSpec{
		{ID: "mqtt", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
		{ID: "ws", Port: 9001, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolWebSocket}},
		{ID: "e2e", Port: 1884, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
	}
	w := New(confPath)
	if _, err := w.Write(specs); err != nil {
		t.Fatalf("apply: %v", err)
	}
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	contents := string(body)
	mustContain := func(s string) {
		t.Helper()
		if !strings.Contains(contents, s) {
			t.Fatalf("missing %q in:\n%s", s, contents)
		}
	}
	mustContain("listener 1883 0.0.0.0")
	mustContain("listener 9001 0.0.0.0")
	mustContain("listener 1884 0.0.0.0")
	mustContain("protocol websockets")
	mustContain("persistence true")
	mustContain("password_file /mosquitto/config/passwd")
	mustContain("acl_file /mosquitto/config/acl")
	mustContain("user root")

	if _, err := w.Write(nil); err != nil {
		t.Fatalf("clear: %v", err)
	}
	body, _ = os.ReadFile(confPath)
	contents = string(body)
	if strings.Contains(contents, "listener 1883 0.0.0.0") {
		t.Fatalf("clear did not drop legacy listener 1883:\n%s", contents)
	}
	if strings.Contains(contents, "listener 1884 0.0.0.0") {
		t.Fatalf("clear did not drop listener 1884:\n%s", contents)
	}
	if strings.Contains(contents, "protocol websockets") {
		t.Fatalf("clear did not drop websockets continuation:\n%s", contents)
	}

	if _, err := w.Write(specs); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	firstSnap, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("snapshot read 1: %v", err)
	}
	if _, err := w.Write(specs); err != nil {
		t.Fatalf("third apply: %v", err)
	}
	secondSnap, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("snapshot read 2: %v", err)
	}
	if string(firstSnap) != string(secondSnap) {
		t.Fatalf("apply is not idempotent:\nbefore=%q\nafter=%q", string(firstSnap), string(secondSnap))
	}

	snapBytes, err := w.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if string(snapBytes) != string(firstSnap) {
		t.Fatalf("snapshot mismatch:\nsnap=%q\nfileBytes=%q", string(snapBytes), string(firstSnap))
	}

	if _, err := w.Write(nil); err != nil {
		t.Fatalf("clear before restore: %v", err)
	}
	if err := w.Restore(snapBytes); err != nil {
		t.Fatalf("restore: %v", err)
	}
	body, _ = os.ReadFile(confPath)
	if string(body) != string(snapBytes) {
		t.Fatalf("restore did not reproduce snapshot:\nsnap=%q\ngot=%q", string(snapBytes), string(body))
	}

	bogusParent := filepath.Join(filepath.Dir(confPath), "bogus")
	if err := os.WriteFile(bogusParent, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed regular file: %v", err)
	}
	if _, err := New(filepath.Join(bogusParent, "mosquitto.conf")).Write(specs); err == nil {
		t.Fatal("expected non-directory parent to surface an error")
	}
}
