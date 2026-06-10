package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/fgjcarlos/mcm/internal/config"
)

// ---------------------------------------------------------------------------
// withSecurityHeaders — unit tests
// ---------------------------------------------------------------------------

// TestSecurityHeadersOnAPIResponse verifies that withSecurityHeaders injects all
// four mandatory headers on a regular JSON response from the API.
func TestSecurityHeadersOnAPIResponse(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	app.Handler().ServeHTTP(rec, req)

	assertSecurityHeaders(t, rec.Header())
}

// TestSecurityHeadersDirectMiddleware verifies withSecurityHeaders in isolation
// (covers the "static/generic handler" path without needing a real frontendFS).
func TestSecurityHeadersDirectMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := withSecurityHeaders(inner)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(rec, req)

	assertSecurityHeaders(t, rec.Header())
}

// assertSecurityHeaders checks that all four mandatory security response headers
// are present with their exact prescribed values.
func assertSecurityHeaders(t *testing.T, h http.Header) {
	t.Helper()

	cases := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"Referrer-Policy", "no-referrer"},
		{"Content-Security-Policy", "default-src 'self'; script-src 'self'; frame-ancestors 'none'"},
	}

	for _, tc := range cases {
		got := h.Get(tc.header)
		if got != tc.want {
			t.Errorf("%s = %q, want %q", tc.header, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// withCORS — unit tests
// ---------------------------------------------------------------------------

// TestCORSAllowedOriginSetsHeaders checks that a request from a configured
// allowed origin receives the full set of CORS response headers, including
// exactly one Vary: Origin value and Access-Control-Expose-Headers: X-Request-ID.
func TestCORSAllowedOriginSetsHeaders(t *testing.T) {
	handler := withCORS(okHandler(), []string{"https://allowed.example.com"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://allowed.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://allowed.example.com")
	}
	// Exactly one Vary: Origin — must not be duplicated.
	if got := rec.Header()["Vary"]; len(got) != 1 || got[0] != "Origin" {
		t.Errorf("Vary = %v, want exactly [Origin]", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want %q", got, "true")
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "X-Request-ID" {
		t.Errorf("Access-Control-Expose-Headers = %q, want %q", got, "X-Request-ID")
	}
}

// TestCORSDisallowedOriginSetsNoHeaders verifies that a request from an origin
// NOT in the allowed list does not receive any Access-Control-* headers, but
// DOES receive Vary: Origin to prevent cache poisoning across origins.
func TestCORSDisallowedOriginSetsNoHeaders(t *testing.T) {
	handler := withCORS(okHandler(), []string{"https://allowed.example.com"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q (must be set even for disallowed origins to prevent cache poisoning)", got, "Origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty", got)
	}
}

// TestCORSPreflightAllowedOriginReturns204 checks that an OPTIONS preflight from
// an allowed origin returns 204 with all preflight headers, and does NOT invoke
// the inner handler (which would change the status to 200).
func TestCORSPreflightAllowedOriginReturns204(t *testing.T) {
	var innerCalled bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	handler := withCORS(inner, []string{"https://allowed.example.com"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://allowed.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rec.Code)
	}
	if innerCalled {
		t.Error("inner handler was called during preflight; it should not be")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods is missing on preflight response")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("Access-Control-Allow-Headers is missing on preflight response")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Error("Access-Control-Max-Age is missing on preflight response")
	}
}

// TestCORSPreflightDisallowedOriginPassesThrough verifies that a preflight from a
// disallowed origin gets no Access-Control-Allow-Origin header.
func TestCORSPreflightDisallowedOriginPassesThrough(t *testing.T) {
	handler := withCORS(okHandler(), []string{"https://allowed.example.com"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/status", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "GET")
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty for disallowed origin", got)
	}
}

// TestCORSNoOriginHeaderPassesThrough confirms that same-origin (no Origin header)
// requests are passed through normally with no CORS headers added.
func TestCORSNoOriginHeaderPassesThrough(t *testing.T) {
	handler := withCORS(okHandler(), []string{"https://allowed.example.com"})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q, want empty when no Origin header", got)
	}
}

// ---------------------------------------------------------------------------
// Integration tests — security headers wired into app.Handler()
// ---------------------------------------------------------------------------

// TestSecurityHeadersWiredIntoHandler confirms that the full handler stack
// (including the new security middleware) emits security headers on API responses.
func TestSecurityHeadersWiredIntoHandler(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	app.Handler().ServeHTTP(rec, req)

	assertSecurityHeaders(t, rec.Header())
}

// TestCORSWiredIntoHandlerWithAllowedOrigin verifies the full handler stack
// injects CORS headers when the origin is allowed via config.
func TestCORSWiredIntoHandlerWithAllowedOrigin(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	// Inject an allowed origin into the app's config after creation.
	app.cfg.HTTP.CORS.AllowedOrigins = []string{"https://dash.example.com"}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", "https://dash.example.com")
	app.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://dash.example.com" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "https://dash.example.com")
	}
}

// ---------------------------------------------------------------------------
// Config — CORSConfig struct and HTTPConfig.CORS field
// ---------------------------------------------------------------------------

// TestSecurityHeadersOnStaticSPAResponse is an integration test that verifies all
// four mandatory security headers are present on a GET / response served through
// the real wired handler stack when a frontend FS is registered.
func TestSecurityHeadersOnStaticSPAResponse(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	// Inject a minimal in-memory frontend FS so spaHandler is registered at /.
	app.frontendFS = fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><html><body><div id=\"root\"></div></body></html>")},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	app.Handler().ServeHTTP(rec, req)

	assertSecurityHeaders(t, rec.Header())
}

// TestCORSConfigDefaultIsEmpty verifies that the default config has an empty
// allowed-origins list (strict same-origin by default).
func TestCORSConfigDefaultIsEmpty(t *testing.T) {
	cfg := config.Default()
	if len(cfg.HTTP.CORS.AllowedOrigins) != 0 {
		t.Errorf("default CORS.AllowedOrigins len = %d, want 0", len(cfg.HTTP.CORS.AllowedOrigins))
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}
