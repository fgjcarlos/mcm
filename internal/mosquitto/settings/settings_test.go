package settings

import (
	"os"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/conf"
)

func testCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return cat
}

// devConf is the configuration MCM ships for the local stack.
func devConf(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("../../../deploy/mosquitto/config/mosquitto.conf")
	if err != nil {
		t.Fatalf("read dev conf: %v", err)
	}
	return body
}

func parse(t *testing.T, body string) *conf.File {
	t.Helper()
	f, err := conf.ParseString(body, "mosquitto.conf")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return f
}

func render(t *testing.T, f *conf.File) string {
	t.Helper()
	out, err := f.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return string(out)
}

// TestExtract_DevConf checks that the settings of the shipped
// configuration are read back correctly.
func TestExtract_DevConf(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, string(devConf(t)))
	doc := Extract(f, cat)

	values, ok := doc.Values("persistence")
	if !ok || len(values) != 1 || values[0] != "true" {
		t.Errorf("persistence = %v (present=%v), want [true]", values, ok)
	}
	values, ok = doc.Values("persistence_location")
	if !ok || len(values) != 1 || values[0] != "/mosquitto/data/" {
		t.Errorf("persistence_location = %v (present=%v), want [/mosquitto/data/]", values, ok)
	}
	values, ok = doc.Values("connection_messages")
	if !ok || len(values) != 1 || values[0] != "true" {
		t.Errorf("connection_messages = %v (present=%v), want [true]", values, ok)
	}
	// A directive the file does not set is listed as unset so the form
	// can show the broker default.
	values, ok = doc.Values("autosave_interval")
	if !ok || values != nil {
		t.Errorf("autosave_interval = %v (present=%v), want unset", values, ok)
	}
	// Directives MCM does not manage are not part of the document.
	if _, ok := doc.Values("allow_anonymous"); ok {
		t.Error("allow_anonymous must not be part of the settings document")
	}
}

// TestExtract_Repeatable checks that a repeatable directive keeps every
// occurrence.
func TestExtract_Repeatable(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, "log_dest stdout\nlog_dest file /var/log/mosquitto.log\nlog_type error\nlog_type warning\n")
	doc := Extract(f, cat)

	var dests, types int
	for _, e := range doc.Entries {
		switch e.Directive {
		case "log_dest":
			dests++
		case "log_type":
			types++
		}
	}
	if dests != 2 {
		t.Errorf("log_dest entries = %d, want 2", dests)
	}
	if types != 2 {
		t.Errorf("log_type entries = %d, want 2", types)
	}
}

// TestRoundTrip_ByteExact is the core guarantee of the settings editor:
// loading the settings of a file and saving them back must not change a
// single byte of it.
func TestRoundTrip_ByteExact(t *testing.T) {
	cat := testCatalog(t)
	original := string(devConf(t))
	f := parse(t, original)

	doc := Extract(f, cat)
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("changed directives = %v, want none", changed)
	}
	if got := render(t, out); got != original {
		t.Errorf("round-trip changed the file:\n--- got ---\n%s\n--- want ---\n%s", got, original)
	}
}

// TestRoundTrip_ByteExact_UnknownDirectives makes sure directives MCM
// does not know survive the round-trip untouched.
func TestRoundTrip_ByteExact_UnknownDirectives(t *testing.T) {
	cat := testCatalog(t)
	original := "# a comment\nfuture_directive some value\npersistence true\n\nlistener 1883 0.0.0.0\n"
	f := parse(t, original)
	doc := Extract(f, cat)
	out, _, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := render(t, out); got != original {
		t.Errorf("round-trip changed the file:\n--- got ---\n%s\n--- want ---\n%s", got, original)
	}
}

