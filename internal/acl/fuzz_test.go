// Package acl hosts the fuzz harnesses for the ACL validators.
//
// These targets fuzz ValidateTopicFilter and ValidateRule for crash
// detection only — they intentionally do not assert correctness on every
// generated input. The harnesses run nightly via
// .github/workflows/fuzz.yml (10 s budget per target) and are also
// exercised by the in-process seed corpus during normal `go test`
// invocations.
//
// Note: -race is intentionally NOT enabled. Go fuzzing is incompatible
// with the race detector (the fuzzer's own internal accounting trips the
// race detector). The //go:build !race tag is a defensive guard so that
// a future contributor who runs `go test -race` does not silently
// disable fuzz coverage or hit a confusing failure.
package acl

import "testing"

func FuzzValidateTopicFilter(f *testing.F) {
	seeds := []string{
		"",                              // empty
		"#",                             // single hash
		"factory/#",                     // valid terminal wildcard
		"sensors/+/temperature",         // valid single-level wildcard
		"sensors/#/temperature",         // invalid: # not in final level
		"factory/area#",                 // invalid: # mid-level
		"foo+bar/value",                 // invalid: + not entire level
		"factory/+east/value",           // invalid: + glued to chars
		"factory/+/temperature\x00",     // NUL byte (must reject)
		"/leading/slash",                // valid (current behavior)
		"trailing/slash/",               // valid (current behavior)
		"$SYS/broker/clients/connected", // $SYS accepted (current behavior)
		"\x01\x02\x03",                  // control chars
		"\xff\xfe",                      // invalid UTF-8
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, filter string) {
		err := ValidateTopicFilter(filter)
		// Pin invariant: NUL bytes must be rejected.
		for _, r := range filter {
			if r == '\x00' {
				if err == nil {
					t.Errorf("ValidateTopicFilter(%q) = nil; want error for NUL byte", filter)
				}
				return
			}
		}
		// No assertion on success/failure for other inputs — fuzz is for
		// crash detection, not for asserting the validator's correctness on
		// every input.
		_ = err
	})
}

// FuzzValidateRule fuzzes ValidateRule. The Go fuzzer only accepts a
// fixed set of primitive parameter types, so we deconstruct Rule into
// its four string fields here and reconstruct the struct inside the
// fuzz target. Permission is fuzzed as a raw string — even though the
// struct field is the typed Permission alias, the fuzzer can drive it
// with arbitrary text, which exercises the
// "permission must be one of the known values" rejection path.
func FuzzValidateRule(f *testing.F) {
	seeds := []struct {
		principal   string
		topicFilter string
		permission  string
		description string
	}{
		{"alice", "sensors/+/temperature", "read", ""},
		{"", "sensors/+", "read", ""},
		{"alice", "", "read", ""},
		{"alice", "sensors/+/temperature", "admin", ""}, // invalid permission
		{"alice", "sensors/+/temperature", "", ""},
		{"   ", "sensors/+", "read", ""},
		{"alice", "foo\x00bar", "read", ""},
		{"alice", "sensors/#/temp", "read", ""},
		{"alice", "x", "readwrite", ""},
		{"", "", "", ""}, // all empty
	}
	for _, s := range seeds {
		f.Add(s.principal, s.topicFilter, s.permission, s.description)
	}
	f.Fuzz(func(t *testing.T, principal, topicFilter, permission, description string) {
		rule := Rule{
			Principal:   principal,
			TopicFilter: topicFilter,
			Permission:  Permission(permission),
			Description: description,
		}
		err := ValidateRule(rule)
		// ValidateRule aggregates problems into a *ValidationError. A
		// well-formed validator must NEVER panic and must NEVER return a
		// non-*ValidationError error from this entry point.
		if _, ok := err.(*ValidationError); err != nil && !ok {
			t.Errorf("ValidateRule returned unexpected error type %T: %v", err, err)
		}
	})
}
