package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
)

func TestNewJSONFormatEmitsStructuredFields(t *testing.T) {
	buf := &bytes.Buffer{}
	logger, err := New(config.LoggingConfig{Level: "info", Format: "json"}, buf)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Info("http_request", slog.String("method", "GET"), slog.Int("status", 200))

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("json output not valid JSON: %v; raw=%s", err, buf.String())
	}
	if record["msg"] != "http_request" || record["method"] != "GET" {
		t.Fatalf("missing fields: %v", record)
	}
}

func TestNewTextFormatReadable(t *testing.T) {
	buf := &bytes.Buffer{}
	logger, err := New(config.LoggingConfig{Level: "debug", Format: "text"}, buf)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Debug("debug_event", slog.String("key", "value"))
	if !strings.Contains(buf.String(), "debug_event") || !strings.Contains(buf.String(), `key=value`) {
		t.Fatalf("text output missing fields: %s", buf.String())
	}
}

func TestNewRejectsUnknownLevel(t *testing.T) {
	if _, err := New(config.LoggingConfig{Level: "trace", Format: "json"}, nil); err == nil {
		t.Fatal("expected error for unknown log level")
	}
}

func TestNewRejectsUnknownFormat(t *testing.T) {
	if _, err := New(config.LoggingConfig{Level: "info", Format: "yaml"}, nil); err == nil {
		t.Fatal("expected error for unknown log format")
	}
}

func TestNewRespectsLevel(t *testing.T) {
	buf := &bytes.Buffer{}
	logger, err := New(config.LoggingConfig{Level: "warn", Format: "json"}, buf)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	logger.Info("hidden")
	if buf.Len() != 0 {
		t.Fatalf("warn level should drop info; got %s", buf.String())
	}
	logger.Warn("visible")
	if !strings.Contains(buf.String(), "visible") {
		t.Fatalf("warn level should keep warn; got %s", buf.String())
	}
}

func TestDiscardLoggerSilent(t *testing.T) {
	// Sanity: discard logger never panics and ignores values.
	logger := Discard()
	logger.InfoContext(context.Background(), "ignored")
}
