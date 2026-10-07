package writer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
)

// fixedConf matches the deploy/mosquitto/config/mosquitto.conf source
// from the dev stack. It deliberately mixes inline listener lines, a
// single `protocol` continuation, and several non-listener directives
// so the writer must preserve every byte outside the listener blocks.
const fixedConf = `# MCM local development Mosquitto configuration

persistence true
persistence_location /mosquitto/data/

log_dest stdout
log_dest file /mosquitto/log/mosquitto.log
log_type error
log_type warning
log_type notice
log_type information
connection_messages true

allow_anonymous false
password_file /mosquitto/config/passwd
acl_file /mosquitto/config/acl

user root

listener 1883 0.0.0.0

listener 9001 0.0.0.0
protocol websockets
`

func TestFileWriter_RoundTripPreservesNonListenerDirectives(t *testing.T) {
	tmp := t.TempDir()
	confPath := filepath.Join(tmp, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(fixedConf), 0o644); err != nil {
		t.Fatalf("seed conf: %v", err)
	}

	specs := []listeners.ListenerSpec{
		{ID: "mqtt", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
		{ID: "ws", Port: 9001, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
		{ID: "e2e", Port: 1884, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
	}

	w := New(confPath)
	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read back conf: %v", err)
	}
	if string(body) != rendered.Full {
		t.Fatalf("disk contents differ from returned Full:\ndisk=%q\nrendered=%q", string(body), rendered.Full)
	}

	mustContain(t, rendered.Full, "persistence true")
	mustContain(t, rendered.Full, "password_file /mosquitto/config/passwd")
	mustContain(t, rendered.Full, "acl_file /mosquitto/config/acl")
	mustContain(t, rendered.Full, "user root")
	mustContain(t, rendered.Full, "listener 1883 0.0.0.0")
	mustContain(t, rendered.Full, "listener 9001 0.0.0.0")
	mustContain(t, rendered.Full, "listener 1884 0.0.0.0")
	if strings.Count(rendered.Full, "listener") != 3 {
		t.Fatalf("expected exactly 3 listener blocks in spliced output, got %d:\n%s", strings.Count(rendered.Full, "listener"), rendered.Full)
	}
	if strings.Count(rendered.Full, "protocol") != 3 {
		t.Fatalf("expected exactly 3 protocol continuations (mqtt for 1883/9001/1884), got %d:\n%s", strings.Count(rendered.Full, "protocol"), rendered.Full)
	}
}

func TestFileWriter_EmptySpecsRemovesLegacyListeners(t *testing.T) {
	tmp := t.TempDir()
	confPath := filepath.Join(tmp, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(fixedConf), 0o644); err != nil {
		t.Fatalf("seed conf: %v", err)
	}

	w := New(confPath)
	rendered, err := w.Write(nil)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	mustNotIncludeLine(t, rendered.Full, "listener 1883 0.0.0.0")
	mustNotIncludeLine(t, rendered.Full, "listener 9001 0.0.0.0")
	mustNotIncludeLine(t, rendered.Full, "protocol websockets")
	mustContain(t, rendered.Full, "persistence true")
}

func TestFileWriter_MissingFileUsesListenerBlockAsBody(t *testing.T) {
	tmp := t.TempDir()
	confPath := filepath.Join(tmp, "mosquitto.conf")

	specs := []listeners.ListenerSpec{
		{ID: "mqtt", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}},
	}
	w := New(confPath)
	rendered, err := w.Write(specs)
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	mustContain(t, rendered.Full, "listener 1883 0.0.0.0")
	if strings.Contains(rendered.Full, "\n\nlistener") {
		t.Fatalf("expected no leading blank line when head is empty, got %q", rendered.Full)
	}
}

func TestFileWriter_RejectsEmptyConfigPath(t *testing.T) {
	w := New("")
	_, err := w.Write([]listeners.ListenerSpec{{Port: 1883}})
	if !errors.Is(err, ErrConfigPathEmpty) {
		t.Fatalf("Write error = %v, want ErrConfigPathEmpty", err)
	}
}

func TestFileWriter_RejectsNonDirectoryParent(t *testing.T) {
	tmp := t.TempDir()
	bogus := filepath.Join(tmp, "regular-file")
	if err := os.WriteFile(bogus, []byte("not a dir"), 0o644); err != nil {
		t.Fatalf("seed regular file: %v", err)
	}
	w := New(filepath.Join(bogus, "mosquitto.conf"))
	_, err := w.Write([]listeners.ListenerSpec{{Port: 1883}})
	if !errors.Is(err, ErrConfigPathNotDirectory) {
		t.Fatalf("Write error = %v, want ErrConfigPathNotDirectory", err)
	}
}

func TestFileWriter_RenderSpecErrorIsSurfaced(t *testing.T) {
	tmp := t.TempDir()
	confPath := filepath.Join(tmp, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(fixedConf), 0o644); err != nil {
		t.Fatalf("seed conf: %v", err)
	}
	w := New(confPath)
	_, err := w.Write([]listeners.ListenerSpec{{ID: "bad", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{"bogus"}}})
	if !errors.Is(err, ErrSpecRender) {
		t.Fatalf("Write error = %v, want ErrSpecRender", err)
	}
	body, err := os.ReadFile(confPath)
	if err != nil {
		t.Fatalf("read back conf: %v", err)
	}
	if !strings.Contains(string(body), "listener 1883 0.0.0.0") {
		t.Fatalf("expected file to be untouched on render failure, got %q", string(body))
	}
}

func TestFileWriter_PropagatesAtomicWriteError(t *testing.T) {
	tmp := t.TempDir()
	confPath := filepath.Join(tmp, "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte(fixedConf), 0o644); err != nil {
		t.Fatalf("seed conf: %v", err)
	}
	w := &FileWriter{
		configPath: confPath,
		FileMode:   FileMode,
		Reader:     os.ReadFile,
		Writer: func(path, content string, perm os.FileMode) error {
			return errors.New("disk full")
		},
	}
	_, err := w.Write([]listeners.ListenerSpec{{ID: "mqtt", Port: 1883, Bind: "0.0.0.0", Protocols: []listeners.Protocol{listeners.ProtocolMQTT}}})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Write error = %v, want disk-full propagation", err)
	}
}

