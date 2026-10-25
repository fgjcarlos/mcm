package catalog

import (
	"errors"
	"slices"
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

// TestLoadVersion_2_0_SettingsSections pins the directives the graphical
// settings form offers (issue #302). Every section must be covered by at
// least one field and the bounds must be filled in.
func TestLoadVersion_2_0_SettingsSections(t *testing.T) {
	cat, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion 2.0: %v", err)
	}
	want := map[string][]string{
		SectionPersistence: {"persistence", "persistence_file", "persistence_location", "autosave_interval", "autosave_on_changes"},
		SectionQueues:      {"max_queued_messages", "max_inflight_messages", "max_queued_bytes", "max_inflight_bytes", "queue_qos0_messages", "max_packet_size"},
		SectionSessions:    {"max_connections", "max_keepalive", "persistent_client_expiration"},
		SectionLogging:     {"log_dest", "log_type", "log_timestamp", "log_timestamp_format", "connection_messages", "log_facility"},
	}
	for section, names := range want {
		for _, name := range names {
			spec, ok := cat.Lookup(name)
			if !ok {
				t.Errorf("catalog 2.0 missing %q", name)
				continue
			}
			if spec.Section != section {
				t.Errorf("%q section = %q, want %q", name, spec.Section, section)
			}
			if spec.Description == "" {
				t.Errorf("%q has no description for the form help text", name)
			}
		}
	}
	// Every section must actually be reachable.
	seen := map[string]int{}
	for _, spec := range cat.Specs() {
		if spec.Section != "" {
			seen[spec.Section]++
		}
	}
	for _, section := range KnownSections() {
		if seen[section] == 0 {
			t.Errorf("no catalog entry uses section %q", section)
		}
	}
}

// TestLoadVersion_2_0_SettingsBounds pins the units and limits the form
// shows. The numbers mirror what Mosquitto 2.0 itself accepts.
func TestLoadVersion_2_0_SettingsBounds(t *testing.T) {
	cat, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion 2.0: %v", err)
	}
	cases := []struct {
		directive string
		unit      string
		min       int64
		max       int64
		hasMin    bool
		hasMax    bool
	}{
		{directive: "autosave_interval", unit: "seconds", min: 0, max: 2147483647, hasMin: true, hasMax: true},
		{directive: "max_queued_messages", unit: "messages", min: 0, max: 2147483647, hasMin: true, hasMax: true},
		{directive: "max_inflight_messages", unit: "messages", min: 0, max: 65535, hasMin: true, hasMax: true},
		{directive: "max_queued_bytes", unit: "bytes", min: 0, hasMin: true},
		{directive: "max_inflight_bytes", unit: "bytes", min: 0, hasMin: true},
		{directive: "max_packet_size", unit: "bytes", min: 20, max: 4294967295, hasMin: true, hasMax: true},
		{directive: "max_keepalive", unit: "seconds", min: 0, max: 65535, hasMin: true, hasMax: true},
		{directive: "max_connections", unit: "connections", min: -1, max: 2147483647, hasMin: true, hasMax: true},
		{directive: "log_facility", unit: "facility", min: 0, max: 7, hasMin: true, hasMax: true},
	}
	for _, tc := range cases {
		spec, ok := cat.Lookup(tc.directive)
		if !ok {
			t.Errorf("catalog 2.0 missing %q", tc.directive)
			continue
		}
		if spec.Type != TypeInteger {
			t.Errorf("%q type = %q, want integer", tc.directive, spec.Type)
		}
		if spec.Unit != tc.unit {
			t.Errorf("%q unit = %q, want %q", tc.directive, spec.Unit, tc.unit)
		}
		if tc.hasMin {
			if spec.Min == nil {
				t.Errorf("%q has no min bound", tc.directive)
			} else if *spec.Min != tc.min {
				t.Errorf("%q min = %d, want %d", tc.directive, *spec.Min, tc.min)
			}
		}
		if tc.hasMax {
			if spec.Max == nil {
				t.Errorf("%q has no max bound", tc.directive)
			} else if *spec.Max != tc.max {
				t.Errorf("%q max = %d, want %d", tc.directive, *spec.Max, tc.max)
			}
		}
	}
}

// TestLoadVersion_2_0_ReloadKinds pins which directives need a restart.
// Mosquitto skips these when it re-reads its configuration on SIGHUP.
func TestLoadVersion_2_0_ReloadKinds(t *testing.T) {
	cat, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion 2.0: %v", err)
	}
	restart := []string{"listener", "port", "max_connections", "user", "certfile", "keyfile", "cafile", "capath", "require_certificate", "tls_version"}
	for _, name := range restart {
		spec, ok := cat.Lookup(name)
		if !ok {
			t.Errorf("catalog 2.0 missing %q", name)
			continue
		}
		if spec.ReloadKind != Restart {
			t.Errorf("%q reload_kind = %q, want restart", name, spec.ReloadKind)
		}
	}
	reload := []string{
		"persistence", "persistence_file", "persistence_location",
		"autosave_interval", "autosave_on_changes",
		"max_queued_messages", "max_inflight_messages", "max_queued_bytes",
		"max_inflight_bytes", "queue_qos0_messages", "max_packet_size",
		"max_keepalive", "persistent_client_expiration",
		"log_dest", "log_type", "log_timestamp", "log_timestamp_format",
		"connection_messages", "log_facility",
	}
	for _, name := range reload {
		spec, ok := cat.Lookup(name)
		if !ok {
			t.Errorf("catalog 2.0 missing %q", name)
			continue
		}
		if spec.ReloadKind != ReloadOK {
			t.Errorf("%q reload_kind = %q, want reload", name, spec.ReloadKind)
		}
	}
}

