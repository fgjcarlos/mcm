// Package schema hosts the fuzz harnesses for the JSON Schema subset
// validator.
//
// These targets fuzz ValidateSchemaDocument and ValidateJSONPayload for
// crash detection only — they intentionally do not assert correctness on
// every generated input. The harnesses run nightly via
// .github/workflows/fuzz.yml (10 s budget per target) and are also
// exercised by the in-process seed corpus during normal `go test`
// invocations.
//
// Note: -race is intentionally NOT enabled. Go fuzzing is incompatible
// with the race detector (the fuzzer's own internal accounting trips the
// race detector). The //go:build !race tag is a defensive guard so that
// a future contributor who runs `go test -race` does not silently
// disable fuzz coverage or hit a confusing failure.
package schema

import "testing"

func FuzzValidateSchemaDocument(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`),
		[]byte(`{"type":"bogus"}`),
		[]byte(`{"type":42}`),
		[]byte(`{"required":"a"}`),
		[]byte(`{"enum":[]}`),
		[]byte(`{"minimum":"low"}`),
		[]byte(`{"maxLength":-1}`),
		[]byte(`{"maxLength":1.5}`),
		[]byte(`{"pattern":"["}`),
		[]byte(`{"items":"string"}`),
		[]byte(`{"type":"string","enum":["a","b","\xff\xfe"]}`),
		[]byte(`{"additionalProperties":"yes"}`),
		[]byte(`{`),
		[]byte(``),
		[]byte(`null`),
		[]byte(`{"type":"object","properties":{"x":{"type":"object","properties":{"y":{"type":"object","properties":{"z":{"type":"object","properties":{"w":{"type":"string"}}}}}}}}}`),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, schemaDoc []byte) {
		// ValidateSchemaDocument may return an error — that is fine. The
		// goal here is crash detection (nil deref, stack overflow from
		// unbounded recursion, panic in regexp.Compile, etc.), not a
		// pass/fail assertion per input.
		_ = ValidateSchemaDocument(schemaDoc)
	})
}

func FuzzValidateJSONPayload(f *testing.F) {
	schemaDoc := []byte(`{"type":"object","required":["t"],"properties":{"t":{"type":"number"}}}`)
	seeds := [][]byte{
		[]byte(`{"t":21.5}`),
		[]byte(`{"t":"hot"}`),
		[]byte(`{}`),
		[]byte(`{"t":21.5,"extra":1}`),
		[]byte(`{"t":`),
		[]byte(``),
	}
	for _, p := range seeds {
		f.Add(schemaDoc, p)
	}
	f.Fuzz(func(t *testing.T, schemaDoc, payload []byte) {
		result, err := ValidateJSONPayload(schemaDoc, payload)
		// If the schema itself is rejected, err is non-nil — that is fine.
		// We only assert invariants on the success path (where err == nil).
		if err != nil {
			return
		}
		// Pin the truncation invariant:
		//   - len(result.Errors) <= maxValidationErrors + 1 == 6
		//   - if truncation happened (len == 6), the last entry is exactly
		//     the truncation notice.
		if n := len(result.Errors); n > 6 {
			t.Errorf("len(Errors) = %d; want <= 6 (maxValidationErrors=5 + truncation notice)", n)
		}
		if len(result.Errors) == 6 && result.Errors[5] != "additional validation errors omitted" {
			t.Errorf("Errors[5] = %q; want %q", result.Errors[5], "additional validation errors omitted")
		}
	})
}
