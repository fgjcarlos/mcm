package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// seedEdgeSite upserts an edge site directly in the store for test setup.
func seedEdgeSite(t *testing.T, store *storage.Store, id, name, status string) storage.EdgeSite {
	t.Helper()
	site := &storage.EdgeSite{
		ID:         id,
		Name:       name,
		Version:    "0.1.0",
		Status:     status,
		LastSeenAt: time.Now().UTC(),
	}
	if err := store.UpsertEdgeSite(context.Background(), site); err != nil {
		t.Fatalf("seedEdgeSite: UpsertEdgeSite returned error: %v", err)
	}
	return *site
}

// TestHandleHeartbeat covers POST /api/v1/edge/heartbeat.
func TestHandleHeartbeat(t *testing.T) {
	t.Run("success returns 200 with site data", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		body := `{"site_id":"gw-01","name":"Factory Gateway","version":"0.1.0","status":"healthy","message":"all systems nominal"}`
		req := authedRequest(http.MethodPost, "/api/v1/edge/heartbeat", body, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var resp storage.EdgeSite
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ID != "gw-01" {
			t.Errorf("ID = %q, want gw-01", resp.ID)
		}
		if resp.Name != "Factory Gateway" {
			t.Errorf("Name = %q, want Factory Gateway", resp.Name)
		}
		if resp.Status != "healthy" {
			t.Errorf("Status = %q, want healthy", resp.Status)
		}
		if resp.Message != "all systems nominal" {
			t.Errorf("Message = %q, want all systems nominal", resp.Message)
		}
		if resp.LastSeenAt.IsZero() {
			t.Error("last_seen_at must be set")
		}
		if resp.CreatedAt.IsZero() {
			t.Error("created_at must be set")
		}
	})

	t.Run("missing site_id returns 400", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/edge/heartbeat", `{"name":"GW","status":"healthy"}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "site_id") {
			t.Errorf("response body missing site_id mention: %s", rec.Body.String())
		}
	})

	t.Run("invalid status returns 400", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		body := `{"site_id":"gw-01","name":"GW","status":"online"}`
		req := authedRequest(http.MethodPost, "/api/v1/edge/heartbeat", body, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "status") {
			t.Errorf("response body missing status mention: %s", rec.Body.String())
		}
	})

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		body := `{"site_id":"gw-01","name":"GW","status":"healthy"}`
		req := authedRequest(http.MethodPost, "/api/v1/edge/heartbeat", body, "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("viewer is forbidden", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "view", "secret", auth.RoleViewer)
		token := loginAs(t, app, "view", "secret")

		rec := httptest.NewRecorder()
		body := `{"site_id":"gw-01","name":"GW","status":"healthy"}`
		req := authedRequest(http.MethodPost, "/api/v1/edge/heartbeat", body, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusForbidden)
		}
	})
}

// TestHandleListSites covers GET /api/v1/edge/sites.
func TestHandleListSites(t *testing.T) {
	t.Run("returns list of sites", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "view", "secret", auth.RoleViewer)
		token := loginAs(t, app, "view", "secret")
		seedEdgeSite(t, store, "gw-a", "Gateway A", "healthy")
		seedEdgeSite(t, store, "gw-b", "Gateway B", "degraded")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/edge/sites", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var resp struct {
			Sites []storage.EdgeSite `json:"sites"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if len(resp.Sites) != 2 {
			t.Errorf("sites length = %d, want 2", len(resp.Sites))
		}
	})

	t.Run("empty list returns sites key with empty array", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "view", "secret", auth.RoleViewer)
		token := loginAs(t, app, "view", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/edge/sites", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"sites"`) {
			t.Errorf("response body missing sites key: %s", rec.Body.String())
		}
	})

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/edge/sites", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}

// TestHandleGetSite covers GET /api/v1/edge/sites/{id}.
func TestHandleGetSite(t *testing.T) {
	t.Run("returns site after heartbeat", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		// Send a heartbeat first.
		hbBody := `{"site_id":"gw-find-me","name":"Find Me","version":"1.0.0","status":"healthy"}`
		hbRec := httptest.NewRecorder()
		hbReq := authedRequest(http.MethodPost, "/api/v1/edge/heartbeat", hbBody, token)
		app.Handler().ServeHTTP(hbRec, hbReq)
		if hbRec.Code != http.StatusOK {
			t.Fatalf("heartbeat status = %d, want %d, body = %s", hbRec.Code, http.StatusOK, hbRec.Body.String())
		}

		// Use viewer token to fetch the site.
		seedAdminUserWithRole(t, store, "view", "secret", auth.RoleViewer)
		viewToken := loginAs(t, app, "view", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/edge/sites/gw-find-me", "", viewToken)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}

		var resp storage.EdgeSite
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ID != "gw-find-me" {
			t.Errorf("ID = %q, want gw-find-me", resp.ID)
		}
		if resp.Name != "Find Me" {
			t.Errorf("Name = %q, want Find Me", resp.Name)
		}
	})

	t.Run("unknown id returns 404", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "view", "secret", auth.RoleViewer)
		token := loginAs(t, app, "view", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/edge/sites/does-not-exist", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/edge/sites/gw-01", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}
