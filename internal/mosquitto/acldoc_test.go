package mosquitto

import (
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// TestParseACLDocumentRoundTrip pins that the document model is lossless:
// parsing a body and rendering it again reproduces the original bytes.
func TestParseACLDocumentRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "plain rules", body: "user alice\ntopic read sensors/#\n"},
		{name: "comments and blank lines", body: "# header\n\nuser alice\n# note\ntopic read sensors/#\n\n"},
		{name: "crlf body", body: "user alice\r\ntopic read sensors/#\r\n"},
		{name: "no trailing newline", body: "user alice\ntopic read sensors/#"},
		{name: "trailing whitespace", body: "  user alice  \ntopic read sensors/#   \n"},
		{name: "only a newline", body: "\n"},
		{name: "unknown directive", body: "user alice\nacl_file /etc/mosquitto/extra.acl\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := ParseACLDocument(tc.body)
			if got := doc.Render(); got != tc.body {
				t.Fatalf("Render() = %q, want %q", got, tc.body)
			}
		})
	}
}

// TestParseACLDocumentEntryKinds pins the classification of every line of a
// document: comment, blank, user header, topic, pattern and unknown.
func TestParseACLDocumentEntryKinds(t *testing.T) {
	t.Parallel()

	body := "# comment\n\nuser alice\ntopic read sensors/#\npattern write logs/%u\nacl_file /etc/other.acl\n"
	doc := ParseACLDocument(body)

	want := []ACLEntryKind{
		ACLEntryComment,
		ACLEntryBlank,
		ACLEntryUser,
		ACLEntryTopic,
		ACLEntryPattern,
		ACLEntryUnknown,
	}
	if len(doc.Entries) != len(want) {
		t.Fatalf("entries = %d, want %d", len(doc.Entries), len(want))
	}
	for i, kind := range want {
		if doc.Entries[i].Kind != kind {
			t.Errorf("entry %d kind = %q, want %q", i, doc.Entries[i].Kind, kind)
		}
	}

	if doc.Entries[3].Raw != "topic read sensors/#" {
		t.Errorf("raw line = %q, want %q", doc.Entries[3].Raw, "topic read sensors/#")
	}
	if doc.Entries[5].Raw != "acl_file /etc/other.acl" {
		t.Errorf("raw line = %q, want %q", doc.Entries[5].Raw, "acl_file /etc/other.acl")
	}
	if !doc.EndsWithNewline {
		t.Error("a body ending in a newline must report EndsWithNewline")
	}
}

// TestParseACLDocumentScopes pins the scope each recognised entry carries:
// anonymous before the first user header, the user principal after it, and
// no principal at all for pattern entries.
func TestParseACLDocumentScopes(t *testing.T) {
	t.Parallel()

	body := "topic read public/#\nuser alice\ntopic read sensors/#\npattern write logs/%u\ntopic write other/#\n"
	doc := ParseACLDocument(body)

	anonymous := doc.Entries[0]
	if anonymous.Kind != ACLEntryTopic || anonymous.Principal != "" {
		t.Fatalf("entry 0 = %+v, want an anonymous topic entry", anonymous)
	}
	if anonymous.Rule == nil || anonymous.Rule.EffectiveSelector() != acl.SelectorAnonymous {
		t.Fatalf("entry 0 rule = %+v, want the anonymous selector", anonymous.Rule)
	}

	scoped := doc.Entries[2]
	if scoped.Kind != ACLEntryTopic || scoped.Principal != "alice" {
		t.Fatalf("entry 2 = %+v, want a topic entry scoped to alice", scoped)
	}
	if scoped.Rule == nil || scoped.Rule.EffectiveSelector() != acl.SelectorUser {
		t.Fatalf("entry 2 rule = %+v, want the user selector", scoped.Rule)
	}

	pattern := doc.Entries[3]
	if pattern.Kind != ACLEntryPattern || pattern.Principal != "" {
		t.Fatalf("entry 3 = %+v, want a pattern entry without principal", pattern)
	}
	if pattern.Rule == nil || pattern.Rule.EffectiveSelector() != acl.SelectorPattern {
		t.Fatalf("entry 3 rule = %+v, want the pattern selector", pattern.Rule)
	}

	// A pattern line does not open a scope: the next topic line still
	// belongs to the last user section.
	afterPattern := doc.Entries[4]
	if afterPattern.Principal != "alice" {
		t.Errorf("entry 4 principal = %q, want alice", afterPattern.Principal)
	}
}

