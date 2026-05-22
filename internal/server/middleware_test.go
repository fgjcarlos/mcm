package server

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDMiddlewareGeneratesIDWhenAbsent(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := withRequestLogging(inner, discardSlogLogger(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	handler.ServeHTTP(rec, req)

	if capturedID == "" {
		t.Fatal("RequestIDFromContext returned empty value; middleware did not inject an ID")
	}
	if header := rec.Header().Get(RequestIDHeader); header == "" {
		t.Fatal("response missing X-Request-ID header")
	} else if header != capturedID {
		t.Fatalf("response X-Request-ID = %q, want context value %q", header, capturedID)
	}
}

func TestRequestIDMiddlewareEchoesIncomingID(t *testing.T) {
	const incoming = "client-supplied-id-123"

	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := withRequestLogging(inner, discardSlogLogger(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set(RequestIDHeader, incoming)
	handler.ServeHTTP(rec, req)

	if capturedID != incoming {
		t.Fatalf("captured request id = %q, want %q", capturedID, incoming)
	}
	if got := rec.Header().Get(RequestIDHeader); got != incoming {
		t.Fatalf("response X-Request-ID = %q, want %q", got, incoming)
	}
}

func TestRequestIDMiddlewareSanitizesControlCharacters(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withRequestLogging(inner, discardSlogLogger(), nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set(RequestIDHeader, "abc\r\nLine-Injected: bad")
	handler.ServeHTTP(rec, req)

	got := rec.Header().Get(RequestIDHeader)
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("sanitized X-Request-ID still contains control chars: %q", got)
	}
	if got == "" {
		t.Fatal("sanitized X-Request-ID is empty; expected printable subset to survive")
	}
}

func TestAccessLogIncludesMethodPathStatusAndRequestID(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	handler := withRequestLogging(inner, logger, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/example", nil)
	req.Header.Set(RequestIDHeader, "fixed-request-id")
	handler.ServeHTTP(rec, req)

	var record map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &record); err != nil {
		t.Fatalf("access log not valid JSON: %v; raw=%s", err, buf.String())
	}
	if record["msg"] != "http_request" {
		t.Fatalf("msg = %v, want http_request", record["msg"])
	}
	if record["method"] != http.MethodPost {
		t.Fatalf("method = %v, want %s", record["method"], http.MethodPost)
	}
	if record["path"] != "/api/v1/example" {
		t.Fatalf("path = %v, want /api/v1/example", record["path"])
	}
	if status, ok := record["status"].(float64); !ok || int(status) != http.StatusTeapot {
		t.Fatalf("status = %v, want %d", record["status"], http.StatusTeapot)
	}
	if record["request_id"] != "fixed-request-id" {
		t.Fatalf("request_id = %v, want fixed-request-id", record["request_id"])
	}
	if _, ok := record["duration"]; !ok {
		t.Fatal("access log missing duration attribute")
	}
}

func discardSlogLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
