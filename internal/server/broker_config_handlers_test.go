package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// fakeBrokerConfigService is an in-memory brokerConfigServicer used by
// the handler tests. It returns whatever the test sets in err / result
// fields, then counts calls so the tests can assert handler dispatch.
type fakeBrokerConfigService struct {
	getResult  deploy.GetBrokerConfigResult
	getErr     error
	previewRes deploy.PreviewResult
	previewErr error
	depRes     storage.Deployment
	applyErr   error
	adoptRes   storage.BrokerConfigAdoption
	adoptErr   error
}

func (f *fakeBrokerConfigService) PreviewBrokerConfig(_ context.Context, _ string) (deploy.PreviewResult, error) {
	return f.previewRes, f.previewErr
}
func (f *fakeBrokerConfigService) ApplyBrokerConfig(_ context.Context, _, _ string, _, _ bool) (storage.Deployment, error) {
	return f.depRes, f.applyErr
}
func (f *fakeBrokerConfigService) AdoptBrokerConfig(_ context.Context, _ string) (storage.BrokerConfigAdoption, error) {
	return f.adoptRes, f.adoptErr
}
func (f *fakeBrokerConfigService) GetBrokerConfig(_ context.Context) (deploy.GetBrokerConfigResult, error) {
	return f.getResult, f.getErr
}

// newAppWithBroker wires a broker-config fake into the App's
// brokerCfgSvc slot. confPath is written with a one-line stub so the
// tests that go through the *real* service code path (PreviewBrokerConfig
// parses the file) can opt in by passing enabled=true.
func newAppWithBroker(t *testing.T, svc brokerConfigServicer, cat *catalog.Catalog, confPath string) (*App, *storage.Store) {
	t.Helper()
	app, store := newTestApp(t)
	app.brokerCfgSvc = svc
	app.brokerCfgCatalog = cat
	if confPath != "" {
		// Best-effort seed so the conf parser has a file to read.
		if err := os.MkdirAll(filepath.Dir(confPath), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(confPath, []byte("listener 1883 0.0.0.0\nallow_anonymous true\n"), 0o644); err != nil {
			t.Fatalf("write conf: %v", err)
		}
	}
	return app, store
}

func loadTestCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := catalog.LoadVersion("2.0")
	if err != nil {
		t.Fatalf("LoadVersion(2.0): %v", err)
	}
	return cat
}

