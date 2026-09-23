package catalog

import (
	"errors"
	"testing"
)

func TestLoadVersion_2_0(t *testing.T) {
	cat, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion 2.0: %v", err)
	}
	if cat.Version != "2.0" {
		t.Errorf("Version = %q, want 2.0", cat.Version)
	}
	mustHave := []string{
		"listener", "port", "max_connections", "protocol",
		"allow_anonymous", "password_file", "acl_file",
		"persistence", "persistence_location", "log_dest",
		"log_type", "connection_messages", "user",
		"cafile", "capath", "certfile", "keyfile",
		"require_certificate", "tls_version",
	}
	for _, name := range mustHave {
		if _, ok := cat.Lookup(name); !ok {
			t.Errorf("catalog 2.0 missing %q", name)
		}
	}
}

func TestLoadVersion_2_1_Stub(t *testing.T) {
	cat, err := LoadVersion("2.1")
	if err != nil {
		t.Fatalf("LoadVersion 2.1: %v", err)
	}
	if cat.Version != "2.1" {
		t.Errorf("Version = %q, want 2.1", cat.Version)
	}
}

func TestLoadVersion_Missing(t *testing.T) {
	if _, err := LoadVersion("99.99"); err == nil {
		t.Fatal("expected an error for a missing catalog version")
	} else if !errors.Is(err, ErrUnknownDirective) {
		t.Errorf("missing-version err = %v, want ErrUnknownDirective", err)
	}
}

func TestLoadVersion_BadName(t *testing.T) {
	if _, err := LoadVersion("../etc/passwd"); err == nil {
		t.Fatal("expected an error for an invalid version name")
	}
}

func TestLoadAll(t *testing.T) {
	cats, err := LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if _, ok := cats["2.0"]; !ok {
		t.Error("LoadAll must include 2.0")
	}
	if _, ok := cats["2.1"]; !ok {
		t.Error("LoadAll must include 2.1")
	}
}

func TestCatalogSpecs_Sorted(t *testing.T) {
	cat, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion: %v", err)
	}
	specs := cat.Specs()
	for i := 1; i < len(specs); i++ {
		if specs[i-1].Name > specs[i].Name {
			t.Errorf("Specs not sorted: %q > %q at %d", specs[i-1].Name, specs[i].Name, i)
		}
	}
}