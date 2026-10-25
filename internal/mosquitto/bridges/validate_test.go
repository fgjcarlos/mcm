package bridges

import (
	"strings"
	"testing"
)

func issueKinds(issues []Issue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Kind+":"+issue.Bridge+":"+issue.Field)
	}
	return out
}

func hasIssue(issues []Issue, kind, bridge, field string) bool {
	for _, issue := range issues {
		if issue.Kind == kind && issue.Bridge == bridge && (field == "" || issue.Field == field) {
			return true
		}
	}
	return false
}

func validBridge() Bridge {
	return Bridge{
		Name:    "upstream",
		Address: "broker.example.org:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}
}

func TestValidateAcceptsAWellFormedBridge(t *testing.T) {
	issues := Validate(Document{Bridges: []Bridge{validBridge()}}, nil)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issueKinds(issues))
	}
}

func TestValidateRejectsBadNames(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"spaces", "up stream"},
		{"leading dash", "-upstream"},
		{"too long", strings.Repeat("a", 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridge := validBridge()
			bridge.Name = tc.input
			issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
			if !hasIssue(issues, IssueKindName, tc.input, "name") {
				t.Fatalf("issues = %v, want a name issue", issueKinds(issues))
			}
		})
	}
}

func TestValidateRejectsDuplicateNames(t *testing.T) {
	first := validBridge()
	second := validBridge()
	issues := Validate(Document{Bridges: []Bridge{first, second}}, nil)
	if !hasIssue(issues, IssueKindDuplicate, "upstream", "name") {
		t.Fatalf("issues = %v, want a duplicate name issue", issueKinds(issues))
	}
}

func TestValidateRejectsBadAddresses(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"scheme", "mqtt://broker:1883"},
		{"path", "broker:1883/path"},
		{"port out of range", "broker:70000"},
		{"port zero", "broker:0"},
		{"not a number", "broker:abc"},
		{"spaces", "bro ker:1883"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridge := validBridge()
			bridge.Address = tc.input
			issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
			if !hasIssue(issues, IssueKindAddress, "upstream", "address") {
				t.Fatalf("issues = %v, want an address issue", issueKinds(issues))
			}
		})
	}
}

func TestValidateRequiresTopics(t *testing.T) {
	bridge := validBridge()
	bridge.Topics = nil
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindDependency, "upstream", "topics") {
		t.Fatalf("issues = %v, want a topics dependency issue", issueKinds(issues))
	}
}

func TestValidateRejectsDuplicateTopicRules(t *testing.T) {
	bridge := validBridge()
	bridge.Topics = []TopicRule{
		{Topic: "both/#", Direction: DirectionBoth, QoS: 1},
		{Topic: "both/#", Direction: DirectionBoth, QoS: 0},
	}
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindDuplicate, "upstream", "topics") {
		t.Fatalf("issues = %v, want a duplicate topic issue", issueKinds(issues))
	}
}

func TestValidateRejectsBadTopicRules(t *testing.T) {
	cases := []struct {
		name  string
		rule  TopicRule
		field string
	}{
		{"empty pattern", TopicRule{Topic: "", Direction: DirectionBoth}, "topics"},
		{"bad wildcard", TopicRule{Topic: "both/#/more", Direction: DirectionBoth}, "topics"},
		{"partial wildcard", TopicRule{Topic: "both/sen+", Direction: DirectionBoth}, "topics"},
		{"bad direction", TopicRule{Topic: "both/#", Direction: "sideways"}, "topics"},
		{"bad qos", TopicRule{Topic: "both/#", Direction: DirectionBoth, QoS: 3}, "topics"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bridge := validBridge()
			bridge.Topics = []TopicRule{tc.rule}
			issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
			if !hasIssue(issues, IssueKindValue, "upstream", tc.field) {
				t.Fatalf("issues = %v, want a value issue", issueKinds(issues))
			}
		})
	}
}

