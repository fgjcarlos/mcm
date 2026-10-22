package catalog

import (
	"strings"
	"testing"
)

func TestCatalogSupportsListenerDirective(t *testing.T) {
	cat, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion() error = %v", err)
	}
	for _, directive := range []string{"cafile", "capath", "certfile", "keyfile", "require_certificate", "tls_version"} {
		if !cat.SupportsListenerDirective(directive) {
			t.Errorf("SupportsListenerDirective(%q) = false, want true", directive)
		}
	}
	for _, directive := range []string{"listener", "protocol", "max_connections", "allow_anonymous", "unknown"} {
		if cat.SupportsListenerDirective(directive) {
			t.Errorf("SupportsListenerDirective(%q) = true, want false", directive)
		}
	}
}

func TestCatalogSupportsListenerDirectiveFailsClosed(t *testing.T) {
	var nilCatalog *Catalog
	if nilCatalog.SupportsListenerDirective("certfile") {
		t.Error("SupportsListenerDirective() on a nil catalog = true, want false")
	}
}

func TestCatalogListenerSinceVersionIsValidated(t *testing.T) {
	spec := DirectiveSpec{
		Name:                 "certfile",
		Type:                 TypePath,
		Scope:                ScopeGlobal,
		Multiplicity:         Once,
		ReloadKind:           Restart,
		SinceVersion:         "1.0",
		ListenerSinceVersion: "not-a-version",
	}
	if err := spec.Validate(DefaultSentinels); err == nil {
		t.Fatal("Validate() error = nil, want an invalid listener_since_version error")
	} else if !strings.Contains(err.Error(), "listener_since_version") {
		t.Fatalf("Validate() error = %v, want a listener_since_version error", err)
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		version string
		since   string
		want    bool
	}{
		{"2.0", "2.0", true},
		{"2.1", "2.0", true},
		{"2.0", "2.1", false},
		{"1.6", "2.0", false},
		{"2", "2.0", true},
		{"2.0.1", "2.0", true},
		{"", "2.0", false},
		{"2.0", "", false},
	}
	for _, tc := range cases {
		if got := versionAtLeast(tc.version, tc.since); got != tc.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tc.version, tc.since, got, tc.want)
		}
	}
}
