package listeners

import "testing"

func TestValidate_TLSOptionsComplete(t *testing.T) {
	issues, warnings := Validate([]ListenerSpec{tlsProdSpec()})
	if len(issues) != 0 {
		t.Errorf("issues = %#v, want none", issues)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %#v, want none", warnings)
	}
}

func TestValidate_TLSCertFileWithoutKeyFile(t *testing.T) {
	spec := tlsProdSpec()
	delete(spec.Options, "keyfile")

	issues, _ := Validate([]ListenerSpec{spec})
	if len(issues) != 1 || issues[0].Kind != "tls_options" {
		t.Fatalf("issues = %#v, want one tls_options issue", issues)
	}
	if issues[0].Directive != "certfile" {
		t.Errorf("issue directive = %q, want certfile", issues[0].Directive)
	}
}

func TestValidate_TLSKeyFileWithoutCertFile(t *testing.T) {
	spec := tlsProdSpec()
	delete(spec.Options, "certfile")

	issues, _ := Validate([]ListenerSpec{spec})
	if len(issues) != 1 || issues[0].Kind != "tls_options" {
		t.Fatalf("issues = %#v, want one tls_options issue", issues)
	}
}

func TestValidate_TLSRequireCertificateWithoutCAFile(t *testing.T) {
	spec := tlsProdSpec()
	delete(spec.Options, "cafile")
	spec.Options["require_certificate"] = "true"

	issues, _ := Validate([]ListenerSpec{spec})
	if len(issues) != 1 || issues[0].Kind != "tls_options" {
		t.Fatalf("issues = %#v, want one tls_options issue", issues)
	}
	if issues[0].Directive != "require_certificate" {
		t.Errorf("issue directive = %q, want require_certificate", issues[0].Directive)
	}
}

func TestValidate_TLSRequireCertificateWithCAFile(t *testing.T) {
	spec := tlsProdSpec()
	spec.Options["require_certificate"] = "true"

	issues, warnings := Validate([]ListenerSpec{spec})
	if len(issues) != 0 {
		t.Errorf("issues = %#v, want none", issues)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %#v, want none", warnings)
	}
}

func TestValidate_RequireCertificateIsAKnownOption(t *testing.T) {
	spec := ListenerSpec{
		ID:        "tls",
		Port:      8883,
		Bind:      "0.0.0.0",
		Protocols: []Protocol{ProtocolMQTT},
		Options:   map[string]string{"require_certificate": "false"},
	}

	_, warnings := Validate([]ListenerSpec{spec})
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
}