// TestLoadVersion_2_1_Inherits checks that 2.1 keeps the 2.0 directive
// set and only adds what 2.1 introduced.
func TestLoadVersion_2_1_Inherits(t *testing.T) {
	base, err := LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion 2.0: %v", err)
	}
	cat, err := LoadVersion("2.1")
	if err != nil {
		t.Fatalf("LoadVersion 2.1: %v", err)
	}
	for _, name := range base.Names() {
		if _, ok := cat.Lookup(name); !ok {
			t.Errorf("catalog 2.1 lost inherited directive %q", name)
		}
	}
	for _, name := range []string{"global_max_clients", "global_max_connections"} {
		spec, ok := cat.Lookup(name)
		if !ok {
			t.Errorf("catalog 2.1 missing %q", name)
			continue
		}
		if spec.SinceVersion != "2.1" {
			t.Errorf("%q since_version = %q, want 2.1", name, spec.SinceVersion)
		}
		if spec.Section != SectionSessions {
			t.Errorf("%q section = %q, want %q", name, spec.Section, SectionSessions)
		}
	}
}

// TestLoadVersion_2_1_Overrides checks that a version can refine an
// inherited directive instead of restating the whole entry.
func TestLoadVersion_2_1_Overrides(t *testing.T) {
	cat, err := LoadVersion("2.1")
	if err != nil {
		t.Fatalf("LoadVersion 2.1: %v", err)
	}
	spec, ok := cat.Lookup("max_packet_size")
	if !ok {
		t.Fatal("catalog 2.1 missing max_packet_size")
	}
	if spec.Default != "2000000" {
		t.Errorf("max_packet_size default = %q, want 2000000", spec.Default)
	}
	if spec.Min == nil || *spec.Min != 20 {
		t.Errorf("max_packet_size min = %v, want 20", spec.Min)
	}
	// 2.1 accepts the seconds suffix, 2.0 does not.
	dur, _ := cat.Lookup("persistent_client_expiration")
	if !slices.Contains(dur.DurationSuffixes, "s") {
		t.Errorf("2.1 persistent_client_expiration suffixes = %v, want an s suffix", dur.DurationSuffixes)
	}
	base, _ := LoadVersion("2.0")
	baseDur, _ := base.Lookup("persistent_client_expiration")
	if slices.Contains(baseDur.DurationSuffixes, "s") {
		t.Errorf("2.0 persistent_client_expiration must not accept an s suffix, got %v", baseDur.DurationSuffixes)
	}
}

// TestDirectiveSpec_Validate covers the new cross-field rules.
func TestDirectiveSpec_Validate(t *testing.T) {
	base := DirectiveSpec{
		Name: "sample", Type: TypeInteger, Scope: ScopeGlobal,
		Multiplicity: Once, ReloadKind: ReloadOK, SinceVersion: "1.0",
	}
	cases := []struct {
		name    string
		mutate  func(*DirectiveSpec)
		wantErr bool
	}{
		{name: "plain integer", mutate: func(*DirectiveSpec) {}},
		{name: "integer with bounds", mutate: func(s *DirectiveSpec) {
			s.Min = ptr(0)
			s.Max = ptr(10)
		}},
		{name: "min above max", mutate: func(s *DirectiveSpec) {
			s.Min = ptr(10)
			s.Max = ptr(0)
		}, wantErr: true},
		{name: "bounds on a string", mutate: func(s *DirectiveSpec) {
			s.Type = TypeString
			s.Min = ptr(0)
		}, wantErr: true},
		{name: "unknown section", mutate: func(s *DirectiveSpec) {
			s.Section = "nope"
		}, wantErr: true},
		{name: "known section", mutate: func(s *DirectiveSpec) {
			s.Section = SectionLogging
		}},
		{name: "duration without suffixes", mutate: func(s *DirectiveSpec) {
			s.Type = TypeDuration
		}, wantErr: true},
		{name: "duration with suffixes", mutate: func(s *DirectiveSpec) {
			s.Type = TypeDuration
			s.DurationSuffixes = []string{"h"}
		}},
		{name: "suffixes on an integer", mutate: func(s *DirectiveSpec) {
			s.DurationSuffixes = []string{"h"}
		}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := base
			tc.mutate(&spec)
			err := spec.Validate(DefaultSentinels)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func ptr(v int64) *int64 { return &v }