// TestApply_ChangesValue rewrites the line of a directive whose value
// changed and leaves the rest of the file alone.
func TestApply_ChangesValue(t *testing.T) {
	cat := testCatalog(t)
	original := "persistence true\npersistence_location /mosquitto/data/\n\nlistener 1883 0.0.0.0\n"
	f := parse(t, original)

	doc := Extract(f, cat)
	for i := range doc.Entries {
		if doc.Entries[i].Directive == "persistence" {
			doc.Entries[i].Values = []string{"false"}
		}
	}
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != "persistence" {
		t.Errorf("changed = %v, want [persistence]", changed)
	}
	got := render(t, out)
	want := "persistence false\npersistence_location /mosquitto/data/\n\nlistener 1883 0.0.0.0\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_AddsDirective writes a directive that was not in the file
// into the global part of the file, before the first listener.
func TestApply_AddsDirective(t *testing.T) {
	cat := testCatalog(t)
	original := "persistence true\n\n# the listeners\nlistener 1883 0.0.0.0\n"
	f := parse(t, original)

	doc := Extract(f, cat)
	for i := range doc.Entries {
		if doc.Entries[i].Directive == "autosave_interval" {
			doc.Entries[i].Unset = false
			doc.Entries[i].Values = []string{"60"}
		}
	}
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != "autosave_interval" {
		t.Errorf("changed = %v, want [autosave_interval]", changed)
	}
	got := render(t, out)
	want := "persistence true\nautosave_interval 60\n\n# the listeners\nlistener 1883 0.0.0.0\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_RemovesDirective drops a directive the operator unset.
func TestApply_RemovesDirective(t *testing.T) {
	cat := testCatalog(t)
	original := "persistence true\nconnection_messages true\nlistener 1883 0.0.0.0\n"
	f := parse(t, original)

	doc := Extract(f, cat)
	for i := range doc.Entries {
		if doc.Entries[i].Directive == "connection_messages" {
			doc.Entries[i].Unset = true
			doc.Entries[i].Values = nil
		}
	}
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != "connection_messages" {
		t.Errorf("changed = %v, want [connection_messages]", changed)
	}
	got := render(t, out)
	want := "persistence true\nlistener 1883 0.0.0.0\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_AddsSeveralDirectives checks that adding more than one
// directive keeps the document order and does not lose track of the
// directives that follow.
func TestApply_AddsSeveralDirectives(t *testing.T) {
	cat := testCatalog(t)
	original := "persistence true\n\nlistener 1883 0.0.0.0\n"
	f := parse(t, original)

	doc := Extract(f, cat)
	for i := range doc.Entries {
		switch doc.Entries[i].Directive {
		case "autosave_interval":
			doc.Entries[i].Unset = false
			doc.Entries[i].Values = []string{"60"}
		case "max_queued_messages":
			doc.Entries[i].Unset = false
			doc.Entries[i].Values = []string{"5000"}
		}
	}
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 2 {
		t.Errorf("changed = %v, want two directives", changed)
	}
	got := render(t, out)
	want := "persistence true\nautosave_interval 60\nmax_queued_messages 5000\n\nlistener 1883 0.0.0.0\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_AddsRepeatableOccurrence adds a second log_dest next to the
// one that is already there.
func TestApply_AddsRepeatableOccurrence(t *testing.T) {
	cat := testCatalog(t)
	original := "log_dest stdout\npersistence true\n"
	f := parse(t, original)

	doc := Extract(f, cat)
	doc.Entries = append(doc.Entries, Entry{Directive: "log_dest", Values: []string{"file", "/var/log/mosquitto.log"}})
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != "log_dest" {
		t.Errorf("changed = %v, want [log_dest]", changed)
	}
	got := render(t, out)
	want := "log_dest stdout\nlog_dest file /var/log/mosquitto.log\npersistence true\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_KeepsListenerBlock makes sure the settings editor never
// touches the directives of a listener block.
func TestApply_KeepsListenerBlock(t *testing.T) {
	cat := testCatalog(t)
	original := "persistence true\n\nlistener 1883 0.0.0.0\n{\n  max_connections 10\n  protocol mqtt\n}\n"
	f := parse(t, original)

	doc := Extract(f, cat)
	// The listener's max_connections is not a global setting, so the
	// document must not ask for it to be written.
	if doc.IsSet("max_connections") {
		t.Error("max_connections inside a listener block must not be editable here")
	}
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 0 {
		t.Errorf("changed = %v, want none", changed)
	}
	if got := render(t, out); got != original {
		t.Errorf("round-trip changed the file:\n%q", got)
	}
}

// TestApply_LeavesUnmentionedDirectives makes sure a document that does
// not mention a directive leaves it alone.
func TestApply_LeavesUnmentionedDirectives(t *testing.T) {
	cat := testCatalog(t)
	original := "persistence true\nconnection_messages true\n"
	f := parse(t, original)

	doc := Document{Entries: []Entry{{Directive: "persistence", Values: []string{"false"}}}}
	out, changed, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changed) != 1 || changed[0] != "persistence" {
		t.Errorf("changed = %v, want [persistence]", changed)
	}
	got := render(t, out)
	want := "persistence false\nconnection_messages true\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_RejectsUnknownDirective makes sure the editor refuses a
// directive it does not own instead of writing it into the file.
func TestApply_RejectsUnknownDirective(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, "persistence true\n")
	doc := Document{Entries: []Entry{{Directive: "allow_anonymous", Values: []string{"true"}}}}
	if _, _, err := Apply(f, doc, cat); err == nil {
		t.Fatal("expected an error for a directive that is not an editable setting")
	}
}

// TestValidate covers the catalog-driven checks of the document.
func TestValidate(t *testing.T) {
	cat := testCatalog(t)
	cases := []struct {
		name     string
		entries  []Entry
		wantKind string
	}{
		{
			name:     "unknown directive",
			entries:  []Entry{{Directive: "nope", Values: []string{"1"}}},
			wantKind: "unknown",
		},
		{
			name:     "boolean without a boolean",
			entries:  []Entry{{Directive: "persistence", Values: []string{"yes"}}},
			wantKind: "type",
		},
		{
			name:     "integer below the minimum",
			entries:  []Entry{{Directive: "max_packet_size", Values: []string{"10"}}},
			wantKind: "range",
		},
		{
			name:     "integer above the maximum",
			entries:  []Entry{{Directive: "max_inflight_messages", Values: []string{"70000"}}},
			wantKind: "range",
		},
		{
			name:     "integer that is not a number",
			entries:  []Entry{{Directive: "autosave_interval", Values: []string{"soon"}}},
			wantKind: "type",
		},
		{
			name:     "value outside the enum",
			entries:  []Entry{{Directive: "log_type", Values: []string{"loud"}}},
			wantKind: "type",
		},
		{
			name:     "duration with a unit the broker does not know",
			entries:  []Entry{{Directive: "persistent_client_expiration", Values: []string{"30s"}}},
			wantKind: "type",
		},
		{
			name:     "duration that is not positive",
			entries:  []Entry{{Directive: "persistent_client_expiration", Values: []string{"0h"}}},
			wantKind: "type",
		},
		{
			name: "a directive that may only appear once",
			entries: []Entry{
				{Directive: "persistence", Values: []string{"true"}},
				{Directive: "persistence", Values: []string{"false"}},
			},
			wantKind: "multiplicity",
		},
		{
			name: "two log files",
			entries: []Entry{
				{Directive: "log_dest", Values: []string{"file", "/a.log"}},
				{Directive: "log_dest", Values: []string{"file", "/b.log"}},
			},
			wantKind: "duplicate",
		},
		{
			name: "a directive that is set and unset at the same time",
			entries: []Entry{
				{Directive: "persistence", Values: []string{"true"}},
				{Directive: "persistence", Unset: true},
			},
			wantKind: "conflict",
		},
		{
			name: "the same conflict in the other order",
			entries: []Entry{
				{Directive: "persistence", Unset: true},
				{Directive: "persistence", Values: []string{"true"}},
			},
			wantKind: "conflict",
		},
		{
			name:     "log file without a path",
			entries:  []Entry{{Directive: "log_dest", Values: []string{"file"}}},
			wantKind: "type",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := Validate(Document{Entries: tc.entries}, cat)
			if len(issues) == 0 {
				t.Fatal("expected an issue, got none")
			}
			found := false
			for _, issue := range issues {
				if issue.Kind == tc.wantKind {
					found = true
				}
			}
			if !found {
				t.Errorf("issues = %+v, want one of kind %q", issues, tc.wantKind)
			}
		})
	}
}

// TestValidate_Accepts makes sure the settings of the shipped
// configuration pass the checks.
func TestValidate_Accepts(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, string(devConf(t)))
	doc := Extract(f, cat)
	if issues := Validate(doc, cat); len(issues) != 0 {
		t.Errorf("issues = %+v, want none", issues)
	}
}

// TestValidate_DurationSuffixesAreVersionSpecific checks that Mosquitto
// 2.0 rejects the seconds suffix while 2.1 accepts it.
func TestValidate_DurationSuffixesAreVersionSpecific(t *testing.T) {
	cat20 := testCatalog(t)
	cat21, err := catalog.LoadVersion("2.1")
	if err != nil {
		t.Fatalf("load catalog 2.1: %v", err)
	}
	doc := Document{Entries: []Entry{{Directive: "persistent_client_expiration", Values: []string{"30s"}}}}
	if issues := Validate(doc, cat20); len(issues) == 0 {
		t.Error("Mosquitto 2.0 must not accept a duration in seconds")
	}
	if issues := Validate(doc, cat21); len(issues) != 0 {
		t.Errorf("Mosquitto 2.1 must accept a duration in seconds, got %+v", issues)
	}
}

// TestBuildSections checks that the form is described by the catalog.
func TestBuildSections(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, string(devConf(t)))
	sections := BuildSections(cat, Extract(f, cat))

	if len(sections) != len(catalog.KnownSections()) {
		t.Fatalf("sections = %d, want %d", len(sections), len(catalog.KnownSections()))
	}
	byID := map[string]Section{}
	for _, s := range sections {
		if s.Title == "" {
			t.Errorf("section %q has no title", s.ID)
		}
		if len(s.Fields) == 0 {
			t.Errorf("section %q has no fields", s.ID)
		}
		byID[s.ID] = s
	}
	persistence := byID[catalog.SectionPersistence]
	var found bool
	for _, field := range persistence.Fields {
		if field.Directive == "autosave_interval" {
			found = true
			if field.Unit != "seconds" {
				t.Errorf("autosave_interval unit = %q, want seconds", field.Unit)
			}
			if field.Min == nil || *field.Min != 0 {
				t.Errorf("autosave_interval min = %v, want 0", field.Min)
			}
			if field.Default != "1800" {
				t.Errorf("autosave_interval default = %q, want 1800", field.Default)
			}
			if field.ReloadKind != catalog.ReloadOK {
				t.Errorf("autosave_interval reload_kind = %q, want reload", field.ReloadKind)
			}
		}
	}
	if !found {
		t.Error("the persistence section has no autosave_interval field")
	}
	// A field the file sets is marked as present.
	for _, field := range byID[catalog.SectionPersistence].Fields {
		if field.Directive == "persistence" {
			if !field.Present || len(field.Values) != 1 || field.Values[0] != "true" {
				t.Errorf("persistence field = %+v, want present with true", field)
			}
		}
		if field.Directive == "autosave_interval" && field.Present {
			t.Error("autosave_interval must not be present in the shipped configuration")
		}
	}
}

// TestChangedDirectives checks the helper that tells a SIGHUP apart
// from a full restart.
func TestChangedDirectives(t *testing.T) {
	cat := testCatalog(t)
	before := parse(t, "persistence true\nmax_keepalive 60\n")
	after := parse(t, "persistence false\nmax_keepalive 60\n")
	changed := ChangedDirectives(before, after, cat)
	if len(changed) != 1 || changed[0] != "persistence" {
		t.Errorf("changed = %v, want [persistence]", changed)
	}
	if got := ChangedDirectives(before, before, cat); len(got) != 0 {
		t.Errorf("changed = %v, want none", got)
	}
}

// TestApply_DoesNotModifyInput makes sure the caller keeps its file.
func TestApply_DoesNotModifyInput(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, "persistence true\n")
	before := render(t, f)
	doc := Extract(f, cat)
	for i := range doc.Entries {
		if doc.Entries[i].Directive == "persistence" {
			doc.Entries[i].Values = []string{"false"}
		}
	}
	if _, _, err := Apply(f, doc, cat); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if after := render(t, f); after != before {
		t.Errorf("the input file was modified: %q", after)
	}
}

