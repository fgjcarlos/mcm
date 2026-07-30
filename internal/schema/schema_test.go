package schema

import "testing"

func TestTopicFilterMatchesMQTTWildcards(t *testing.T) {
	tests := []struct {
		filter string
		topic  string
		want   bool
	}{
		{"factory/+/temperature", "factory/line1/temperature", true},
		{"factory/+/temperature", "factory/line1/humidity", false},
		{"factory/#", "factory/line1/temperature", true},
		{"factory/#", "factory", true},
		{"factory/line1", "factory/line1/temperature", false},
	}
	for _, tc := range tests {
		if got := TopicFilterMatches(tc.filter, tc.topic); got != tc.want {
			t.Fatalf("TopicFilterMatches(%q, %q) = %v, want %v", tc.filter, tc.topic, got, tc.want)
		}
	}
}

func TestValidateJSONPayloadAcceptsAndRejectsPayloads(t *testing.T) {
	schemaDoc := []byte(`{"type":"object","required":["temperature","unit"],"properties":{"temperature":{"type":"number"},"unit":{"type":"string"}}}`)
	valid, err := ValidateJSONPayload(schemaDoc, []byte(`{"temperature":21.5,"unit":"c"}`))
	if err != nil {
		t.Fatalf("ValidateJSONPayload valid returned error: %v", err)
	}
	if !valid.Valid || len(valid.Errors) != 0 {
		t.Fatalf("valid result = %+v, want valid with no errors", valid)
	}

	invalid, err := ValidateJSONPayload(schemaDoc, []byte(`{"temperature":"hot"}`))
	if err != nil {
		t.Fatalf("ValidateJSONPayload invalid returned error: %v", err)
	}
	if invalid.Valid || len(invalid.Errors) == 0 {
		t.Fatalf("invalid result = %+v, want validation errors", invalid)
	}
}

func TestValidateJSONPayloadRejectsMalformedSchema(t *testing.T) {
	_, err := ValidateJSONPayload([]byte(`{"type":"object","properties":{"value":{"type":"bogus"}}}`), []byte(`{"value":1}`))
	if err == nil {
		t.Fatal("ValidateJSONPayload succeeded with malformed schema, want error")
	}
}

func TestValidateJSONPayloadEnum(t *testing.T) {
	doc := []byte(`{"type":"object","properties":{"unit":{"type":"string","enum":["c","f","k"]}}}`)
	if r, _ := ValidateJSONPayload(doc, []byte(`{"unit":"c"}`)); !r.Valid {
		t.Fatalf("expected valid for unit=c, got %+v", r)
	}
	r, _ := ValidateJSONPayload(doc, []byte(`{"unit":"z"}`))
	if r.Valid {
		t.Fatalf("expected invalid for unit=z, got %+v", r)
	}
}

func TestValidateJSONPayloadNumericBounds(t *testing.T) {
	doc := []byte(`{"type":"object","properties":{"temp":{"type":"number","minimum":-40,"maximum":120}}}`)
	if r, _ := ValidateJSONPayload(doc, []byte(`{"temp":21.5}`)); !r.Valid {
		t.Fatalf("expected valid in-range temp, got %+v", r)
	}
	r, _ := ValidateJSONPayload(doc, []byte(`{"temp":1000}`))
	if r.Valid || len(r.Errors) == 0 {
		t.Fatalf("expected invalid over-max temp, got %+v", r)
	}
	r, _ = ValidateJSONPayload(doc, []byte(`{"temp":-100}`))
	if r.Valid || len(r.Errors) == 0 {
		t.Fatalf("expected invalid under-min temp, got %+v", r)
	}
}

func TestValidateJSONPayloadStringLengthAndPattern(t *testing.T) {
	doc := []byte(`{"type":"object","properties":{"name":{"type":"string","minLength":2,"maxLength":12,"pattern":"^[a-z][a-z0-9_-]+$"}}}`)
	if r, _ := ValidateJSONPayload(doc, []byte(`{"name":"line_01"}`)); !r.Valid {
		t.Fatalf("expected valid name, got %+v", r)
	}
	r, _ := ValidateJSONPayload(doc, []byte(`{"name":"a"}`))
	if r.Valid {
		t.Fatalf("expected invalid (too short), got %+v", r)
	}
	r, _ = ValidateJSONPayload(doc, []byte(`{"name":"this-name-is-too-long"}`))
	if r.Valid {
		t.Fatalf("expected invalid (too long), got %+v", r)
	}
	r, _ = ValidateJSONPayload(doc, []byte(`{"name":"1bad"}`))
	if r.Valid {
		t.Fatalf("expected invalid (pattern mismatch), got %+v", r)
	}
}

func TestValidateJSONPayloadArrayItems(t *testing.T) {
	doc := []byte(`{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string","minLength":1}}}}`)
	if r, _ := ValidateJSONPayload(doc, []byte(`{"tags":["one","two"]}`)); !r.Valid {
		t.Fatalf("expected valid string-array, got %+v", r)
	}
	r, _ := ValidateJSONPayload(doc, []byte(`{"tags":["one",""]}`))
	if r.Valid {
		t.Fatalf("expected invalid (empty element), got %+v", r)
	}
	r, _ = ValidateJSONPayload(doc, []byte(`{"tags":[1]}`))
	if r.Valid {
		t.Fatalf("expected invalid (wrong type element), got %+v", r)
	}
}

func TestValidateSchemaDocumentRejectsMalformedKeywords(t *testing.T) {
	cases := map[string]string{
		"enum not array":        `{"type":"string","enum":"x"}`,
		"empty enum":            `{"type":"string","enum":[]}`,
		"minimum not number":    `{"type":"number","minimum":"low"}`,
		"maximum not number":    `{"type":"number","maximum":true}`,
		"minLength negative":    `{"type":"string","minLength":-1}`,
		"maxLength not integer": `{"type":"string","maxLength":1.5}`,
		"pattern not string":    `{"type":"string","pattern":42}`,
		"pattern invalid regex": `{"type":"string","pattern":"["}`,
		"items not object":      `{"type":"array","items":"string"}`,
		"items malformed":       `{"type":"array","items":{"type":"bogus"}}`,
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateSchemaDocument([]byte(doc)); err == nil {
				t.Fatalf("ValidateSchemaDocument(%s) succeeded, want error", doc)
			}
		})
	}
}

func TestValidateJSONPayloadBoundedErrors(t *testing.T) {
	doc := []byte(`{"type":"object","required":["a","b","c","d","e","f","g","h"]}`)
	r, _ := ValidateJSONPayload(doc, []byte(`{}`))
	if r.Valid {
		t.Fatalf("expected invalid (missing fields), got %+v", r)
	}
	// errs slice should be capped: maxValidationErrors entries + the truncation notice.
	if len(r.Errors) > maxValidationErrors+1 {
		t.Fatalf("errors not truncated: len=%d, want <= %d", len(r.Errors), maxValidationErrors+1)
	}
	if r.Errors[len(r.Errors)-1] != "additional validation errors omitted" {
		t.Fatalf("expected truncation notice, got %q", r.Errors[len(r.Errors)-1])
	}
}
