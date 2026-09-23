package conf

import (
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
)

const sampleConf = `# MCM-managed Mosquitto configuration
# Auto-imported from the host on first boot.
persistence true
persistence_location /var/lib/mosquitto/

log_dest stdout
log_dest file /var/log/mosquitto/mosquitto.log
log_type error
log_type warning
connection_messages true

allow_anonymous false
password_file /etc/mosquitto/passwd
acl_file /etc/mosquitto/acl

# --- listeners ---
listener 1883 0.0.0.0 {
protocol mqtt
}

listener 9001 0.0.0.0 {
protocol websockets
}

# Unknown / future directive must round-trip.
some_future_setting foo bar baz
`

func TestParseConf_PreservesComments(t *testing.T) {
	f, err := ParseString(sampleConf, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	var comments int
	for _, it := range f.Items {
		if it.Kind == ItemComment && strings.HasPrefix(strings.TrimSpace(it.Text), "#") {
			comments++
		}
	}
	if comments < 4 {
		t.Errorf("comments = %d, want >= 4 (sample has 4 leading + inline #)", comments)
	}

	rendered, err := f.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(rendered) != sampleConf {
		t.Errorf("round-trip mismatch:\n--- got ---\n%s\n--- want ---\n%s", rendered, sampleConf)
	}
}

func TestParseConf_PreservesUnknownDirectives(t *testing.T) {
	f, err := ParseString(sampleConf, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	var found bool
	for _, it := range f.Items {
		if it.Kind == ItemDirective && it.Key == "some_future_setting" {
			found = true
			if len(it.Values) != 3 || it.Values[0] != "foo" || it.Values[2] != "baz" {
				t.Errorf("unknown directive values = %v", it.Values)
			}
		}
	}
	if !found {
		t.Fatal("some_future_setting directive must be preserved as ItemDirective")
	}
}

func TestParseConf_RoundTrip(t *testing.T) {
	f, err := ParseString(sampleConf, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	rendered, err := f.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(rendered) != sampleConf {
		t.Fatalf("first render diverged from input")
	}
	// Parse the rendered bytes again — they must still round-trip.
	f2, err := ParseString(string(rendered), "mosquitto.conf")
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	rendered2, err := f2.Render()
	if err != nil {
		t.Fatalf("Render #2: %v", err)
	}
	if string(rendered2) != sampleConf {
		t.Fatalf("second render diverged")
	}
}

func TestParseConf_ListenerBlocks(t *testing.T) {
	f, err := ParseString(sampleConf, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	blocks := f.ListenerBlocks()
	if len(blocks) != 2 {
		t.Fatalf("listener blocks = %d, want 2", len(blocks))
	}
	if blocks[0].Header.Values[0] != "1883" || blocks[0].Header.Values[1] != "0.0.0.0" {
		t.Errorf("block 0 header = %+v", blocks[0].Header)
	}
	if len(blocks[0].Body) != 1 || blocks[0].Body[0].Key != "protocol" || blocks[0].Body[0].Values[0] != "mqtt" {
		t.Errorf("block 0 body = %+v", blocks[0].Body)
	}
}

func TestRenderConf_Diff(t *testing.T) {
	a, err := ParseString(sampleConf, "a.conf")
	if err != nil {
		t.Fatalf("ParseString a: %v", err)
	}
	b, err := ParseString(strings.Replace(sampleConf, "persistence true", "persistence false", 1), "b.conf")
	if err != nil {
		t.Fatalf("ParseString b: %v", err)
	}
	ra, _ := a.Render()
	rb, _ := b.Render()
	diff, err := Diff("a.conf", "b.conf", ra, rb)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "-persistence true") || !strings.Contains(diff, "+persistence false") {
		t.Errorf("diff did not pick up the persistence change:\n%s", diff)
	}
}

func TestValidate_Happy(t *testing.T) {
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	f, err := ParseString(sampleConf, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	issues := Validate(f, cat)
	// The sample intentionally contains one unknown directive
	// (`some_future_setting`) to prove the importer preserves unknown
	// entries. That single issue is expected and is the only one.
	if len(issues) != 1 || issues[0].Kind != "unknown" || issues[0].Directive != "some_future_setting" {
		t.Errorf("happy-path issues = %+v, want exactly one 'unknown' for some_future_setting", issues)
	}
}

func TestValidate_SadUnknownDirective(t *testing.T) {
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	bad := sampleConf + "\nnot_a_real_directive yes\n"
	f, err := ParseString(bad, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	issues := Validate(f, cat)
	var foundUnknown bool
	for _, iss := range issues {
		if iss.Kind == "unknown" && iss.Directive == "not_a_real_directive" {
			foundUnknown = true
		}
	}
	if !foundUnknown {
		t.Errorf("expected an 'unknown' issue for not_a_real_directive, got: %+v", issues)
	}
}

func TestValidate_SadBadType(t *testing.T) {
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	// max_connections expects an integer; pass a word.
	bad := "max_connections too-many\n"
	f, err := ParseString(bad, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	issues := Validate(f, cat)
	var foundType bool
	for _, iss := range issues {
		if iss.Kind == "type" && iss.Directive == "max_connections" {
			foundType = true
		}
	}
	if !foundType {
		t.Errorf("expected a 'type' issue for max_connections, got: %+v", issues)
	}
}

func TestValidate_SadScopeOutsideListener(t *testing.T) {
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	// protocol is a listener-scope directive; placing it at global scope is wrong.
	bad := "protocol mqtt\n"
	f, err := ParseString(bad, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	issues := Validate(f, cat)
	var foundScope bool
	for _, iss := range issues {
		if iss.Kind == "scope" && iss.Directive == "protocol" {
			foundScope = true
		}
	}
	if !foundScope {
		t.Errorf("expected a 'scope' issue for protocol at global scope, got: %+v", issues)
	}
}

func TestValidate_SadMultiplicity(t *testing.T) {
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	// allow_anonymous must appear once.
	bad := "allow_anonymous true\nallow_anonymous false\n"
	f, err := ParseString(bad, "mosquitto.conf")
	if err != nil {
		t.Fatalf("ParseString: %v", err)
	}
	issues := Validate(f, cat)
	var found bool
	for _, iss := range issues {
		if iss.Kind == "multiplicity" && iss.Directive == "allow_anonymous" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected multiplicity issue, got: %+v", issues)
	}
}