// TestExtract_IgnoresGlobalDirectiveAfterListener checks that a global
// directive written after a listener line is still read: Mosquitto keeps
// global options global wherever they appear.
func TestExtract_IgnoresGlobalDirectiveAfterListener(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, "listener 1883 0.0.0.0\npersistence true\n")
	doc := Extract(f, cat)
	values, ok := doc.Values("persistence")
	if !ok || len(values) != 1 || values[0] != "true" {
		t.Errorf("persistence = %v (present=%v), want [true]", values, ok)
	}
}

// TestApply_KeepsGlobalDirectiveAfterListener checks that rewriting such
// a directive leaves it where the operator put it.
func TestApply_KeepsGlobalDirectiveAfterListener(t *testing.T) {
	cat := testCatalog(t)
	original := "listener 1883 0.0.0.0\npersistence true\n"
	f := parse(t, original)
	doc := Extract(f, cat)
	for i := range doc.Entries {
		if doc.Entries[i].Directive == "persistence" {
			doc.Entries[i].Values = []string{"false"}
		}
	}
	out, _, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, out)
	want := "listener 1883 0.0.0.0\npersistence false\n"
	if got != want {
		t.Errorf("rendered:\n%q\nwant:\n%q", got, want)
	}
}

// TestApply_QuotesSurvive makes sure a value with spaces keeps its
// quoting when the directive is rewritten.
func TestApply_QuotesSurvive(t *testing.T) {
	cat := testCatalog(t)
	f := parse(t, "log_dest file \"/var/log/mos quitto.log\"\n")
	doc := Extract(f, cat)
	if values, ok := doc.Values("log_dest"); !ok || len(values) != 2 {
		t.Fatalf("log_dest = %v (present=%v), want two values", values, ok)
	}
	for i := range doc.Entries {
		if doc.Entries[i].Directive == "log_dest" {
			doc.Entries[i].Values = []string{"file", "/var/log/other.log"}
		}
	}
	out, _, err := Apply(f, doc, cat)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := render(t, out)
	if !strings.Contains(got, "log_dest file /var/log/other.log") {
		t.Errorf("rendered:\n%q", got)
	}
}