func TestFilterListenerItems_HandlesBracedBlocks(t *testing.T) {
	body := `listener 1883 0.0.0.0
listener 1884 0.0.0.0 {
    protocol mqtt
}
allow_anonymous false
listener 9001 0.0.0.0
protocol websockets
`
	parsed, err := parseForTest(t, body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	filtered := filterListenerItems(parsed.Items)
	out := mustRender(t, parsed, filtered)
	mustNotIncludeLine(t, out, "listener 1883")
	mustNotIncludeLine(t, out, "listener 1884 0.0.0.0")
	mustNotIncludeLine(t, out, "listener 9001")
	mustNotIncludeLine(t, out, "protocol websockets")
	mustContain(t, out, "allow_anonymous false")
}

func parseForTest(t *testing.T, body string) (*confFile, error) {
	t.Helper()
	return parseConf(body)
}

func mustRender(t *testing.T, parsed *confFile, items []confItem) string {
	t.Helper()
	cp := *parsed
	cp.Items = items
	out, err := cp.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return string(out)
}

func mustContain(t *testing.T, body, fragment string) {
	t.Helper()
	if !strings.Contains(body, fragment) {
		t.Fatalf("expected body to contain %q, got:\n%s", fragment, body)
	}
}

func mustNotIncludeLine(t *testing.T, body, fragment string) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == strings.TrimSpace(fragment) {
			t.Fatalf("body still contains removed line %q:\n%s", fragment, body)
		}
	}
}

// Type aliases keep the helper signatures short while still being
// exactly the conf package's types.
type confItem = conf.Item
type confFile = conf.File

func parseConf(body string) (*conf.File, error) {
	return conf.ParseString(body, "mosquitto.conf")
}