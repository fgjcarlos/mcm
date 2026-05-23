package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/logging"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// newTestAppWithDeploy creates a test App with a real deploy.Service wired to
// temp files so the "deploy enabled" HTTP paths can be exercised.
func newTestAppWithDeploy(t *testing.T) (*App, *storage.Store) {
	t.Helper()

	dir := t.TempDir()
	aclPath := filepath.Join(dir, "acl")
	passwdPath := filepath.Join(dir, "passwd")

	// Write empty files so reads in Preview do not return ErrNotExist.
	if err := os.WriteFile(aclPath, []byte{}, 0o600); err != nil {
		t.Fatalf("write temp acl file: %v", err)
	}
	if err := os.WriteFile(passwdPath, []byte{}, 0o600); err != nil {
		t.Fatalf("write temp passwd file: %v", err)
	}

	cfg := config.Default()
	cfg.Database.Path = filepath.Join(dir, "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{}
	cfg.Mosquitto.Deploy.Mode = "file"
	cfg.Mosquitto.Deploy.ACLPath = aclPath
	cfg.Mosquitto.Deploy.PasswdPath = passwdPath

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}

	app, err := New(cfg, store, logging.Discard())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	// Override the healthcheck so Apply does not try to dial a real broker.
	alwaysOKHealthCheck := func(_ context.Context, _ config.MosquittoConfig) diagnostics.MQTTResult {
		return diagnostics.MQTTResult{OK: true, Message: "test ok"}
	}
	// Replace the deploy service with one using a fake healthcheck but real
	// FileApplier and real storage — this exercises the full apply path.
	app.deploySvc = deploy.NewService(
		mosquitto.FileApplier{
			ACLPath:    aclPath,
			PasswdPath: passwdPath,
		},
		store.ACLStore(),
		store,
		store,
		alwaysOKHealthCheck,
		cfg.Mosquitto,
		cfg.Mosquitto.Deploy,
		func(ctx context.Context, actor, action, resourceType, resourceID, result string, metadata []byte) {
			_, _ = store.RecordAuditEvent(ctx, storage.CreateAuditEventParams{
				OccurredAt:   time.Now().UTC(),
				Actor:        actor,
				Action:       action,
				ResourceType: resourceType,
				ResourceID:   resourceID,
				Result:       result,
				Metadata:     metadata,
			})
		},
	)
	app.now = func() time.Time { return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC) }

	return app, store
}

// --- Preview endpoint tests ---

// TestHandleDeployPreview_Disabled verifies that POST /api/v1/deployments/preview
// returns 422 when deploy mode is not configured.
func TestHandleDeployPreview_Disabled(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	token := loginAs(t, app, "viewer", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "deploy mode is not configured") {
		t.Errorf("response body missing expected message: %s", rec.Body.String())
	}
}

// TestHandleDeployPreview_Unauthenticated verifies that unauthenticated requests
// to preview are rejected with 401.
func TestHandleDeployPreview_Unauthenticated(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", "")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

// TestHandleDeployPreview_Success verifies that preview returns 200 with diff
// fields and has_changes when deploy is enabled.
func TestHandleDeployPreview_Success(t *testing.T) {
	app, store := newTestAppWithDeploy(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	token := loginAs(t, app, "viewer", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp previewResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	// Files are empty and store is empty so there are no changes.
	if resp.HasChanges {
		t.Errorf("has_changes = true, want false when files and store are both empty")
	}
}

// --- Apply endpoint tests ---

// TestHandleDeployApply_Disabled verifies that POST /api/v1/deployments/apply
// returns 422 when deploy mode is not configured.
func TestHandleDeployApply_Disabled(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "deploy mode is not configured") {
		t.Errorf("response body missing expected message: %s", rec.Body.String())
	}
}

// TestHandleDeployApply_ViewerForbidden verifies that a viewer role is rejected
// with 403 from the apply endpoint.
func TestHandleDeployApply_ViewerForbidden(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	token := loginAs(t, app, "viewer", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

// TestHandleDeployApply_Success verifies that a successful apply returns 200
// with status "applied" and a non-zero deployment ID.
func TestHandleDeployApply_Success(t *testing.T) {
	app, store := newTestAppWithDeploy(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp applyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if resp.ID == 0 {
		t.Error("deployment id must be non-zero")
	}
	if resp.Status != "applied" {
		t.Errorf("status = %q, want %q", resp.Status, "applied")
	}
}

// --- List deployments endpoint tests ---

// TestHandleListDeployments_EmptyArray verifies that GET /api/v1/deployments
// returns an empty JSON array (not null) when no deployments exist.
func TestHandleListDeployments_EmptyArray(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	token := loginAs(t, app, "viewer", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/deployments", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp map[string]json.RawMessage
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	deploymentsRaw, ok := resp["deployments"]
	if !ok {
		t.Fatal("response missing 'deployments' key")
	}
	// Must be JSON array, not null.
	if string(deploymentsRaw) == "null" {
		t.Error("deployments must be [] not null when empty")
	}
	if !strings.HasPrefix(string(deploymentsRaw), "[") {
		t.Errorf("deployments must be a JSON array, got: %s", deploymentsRaw)
	}
}

// TestHandleListDeployments_Paginated verifies that limit and offset query
// parameters are respected and pagination fields are echoed in the response.
func TestHandleListDeployments_Paginated(t *testing.T) {
	app, store := newTestAppWithDeploy(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	// Create three deployments via the apply endpoint.
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("apply %d: status = %d, want 200, body = %s", i, rec.Code, rec.Body.String())
		}
	}

	// Request with limit=2 offset=0.
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/deployments?limit=2&offset=0", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Deployments []json.RawMessage `json:"deployments"`
		Limit       int               `json:"limit"`
		Offset      int               `json:"offset"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(resp.Deployments) != 2 {
		t.Errorf("deployments count = %d, want 2", len(resp.Deployments))
	}
	if resp.Limit != 2 {
		t.Errorf("limit = %d, want 2", resp.Limit)
	}
	if resp.Offset != 0 {
		t.Errorf("offset = %d, want 0", resp.Offset)
	}
}

// TestHandleListDeployments_LimitCappedAt100 verifies that limit > 100 is
// silently capped to 100 in the response.
func TestHandleListDeployments_LimitCappedAt100(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	token := loginAs(t, app, "viewer", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/deployments?limit=500", "", token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Limit int `json:"limit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if resp.Limit != 100 {
		t.Errorf("limit = %d, want 100 (capped)", resp.Limit)
	}
}

// TestHandleListDeployments_Unauthenticated verifies that unauthenticated
// requests to list are rejected with 401.
func TestHandleListDeployments_Unauthenticated(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodGet, "/api/v1/deployments", "", "")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