// TestParseACLDocumentTypedRules pins the typed rules MCM can model and the
// principals the document declares.
func TestParseACLDocumentTypedRules(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("topic read public/#\nuser alice\ntopic read sensors/#\npattern write logs/%u\nacl_file /etc/other.acl\n")
	rules := doc.TypedRules()
	if len(rules) != 3 {
		t.Fatalf("typed rules = %d, want 3", len(rules))
	}
	if rules[0].EffectiveSelector() != acl.SelectorAnonymous || rules[0].TopicFilter != "public/#" {
		t.Errorf("rule 0 = %+v, want the anonymous public/# rule", rules[0])
	}
	if rules[1].EffectiveSelector() != acl.SelectorUser || rules[1].Principal != "alice" {
		t.Errorf("rule 1 = %+v, want the alice user rule", rules[1])
	}
	if rules[2].EffectiveSelector() != acl.SelectorPattern || rules[2].Permission != acl.PermissionWrite {
		t.Errorf("rule 2 = %+v, want the pattern write rule", rules[2])
	}

	principals := doc.Principals()
	if len(principals) != 1 || principals[0] != "alice" {
		t.Errorf("principals = %v, want [alice]", principals)
	}
}

// TestParseACLDocumentUnknownEntry pins that an unrecognised directive keeps
// its verbatim bytes and explains why it could not be interpreted.
func TestParseACLDocumentUnknownEntry(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("acl_file /etc/mosquitto/extra.acl\n")
	unknown := doc.UnknownEntries()
	if len(unknown) != 1 {
		t.Fatalf("unknown entries = %d, want 1", len(unknown))
	}
	if unknown[0].Raw != "acl_file /etc/mosquitto/extra.acl" {
		t.Errorf("raw = %q, want the verbatim directive", unknown[0].Raw)
	}
	if unknown[0].Issue == "" {
		t.Error("an unknown entry must explain the issue")
	}
	if unknown[0].Rule != nil {
		t.Error("an unknown entry must not carry a typed rule")
	}
}

// TestACLDocumentSemanticKeys pins the canonical comparable key of every
// semantic entry. Comments and blank lines carry no key.
func TestACLDocumentSemanticKeys(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("# comment\n\ntopic read public/#\nuser alice\ntopic read sensors/#\npattern write logs/%u\nacl_file /etc/other.acl\n")
	want := []string{
		"anonymous | topic read public/#",
		"pattern | write logs/%u",
		"unknown | acl_file /etc/other.acl",
		"user alice | topic read sensors/#",
	}
	got := doc.SemanticKeys()
	if len(got) != len(want) {
		t.Fatalf("semantic keys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("semantic key %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestComposeACLDocumentJoinsExistingUserSection pins that a managed rule for
// a principal the source already declares joins that section.
func TestComposeACLDocumentJoinsExistingUserSection(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("user alice\ntopic read sensors/#\n")
	composed, warnings := ComposeACLDocument(doc, []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionReadWrite},
	})
	want := "user alice\ntopic read sensors/#\ntopic readwrite sensors/#\n"
	if got := composed.Render(); got != want {
		t.Fatalf("composed = %q, want %q", got, want)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one insertion warning", warnings)
	}
	if !strings.Contains(warnings[0], "user alice | topic readwrite sensors/#") {
		t.Errorf("warning %q must name the inserted rule", warnings[0])
	}
}

// TestComposeACLDocumentOpensNewUserSection pins that a managed rule for an
// undeclared principal appends a new user section.
func TestComposeACLDocumentOpensNewUserSection(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("# source\nuser alice\ntopic read sensors/#\n")
	composed, warnings := ComposeACLDocument(doc, []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "bob", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
	})
	want := "# source\nuser alice\ntopic read sensors/#\nuser bob\ntopic read sensors/#\n"
	if got := composed.Render(); got != want {
		t.Fatalf("composed = %q, want %q", got, want)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one insertion warning", warnings)
	}
}

