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

// TestRequestBodyLimitLoginReturns413 verifies that an oversized body sent to the
// pre-auth POST /api/v1/auth/login endpoint returns HTTP 413, not 400 or 401.
// This test fails on main (no MaxBytesReader applied) and must pass after the fix.
func TestRequestBodyLimitLoginReturns413(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	// Build a body that exceeds apiBodyLimitBytes (1 MiB).
	oversized := bytes.Repeat([]byte("a"), int(apiBodyLimitBytes)+1)
	body := append([]byte(`{"username":"admin","password":"`), oversized...)
	body = append(body, '"', '}')

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (413); body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("body is not valid JSON: %v; raw=%s", err, rec.Body.String())
	}
	if errResp.Error == "" {
		t.Fatal("error envelope missing 'error' field")
	}
}

// TestRequestBodyLimitMutationRouteReturns413 verifies that an oversized body on an
// authenticated mutation route (POST /api/v1/admin-users) also returns HTTP 413.
func TestRequestBodyLimitMutationRouteReturns413(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	token := loginAsSeededUser(t, app, store, "admin", "secret-password")

	oversized := bytes.Repeat([]byte("x"), int(apiBodyLimitBytes)+1)
	body := append([]byte(`{"username":"`), oversized...)
	body = append(body, '"', '}')

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin-users", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (413); body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("body is not valid JSON: %v; raw=%s", err, rec.Body.String())
	}
	if errResp.Error == "" {
		t.Fatal("error envelope missing 'error' field")
	}
}

// TestRequestBodyLimitMFAVerifyReturns413 verifies that an oversized body on the
// authenticated POST /api/v1/auth/mfa/verify endpoint returns HTTP 413, not 400.
func TestRequestBodyLimitMFAVerifyReturns413(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	// MFA verify requires a valid session token; seed a user and log in.
	seedAdminUser(t, store, "admin", "secret-password", false)
	token := loginAs(t, app, "admin", "secret-password")

	oversized := bytes.Repeat([]byte("x"), int(apiBodyLimitBytes)+1)
	body := append([]byte(`{"code":"`), oversized...)
	body = append(body, '"', '}')

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/verify", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (413); body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("body is not valid JSON: %v; raw=%s", err, rec.Body.String())
	}
	if errResp.Error == "" {
		t.Fatal("error envelope missing 'error' field")
	}
}

// TestRequestBodyLimitMFADisableReturns413 verifies that an oversized body on the
// authenticated DELETE /api/v1/auth/mfa endpoint returns HTTP 413, not 400.
func TestRequestBodyLimitMFADisableReturns413(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	token := loginAs(t, app, "admin", "secret-password")

	oversized := bytes.Repeat([]byte("x"), int(apiBodyLimitBytes)+1)
	body := append([]byte(`{"password":"`), oversized...)
	body = append(body, '"', '}')

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/mfa", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (413); body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("body is not valid JSON: %v; raw=%s", err, rec.Body.String())
	}
	if errResp.Error == "" {
		t.Fatal("error envelope missing 'error' field")
	}
}

// TestRequestBodyLimitACLCreateReturns413 verifies that an oversized body on
// POST /api/v1/acls (backed by decodeRuleRequest → writeAPIError) returns HTTP 413,
// not 400, and does not leak the internal error string.
func TestRequestBodyLimitACLCreateReturns413(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	token := loginAsSeededUser(t, app, store, "admin", "secret-password")

	oversized := bytes.Repeat([]byte("x"), int(apiBodyLimitBytes)+1)
	body := append([]byte(`{"principal":"`), oversized...)
	body = append(body, '"', '}')

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d (413); body = %s", rec.Code, http.StatusRequestEntityTooLarge, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}
	var errResp struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&errResp); err != nil {
		t.Fatalf("body is not valid JSON: %v; raw=%s", err, rec.Body.String())
	}
	if errResp.Error == "" {
		t.Fatal("error envelope missing 'error' field")
	}
	// The internal error string must not be leaked to the client.
	if strings.Contains(errResp.Error, "http: request body too large") {
		t.Fatalf("response leaks internal error string: %s", errResp.Error)
	}
}

// TestRequestBodyLimitNormalRequestSucceeds verifies that a normal-sized login
// request is not rejected by the body limit middleware.
func TestRequestBodyLimitNormalRequestSucceeds(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestRequestIDMiddlewareGeneratesIDWhenAbsent(t *testing.T) {
	var capturedID string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = RequestIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	handler := withRequestLogging(inner, discardSlogLogger(), nil, nil)
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

	handler := withRequestLogging(inner, discardSlogLogger(), nil, nil)
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
	handler := withRequestLogging(inner, discardSlogLogger(), nil, nil)
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

	handler := withRequestLogging(inner, logger, nil, nil)
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
