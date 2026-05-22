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
