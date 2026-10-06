package listeners

import (
	"reflect"
	"testing"
)

func TestValidate_OK(t *testing.T) {
	specs := []ListenerSpec{
		{ID: "mqtt-dev", Port: 1883, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolMQTT}},
		{ID: "websocket-dev", Port: 9001, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolWebSocket}},
		tlsProdSpec(),
	}

	issues, warnings := Validate(specs)
	if len(issues) != 0 {
		t.Errorf("issues = %#v, want none", issues)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %#v, want none", warnings)
	}
}

func TestValidate_Duplicate(t *testing.T) {
	specs := []ListenerSpec{
		{ID: "first", Port: 1883, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolMQTT}},
		{ID: "second", Port: 1883, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolMQTT}},
	}

	issues, _ := Validate(specs)
	if len(issues) != 1 || issues[0].Kind != "duplicate" {
		t.Errorf("issues = %#v, want one duplicate issue", issues)
	}
}

func TestValidate_BindInvalid(t *testing.T) {
	issues, _ := Validate([]ListenerSpec{{ID: "bad-bind", Port: 1883, Bind: "notanIP", Protocols: []Protocol{ProtocolMQTT}}})

	if len(issues) != 1 || issues[0].Kind != "bind" {
		t.Errorf("issues = %#v, want one bind issue", issues)
	}
}

func TestValidate_PortRange(t *testing.T) {
	specs := []ListenerSpec{
		{ID: "zero", Port: 0, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolMQTT}},
		{ID: "high", Port: 70000, Bind: "0.0.0.0", Protocols: []Protocol{ProtocolMQTT}},
	}

	issues, _ := Validate(specs)
	if len(issues) != 2 {
		t.Fatalf("issues = %#v, want two port_range issues", issues)
	}
	for _, issue := range issues {
		if issue.Kind != "port_range" {
			t.Errorf("issue.Kind = %q, want port_range", issue.Kind)
		}
	}
}

func TestValidate_ProtocolsRequired(t *testing.T) {
	issues, _ := Validate([]ListenerSpec{{ID: "no-protocol", Port: 1883, Bind: "0.0.0.0"}})

	if len(issues) != 1 || issues[0].Kind != "protocols_required" {
		t.Errorf("issues = %#v, want one protocols_required issue", issues)
	}
}

func TestValidate_ProtocolsUnknown(t *testing.T) {
	issues, _ := Validate([]ListenerSpec{{ID: "unknown-protocol", Port: 1883, Bind: "0.0.0.0", Protocols: []Protocol{"bogus"}}})

	if len(issues) != 1 || issues[0].Kind != "protocols_unknown" {
		t.Errorf("issues = %#v, want one protocols_unknown issue", issues)
	}
}

func TestValidate_OptionWarning(t *testing.T) {
	_, warnings := Validate([]ListenerSpec{{
		ID:        "unknown-option",
		Port:      1883,
		Bind:      "0.0.0.0",
		Protocols: []Protocol{ProtocolMQTT},
		Options:   map[string]string{"weird": ""},
	}})

	if len(warnings) != 1 {
		t.Fatalf("warnings = %#v, want one warning", warnings)
	}
	want := OptionWarning{Kind: "options_unknown_key", ListenerID: "unknown-option", Key: "weird", Message: "unknown option key \"weird\""}
	if warnings[0] != want {
		t.Errorf("warning = %#v, want %#v", warnings[0], want)
	}
}

func TestValidate_DeterministicOrder(t *testing.T) {
	specs := []ListenerSpec{
		{ID: "zeta", Port: 0, Bind: "not-an-ip", Protocols: []Protocol{"bogus"}, Options: map[string]string{"z": ""}},
		{ID: "alpha", Port: 0, Bind: "also-not-an-ip", Protocols: []Protocol{"bad"}, Options: map[string]string{"a": ""}},
	}

	issues, warnings := Validate(specs)
	wantIssues := []Issue{
		{Kind: "bind", ListenerID: "alpha", Message: "bind must be an IP address"},
		{Kind: "port_range", ListenerID: "alpha", Message: "port must be between 1 and 65535"},
		{Kind: "protocols_unknown", ListenerID: "alpha", Directive: "protocol", Message: "unknown protocol \"bad\""},
		{Kind: "bind", ListenerID: "zeta", Message: "bind must be an IP address"},
		{Kind: "port_range", ListenerID: "zeta", Message: "port must be between 1 and 65535"},
		{Kind: "protocols_unknown", ListenerID: "zeta", Directive: "protocol", Message: "unknown protocol \"bogus\""},
	}
	wantWarnings := []OptionWarning{
		{Kind: "options_unknown_key", ListenerID: "alpha", Key: "a", Message: "unknown option key \"a\""},
		{Kind: "options_unknown_key", ListenerID: "zeta", Key: "z", Message: "unknown option key \"z\""},
	}
	if !reflect.DeepEqual(issues, wantIssues) {
		t.Errorf("issues = %#v, want %#v", issues, wantIssues)
	}
	if !reflect.DeepEqual(warnings, wantWarnings) {
		t.Errorf("warnings = %#v, want %#v", warnings, wantWarnings)
	}
}
