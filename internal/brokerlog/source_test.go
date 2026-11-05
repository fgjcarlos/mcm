package brokerlog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLineClassification(t *testing.T) {
	cases := []struct {
		line, severity, kind, clientID, topic string
	}{
		{"New client connected from 127.0.0.1:1883 as sensor-a (p2, c1, k60)", "info", "client_connected", "sensor-a", ""},
		{"Client sensor-a disconnected.", "info", "client_disconnected", "sensor-a", ""},
		{"Received PUBLISH from sensor-a (d0, q0, r0, m1, 'sensors/temp', ...)", "debug", "publish_received", "sensor-a", "sensors/temp"},
		{"Opening new connection", "info", "connection_opening", "", ""},
		{"Error: Address already in use", "error", "error", "", ""},
		{"Warning: listener issue", "warning", "warning", "", ""},
		{"Client sent PINGREQ", "info", "unclassified", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			entry, ok := parseLine(tc.line, 1024)
			if !ok {
				t.Fatal("line was ignored")
			}
			if string(entry.Severity) != tc.severity || entry.Kind != tc.kind || entry.ClientID != tc.clientID || entry.Topic != tc.topic {
				t.Fatalf("entry = %+v; want severity=%s kind=%s client=%q topic=%q", entry, tc.severity, tc.kind, tc.clientID, tc.topic)
			}
			if entry.ObservedAt.IsZero() || entry.Message == "" {
				t.Fatalf("entry lacks timestamp/message: %+v", entry)
			}
		})
	}
}

func testSource(t *testing.T, content string, limits Limits) (*FileSource, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mosquitto.log")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	source, err := NewFileSource(path, limits)
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	return source, path
}

func TestFileSourceFiltersAndLimits(t *testing.T) {
	limits := Limits{MaxEntries: 3, MaxEntriesPerRead: 2, MaxMessageBytes: 18, MaxAge: time.Hour, MaxBytesPerRead: 4096}
	source, _ := testSource(t, "Opening new connection\nWarning: discard me\nError: keep error\nNew client connected from 127.0.0.1:1 as sensor-123456789\n", limits)
	got, err := source.Read(context.Background(), Filter{MinimumSeverity: SeverityWarning, Include: []string{"keep", "sensor"}, Exclude: []string{"discard"}})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !got.Available || len(got.Entries) != 1 || got.Entries[0].Kind != "error" {
		t.Fatalf("filtered result = %+v", got)
	}

	got, err = source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(got.Entries) != 2 {
		t.Fatalf("per-read cap returned %d entries, want 2", len(got.Entries))
	}
	for _, entry := range got.Entries {
		if len(entry.Message) > limits.MaxMessageBytes {
			t.Fatalf("message length %d exceeds cap", len(entry.Message))
		}
	}
	// The three-entry ring retains the newest three lines; returned entries
	// are the newest two and remain ordered oldest-to-newest.
	if got.Entries[0].Kind != "error" || got.Entries[1].Kind != "client_connected" {
		t.Fatalf("entries = %+v", got.Entries)
	}
	got, err = source.Read(context.Background(), Filter{MinimumSeverity: SeverityDebug, Exclude: []string{"keep"}})
	if err != nil {
		t.Fatalf("Read with exclude filter: %v", err)
	}
	for _, entry := range got.Entries {
		if strings.Contains(entry.Message, "keep") {
			t.Fatalf("exclude pattern admitted entry: %+v", entry)
		}
	}
}

func TestFileSourceAgeLimit(t *testing.T) {
	source, _ := testSource(t, "Opening new connection\n", Limits{MaxEntries: 2, MaxEntriesPerRead: 2, MaxMessageBytes: 100, MaxAge: time.Hour, MaxBytesPerRead: 100})
	if _, err := source.Read(context.Background(), Filter{}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	source.mu.Lock()
	source.entries[0].ObservedAt = time.Now().Add(-time.Hour)
	source.mu.Unlock()
	result, err := source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(result.Entries) != 0 {
		t.Fatalf("expired entry retained: %+v", result.Entries)
	}
}

func TestFileSourceByteBoundAndPartialLine(t *testing.T) {
	limits := Limits{MaxEntries: 10, MaxEntriesPerRead: 10, MaxMessageBytes: 100, MaxAge: time.Hour, MaxBytesPerRead: 10}
	source, path := testSource(t, "Opening new connection\n", limits)
	first, err := source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(first.Entries) != 0 || source.offset != 10 {
		t.Fatalf("first poll consumed/returned wrong data: offset=%d result=%+v", source.offset, first)
	}
	for i := 0; i < 3; i++ {
		if _, err := source.Read(context.Background(), Filter{}); err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	result, err := source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(result.Entries) != 1 || result.Entries[0].Message != "Opening new connection" {
		t.Fatalf("completed line = %+v", result)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 200)), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
}

func TestFileSourceRotationAndTruncation(t *testing.T) {
	limits := Limits{MaxEntries: 10, MaxEntriesPerRead: 10, MaxMessageBytes: 100, MaxAge: time.Hour, MaxBytesPerRead: 4096}
	source, path := testSource(t, "Opening new connection\n", limits)
	if _, err := source.Read(context.Background(), Filter{}); err != nil {
		t.Fatalf("initial Read: %v", err)
	}
	rotated := path + ".1"
	if err := os.Rename(path, rotated); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if err := os.WriteFile(path, []byte("Client next disconnected\n"), 0o600); err != nil {
		t.Fatalf("create replacement: %v", err)
	}
	result, err := source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("rotated Read: %v", err)
	}
	if len(result.Entries) != 2 || result.Entries[0].Kind != "connection_opening" || result.Entries[1].ClientID != "next" {
		t.Fatalf("rotation result = %+v", result)
	}

	if err := os.WriteFile(path, []byte("Opening new connection\n"), 0o600); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	result, err = source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("truncated Read: %v", err)
	}
	if len(result.Entries) != 3 || result.Entries[2].Kind != "connection_opening" {
		t.Fatalf("truncation result = %+v", result)
	}
}

func TestFileSourceMissingIsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.log")
	source, err := NewFileSource(path, DefaultLimits())
	if err != nil {
		t.Fatalf("NewFileSource: %v", err)
	}
	result, err := source.Read(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("Read missing file: %v", err)
	}
	if result.Available || len(result.Entries) != 0 {
		t.Fatalf("missing file result = %+v", result)
	}
}

func TestNewFileSourceRejectsUnboundedLimits(t *testing.T) {
	limits := DefaultLimits()
	limits.MaxBytesPerRead = maxAllowedPollBytes + 1
	if _, err := NewFileSource("log", limits); err == nil {
		t.Fatal("accepted an unbounded poll size")
	}
}

func TestFileSourceRejectsOversizedFilterWithoutReading(t *testing.T) {
	source, _ := testSource(t, "Opening new connection\\n", DefaultLimits())
	result, err := source.Read(context.Background(), Filter{Include: []string{strings.Repeat("x", maxFilterPatternBytes+1)}})
	if err == nil {
		t.Fatal("Read accepted an oversized filter pattern")
	}
	if result.Available || len(result.Entries) != 0 || source.offset != 0 {
		t.Fatalf("rejected filter changed source state: result=%+v offset=%d", result, source.offset)
	}
}