// TestHandleBrokerConfigGet covers GET /api/v1/broker/config.
func TestHandleBrokerConfigGet(t *testing.T) {
	t.Run("returns 200 with body and hash", func(t *testing.T) {
		confPath := filepath.Join(t.TempDir(), "mosquitto.conf")
		cat := loadTestCatalog(t)
		svc := &fakeBrokerConfigService{getResult: deploy.GetBrokerConfigResult{
			Path: confPath,
			Body: "listener 1883 0.0.0.0\n",
			Hash: "deadbeef",
		}}
		app, store := newAppWithBroker(t, svc, cat, confPath)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/config", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp["path"] != confPath {
			t.Errorf("path = %v, want %s", resp["path"], confPath)
		}
		if resp["hash"] != "deadbeef" {
			t.Errorf("hash = %v, want deadbeef", resp["hash"])
		}
	})

	t.Run("returns 503 when broker config is disabled", func(t *testing.T) {
		svc := &fakeBrokerConfigService{getErr: deploy.ErrBrokerConfigDisabled}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/config", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
		}
		var resp errorResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if !strings.Contains(resp.Error, "broker config is not configured") {
			t.Errorf("error = %q, want substring 'broker config is not configured'", resp.Error)
		}
	})

	t.Run("returns 401 without auth", func(t *testing.T) {
		app, store := newAppWithBroker(t, &fakeBrokerConfigService{}, nil, "")
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/config", "", "")
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

// TestHandleBrokerConfigImport covers POST /api/v1/broker/config/import
// (and its alias /preview).
func TestHandleBrokerConfigImport(t *testing.T) {
	t.Run("returns 200 with PreviewResult", func(t *testing.T) {
		confPath := filepath.Join(t.TempDir(), "mosquitto.conf")
		cat := loadTestCatalog(t)
		svc := &fakeBrokerConfigService{previewRes: deploy.PreviewResult{
			RevisionID:       "rev-1",
			Kind:             "broker_config",
			BaseConfHash:     "h1",
			RenderedConfHash: "h2",
			ConfBody:         "listener 1883 0.0.0.0\n",
			ConfDiff:         "",
			ReloadKind:       "reload",
			HasChanges:       false,
		}}
		app, store := newAppWithBroker(t, svc, cat, confPath)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/import", `{}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp deploy.PreviewResult
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.RevisionID != "rev-1" {
			t.Errorf("revision_id = %q, want rev-1", resp.RevisionID)
		}
	})

	t.Run("/preview alias returns the same body", func(t *testing.T) {
		svc := &fakeBrokerConfigService{previewRes: deploy.PreviewResult{RevisionID: "rev-2"}}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/preview", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp deploy.PreviewResult
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if resp.RevisionID != "rev-2" {
			t.Errorf("revision_id = %q, want rev-2", resp.RevisionID)
		}
	})

	t.Run("returns 503 when broker config is disabled", func(t *testing.T) {
		svc := &fakeBrokerConfigService{previewErr: deploy.ErrBrokerConfigDisabled}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/import", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}

// TestHandleBrokerConfigAdopt covers POST /api/v1/broker/config/adopt.
func TestHandleBrokerConfigAdopt(t *testing.T) {
	t.Run("returns 200 with adoption row", func(t *testing.T) {
		svc := &fakeBrokerConfigService{adoptRes: storage.BrokerConfigAdoption{
			ID:         1,
			SourcePath: "/etc/mosquitto/mosquitto.conf",
			AdoptedBy:  "admin",
			AdoptedAt:  time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		}}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/adopt", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp storage.BrokerConfigAdoption
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ID != 1 || resp.SourcePath != "/etc/mosquitto/mosquitto.conf" {
			t.Errorf("unexpected adoption: %+v", resp)
		}
	})
}

// TestHandleBrokerConfigApply covers POST /api/v1/broker/config/apply
// across the lifecycle sentinels and the happy path.
func TestHandleBrokerConfigApply(t *testing.T) {
	t.Run("returns 400 when revision_id is missing", func(t *testing.T) {
		svc := &fakeBrokerConfigService{applyErr: deploy.ErrRevisionMissing}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/apply", "{}", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 200 with deployment on success", func(t *testing.T) {
		svc := &fakeBrokerConfigService{depRes: storage.Deployment{
			ID:           42,
			Actor:        "admin",
			Status:       "active_verified",
			Kind:         "broker_config",
			ReloadKind:   "reload",
			BaseConfHash: "h1",
		}}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/apply",
			`{"revision_id":"rev-1","force":true,"adopt":false}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp storage.Deployment
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.ID != 42 || resp.Status != "active_verified" {
			t.Errorf("unexpected deployment: %+v", resp)
		}
	})

	t.Run("returns 409 with directives on requires_restart", func(t *testing.T) {
		// deploy service formats ErrRequiresRestart as
		// "apply requires broker restart: directives <list>" using
		// fmt.Errorf("%w: directives ...", ErrRequiresRestart, ...).
		svc := &fakeBrokerConfigService{
			applyErr: fmt.Errorf("%w: directives persistence_location", deploy.ErrRequiresRestart),
		}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/apply",
			`{"revision_id":"rev-1"}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !strings.Contains(resp["error"].(string), "requires broker restart") {
			t.Errorf("error = %v, want substring 'requires broker restart'", resp["error"])
		}
		if resp["directives"] != "persistence_location" {
			t.Errorf("directives = %v, want persistence_location", resp["directives"])
		}
	})

	t.Run("returns 409 on adoption_required", func(t *testing.T) {
		svc := &fakeBrokerConfigService{applyErr: deploy.ErrAdoptionRequired}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/apply",
			`{"revision_id":"rev-1"}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
		}
		var resp errorResponse
		_ = json.NewDecoder(rec.Body).Decode(&resp)
		if !strings.Contains(resp.Error, "adoption") {
			t.Errorf("error = %q, want substring 'adoption'", resp.Error)
		}
	})

	t.Run("returns 409 on revision_mismatch", func(t *testing.T) {
		svc := &fakeBrokerConfigService{applyErr: deploy.ErrRevisionMismatch}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/apply",
			`{"revision_id":"rev-1"}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409", rec.Code)
		}
	})

	t.Run("returns 503 when broker config is disabled", func(t *testing.T) {
		svc := &fakeBrokerConfigService{applyErr: deploy.ErrBrokerConfigDisabled}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/config/apply",
			`{"revision_id":"rev-1"}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestHandleBrokerConfigCatalog covers GET /api/v1/broker/config/catalog.
func TestHandleBrokerConfigCatalog(t *testing.T) {
	t.Run("returns 200 with version and directives", func(t *testing.T) {
		cat := loadTestCatalog(t)
		app, store := newAppWithBroker(t, &fakeBrokerConfigService{}, cat, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/config/catalog", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp["version"] != "2.0" {
			t.Errorf("version = %v, want 2.0", resp["version"])
		}
		if _, ok := resp["directives"].([]any); !ok {
			t.Errorf("directives field must be an array, got %T", resp["directives"])
		}
	})

	t.Run("returns 503 when catalog is not loaded", func(t *testing.T) {
		app, store := newAppWithBroker(t, &fakeBrokerConfigService{}, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/config/catalog", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503", rec.Code)
		}
	})
}

// TestBrokerConfigDisabledDefaults verifies that a freshly-constructed
// App (no brokerCfgSvc wired) returns 503 for every broker-config
// endpoint — the contract used by Run when ConfigDir is empty.
func TestBrokerConfigDisabledDefaults(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	cases := []struct {
		method, path, body string
	}{
		{http.MethodGet, "/api/v1/broker/config", ""},
		{http.MethodPost, "/api/v1/broker/config/import", "{}"},
		{http.MethodPost, "/api/v1/broker/config/preview", ""},
		{http.MethodPost, "/api/v1/broker/config/adopt", ""},
		{http.MethodPost, "/api/v1/broker/config/apply", `{"revision_id":"rev-1"}`},
		{http.MethodGet, "/api/v1/broker/config/catalog", ""},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := authedRequest(tc.method, tc.path, tc.body, token)
			app.Handler().ServeHTTP(rec, req)
			// The catalog endpoint reads a nil catalog and returns 503
			// (different message). All other endpoints hit the service
			// which is nil and return 503 via the same error mapping.
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
			}
		})
	}
}