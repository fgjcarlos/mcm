package server

import (
	"strings"
	"testing"
)

func TestFormatPayloadPreviewPrettyPrintsJSON(t *testing.T) {
	preview, truncated, isJSON := formatPayloadPreview([]byte(`{"ok":true,"nested":{"value":42}}`), 128)
	if truncated {
		t.Fatalf("expected json preview to fit within limit")
	}
	if !isJSON {
		t.Fatalf("expected payload to be recognised as json")
	}
	for _, want := range []string{"{\n", "\"ok\": true", "\"nested\": {"} {
		if !strings.Contains(preview, want) {
			t.Fatalf("preview missing %q; got:\n%s", want, preview)
		}
	}
}

func TestFormatPayloadPreviewTruncatesSafely(t *testing.T) {
	preview, truncated, isJSON := formatPayloadPreview([]byte("abcdefghij"), 7)
	if !truncated {
		t.Fatalf("expected preview to be truncated")
	}
	if isJSON {
		t.Fatalf("expected plain text payload to stay non-json")
	}
	if preview != "abcd..." {
		t.Fatalf("unexpected preview: %q", preview)
	}
}

func TestStateStoreTracksLatestTopicActivity(t *testing.T) {
	store := NewStateStore("tcp://localhost:1883")
	store.AddTopicMessage("factory/line1", []byte("alpha"))
	store.AddTopicMessage("factory/line2", []byte(`{"temp":19}`))

	snapshot := store.Snapshot()
	if snapshot.Broker.MessagesSeen != 2 {
		t.Fatalf("expected message count 2, got %d", snapshot.Broker.MessagesSeen)
	}
	if snapshot.Broker.ObservedTopics != 2 {
		t.Fatalf("expected observed topics 2, got %d", snapshot.Broker.ObservedTopics)
	}
	if len(snapshot.Topics) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(snapshot.Topics))
	}
	if snapshot.Topics[0].Topic != "factory/line2" {
		t.Fatalf("expected most recent topic first, got %s", snapshot.Topics[0].Topic)
	}
	if !snapshot.Topics[0].IsJSON {
		t.Fatalf("expected json flag on latest topic")
	}
}