func TestValidateChecksTLSDependencies(t *testing.T) {
	bridge := validBridge()
	bridge.TLS = TLSOptions{CertFile: "/client.pem"}
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindDependency, "upstream", "tls.key_file") {
		t.Fatalf("issues = %v, want a key file dependency issue", issueKinds(issues))
	}

	bridge = validBridge()
	bridge.TLS = TLSOptions{KeyFile: "/client.key"}
	issues = Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindDependency, "upstream", "tls.cert_file") {
		t.Fatalf("issues = %v, want a certificate dependency issue", issueKinds(issues))
	}

	bridge = validBridge()
	bridge.TLS = TLSOptions{PSK: "secret"}
	issues = Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindDependency, "upstream", "tls.identity") {
		t.Fatalf("issues = %v, want a PSK identity dependency issue", issueKinds(issues))
	}
}

func TestValidateChecksNotificationDependency(t *testing.T) {
	bridge := validBridge()
	disabled := false
	bridge.Notifications = &disabled
	bridge.NotificationTopic = "$SYS/broker/connection/upstream/state"
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindDependency, "upstream", "notification_topic") {
		t.Fatalf("issues = %v, want a notification dependency issue", issueKinds(issues))
	}
}

func TestValidateWarnsAboutIncompleteCredentials(t *testing.T) {
	bridge := validBridge()
	bridge.Username = "mcm"
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindSecret, "upstream", "password") {
		t.Fatalf("issues = %v, want a credential warning", issueKinds(issues))
	}
}

func TestValidateRejectsInsecureTLSWithoutCA(t *testing.T) {
	bridge := validBridge()
	bridge.TLS = TLSOptions{Insecure: boolPtr(true)}
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindSecret, "upstream", "tls.insecure") {
		t.Fatalf("issues = %v, want an insecure TLS warning", issueKinds(issues))
	}
}

