package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
)

// TestHandleSettings covers GET /api/v1/settings.
func TestHandleSettings(t *testing.T) {
	t.Run("returns 200 with valid JSON", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/settings", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var resp map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if _, ok := resp["http"]; !ok {
			t.Error("response missing 'http' key")
		}
		if _, ok := resp["mosquitto"]; !ok {
			t.Error("response missing 'mosquitto' key")
		}
	})

	t.Run("response does NOT contain jwt_secret or bootstrap password", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/settings", "", token)
		app.Handler().ServeHTTP(rec, req)

		body := rec.Body.String()

		// Must not leak JWT secret
		if strings.Contains(body, "jwt_secret") {
			t.Errorf("response must not contain jwt_secret, got: %s", body)
		}
		// Must not leak bootstrap password
		if strings.Contains(body, "bootstrap") {
			t.Errorf("response must not contain bootstrap credentials, got: %s", body)
		}
		// Must not leak DSN credentials
		if strings.Contains(body, "dsn") {
			t.Errorf("response must not contain DSN, got: %s", body)
		}
	})

	t.Run("returns 401 without auth", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/settings", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}