// TestComposeACLDocumentAnonymousBeforeFirstUser pins that an anonymous
// managed rule lands before the first user line.
func TestComposeACLDocumentAnonymousBeforeFirstUser(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("user alice\ntopic read sensors/#\n")
	composed, _ := ComposeACLDocument(doc, []acl.Rule{
		{Selector: acl.SelectorAnonymous, TopicFilter: "public/#", Permission: acl.PermissionRead},
	})
	rendered := composed.Render()
	anonymousIdx := strings.Index(rendered, "topic read public/#")
	userIdx := strings.Index(rendered, "user alice")
	if anonymousIdx < 0 || userIdx < 0 || anonymousIdx > userIdx {
		t.Fatalf("the anonymous rule must precede the first user line:\n%s", rendered)
	}
}

// TestComposeACLDocumentPatternBeforeFirstUser pins that a pattern managed
// rule lands before the first user line.
func TestComposeACLDocumentPatternBeforeFirstUser(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("user alice\ntopic read sensors/#\n")
	composed, _ := ComposeACLDocument(doc, []acl.Rule{
		{Selector: acl.SelectorPattern, TopicFilter: "logs/%u", Permission: acl.PermissionWrite},
	})
	rendered := composed.Render()
	patternIdx := strings.Index(rendered, "pattern write logs/%u")
	userIdx := strings.Index(rendered, "user alice")
	if patternIdx < 0 || userIdx < 0 || patternIdx > userIdx {
		t.Fatalf("the pattern rule must precede the first user line:\n%s", rendered)
	}
}

// TestComposeACLDocumentSkipsDuplicateManagedRule pins that a managed rule
// the source already carries is not inserted a second time and is reported.
func TestComposeACLDocumentSkipsDuplicateManagedRule(t *testing.T) {
	t.Parallel()

	doc := ParseACLDocument("user alice\ntopic read sensors/#\n")
	composed, warnings := ComposeACLDocument(doc, []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "alice", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
	})
	want := "user alice\ntopic read sensors/#\n"
	if got := composed.Render(); got != want {
		t.Fatalf("composed = %q, want %q", got, want)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want one duplicate warning", warnings)
	}
	if !strings.Contains(warnings[0], "user alice | topic read sensors/#") {
		t.Errorf("warning %q must name the skipped duplicate", warnings[0])
	}
}

// TestComposeACLDocumentPreservesUnknownAndComments pins that the foreign
// content of an imported source survives the composition.
func TestComposeACLDocumentPreservesUnknownAndComments(t *testing.T) {
	t.Parallel()

	source := "# header\nacl_file /etc/other.acl\n\nuser alice\ntopic read sensors/#\n"
	doc := ParseACLDocument(source)
	composed, _ := ComposeACLDocument(doc, []acl.Rule{
		{Selector: acl.SelectorUser, Principal: "bob", TopicFilter: "sensors/#", Permission: acl.PermissionRead},
	})
	rendered := composed.Render()
	for _, line := range strings.Split(strings.TrimSuffix(source, "\n"), "\n") {
		if line == "" {
			continue
		}
		if !strings.Contains(rendered, line) {
			t.Errorf("the composed document dropped the original line %q:\n%s", line, rendered)
		}
	}
}

// TestMissingSemanticEntries pins the semantic keys the next document drops.
func TestMissingSemanticEntries(t *testing.T) {
	t.Parallel()

	base := ParseACLDocument("topic read public/#\nuser alice\ntopic read sensors/#\npattern write logs/%u\nacl_file /etc/other.acl\n")
	next := ParseACLDocument("user alice\ntopic read sensors/#\n")
	want := []string{
		"anonymous | topic read public/#",
		"pattern | write logs/%u",
		"unknown | acl_file /etc/other.acl",
	}
	got := MissingSemanticEntries(base, next)
	if len(got) != len(want) {
		t.Fatalf("missing entries = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("missing entry %d = %q, want %q", i, got[i], want[i])
		}
	}
}