func TestValidateDetectsSelfConnection(t *testing.T) {
	bridge := validBridge()
	bridge.Address = "127.0.0.1:1883"
	issues := Validate(Document{Bridges: []Bridge{bridge}}, []string{"0.0.0.0:1883"})
	// The listener is bound to 0.0.0.0, so the loopback address reaches
	// the same broker.
	found := false
	for _, issue := range issues {
		if issue.Kind == IssueKindLoop && issue.Bridge == "upstream" {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %v, want a loop issue", issueKinds(issues))
	}
}

func TestValidateDetectsLoopBetweenTwoRulesOfTheSameBridge(t *testing.T) {
	bridge := validBridge()
	bridge.Topics = []TopicRule{
		{Topic: "data/#", Direction: DirectionOut, QoS: 1},
		{Topic: "data/#", Direction: DirectionIn, QoS: 1},
	}
	issues := Validate(Document{Bridges: []Bridge{bridge}}, nil)
	if !hasIssue(issues, IssueKindLoop, "upstream", "topics") {
		t.Fatalf("issues = %v, want a loop issue", issueKinds(issues))
	}
}

func TestValidateDetectsLoopBetweenTwoBridgesOfTheSameRemote(t *testing.T) {
	first := Bridge{
		Name:    "first",
		Address: "remote.example.org:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}
	second := Bridge{
		Name:    "second",
		Address: "remote.example.org:1883",
		Topics:  []TopicRule{{Topic: "both/+/x", Direction: DirectionBoth, QoS: 1}},
	}
	issues := Validate(Document{Bridges: []Bridge{first, second}}, nil)
	if !hasIssue(issues, IssueKindLoop, "second", "address") {
		t.Fatalf("issues = %v, want a loop issue", issueKinds(issues))
	}
}

func TestValidateAcceptsTwoBridgesOfDifferentRemotes(t *testing.T) {
	first := Bridge{
		Name:    "first",
		Address: "one.example.org:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}
	second := Bridge{
		Name:    "second",
		Address: "two.example.org:1883",
		Topics:  []TopicRule{{Topic: "both/#", Direction: DirectionBoth, QoS: 1}},
	}
	issues := Validate(Document{Bridges: []Bridge{first, second}}, nil)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issueKinds(issues))
	}
}

func TestValidateAcceptsOneWayBridgesOnTheSameRemote(t *testing.T) {
	first := Bridge{
		Name:    "first",
		Address: "remote.example.org:1883",
		Topics:  []TopicRule{{Topic: "out/#", Direction: DirectionOut, QoS: 1}},
	}
	second := Bridge{
		Name:    "second",
		Address: "remote.example.org:1883",
		Topics:  []TopicRule{{Topic: "out/+/x", Direction: DirectionOut, QoS: 1}},
	}
	issues := Validate(Document{Bridges: []Bridge{first, second}}, nil)
	if len(issues) != 0 {
		t.Fatalf("issues = %v, want none", issueKinds(issues))
	}
}

func TestTopicsOverlap(t *testing.T) {
	cases := []struct {
		a    string
		b    string
		want bool
	}{
		{"a/b", "a/b", true},
		{"a/b", "a/c", false},
		{"a/#", "a/b", true},
		{"a/b", "a/#", true},
		{"a/#", "a/b/c", true},
		{"a/+/c", "a/b/c", true},
		{"a/+/c", "a/b/d", false},
		{"#", "a/b", true},
		{"a/b", "#", true},
		{"a/b/c", "a/b", false},
		{"a/b", "a/b/c", false},
		{"a/b/#", "a/b", true},
	}
	for _, tc := range cases {
		if got := TopicsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("TopicsOverlap(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSplitAddress(t *testing.T) {
	cases := []struct {
		input string
		host  string
		port  int
		err   bool
	}{
		{input: "broker", host: "broker", port: 1883},
		{input: "broker:1884", host: "broker", port: 1884},
		{input: "127.0.0.1:1883", host: "127.0.0.1", port: 1883},
		{input: "[::1]:1883", host: "::1", port: 1883},
		{input: "::1", host: "::1", port: 1883},
		{input: "broker:0", err: true},
		{input: "broker:70000", err: true},
		{input: "mqtt://broker", err: true},
		{input: "", err: true},
	}
	for _, tc := range cases {
		host, port, err := SplitAddress(tc.input)
		if tc.err {
			if err == nil {
				t.Errorf("SplitAddress(%q) = %q, %d, want an error", tc.input, host, port)
			}
			continue
		}
		if err != nil {
			t.Errorf("SplitAddress(%q): %v", tc.input, err)
			continue
		}
		if host != tc.host || port != tc.port {
			t.Errorf("SplitAddress(%q) = %q, %d, want %q, %d", tc.input, host, port, tc.host, tc.port)
		}
	}
}

func TestRedactedClearsSecrets(t *testing.T) {
	bridge := Bridge{
		Name:          "upstream",
		Address:       "broker:1883",
		Password:      "secret",
		LocalPassword: "local-secret",
		TLS:           TLSOptions{PSK: "psk-secret"},
	}
	redacted := bridge.Redacted()
	if redacted.Password != "" || redacted.LocalPassword != "" || redacted.TLS.PSK != "" {
		t.Fatalf("the redacted bridge still carries a secret: %+v", redacted)
	}
	if !redacted.PasswordSet || !redacted.LocalPasswordSet || !redacted.TLS.PSKSet {
		t.Fatalf("the redacted bridge lost its set flags: %+v", redacted)
	}
}

func TestDocumentCloneIsDeep(t *testing.T) {
	doc := Document{Bridges: []Bridge{{
		Name:   "upstream",
		Topics: []TopicRule{{Topic: "both/#"}},
	}}}
	clone := doc.Clone()
	clone.Bridges[0].Topics[0].Topic = "changed"
	if doc.Bridges[0].Topics[0].Topic != "both/#" {
		t.Errorf("the clone shares its topics with the original")
	}
}
