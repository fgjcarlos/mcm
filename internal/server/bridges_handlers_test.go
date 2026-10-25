package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/mosquitto/bridges"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// fakeBridgeService is an in-memory bridgeServicer used by the handler
// tests.
type fakeBridgeService struct {
	listResult  deploy.BridgesResult
	listErr     error
	previewRes  deploy.PreviewResult
	previewErr  error
	applyRes    storage.Deployment
	applyErr    error
	previewDoc  bridges.Document
	applyRevID  string
	applyForce  bool
	applyAdopt  bool
	previewCall int
}

func (f *fakeBridgeService) GetBridges(_ context.Context) (deploy.BridgesResult, error) {
	return f.listResult, f.listErr
}

func (f *fakeBridgeService) PreviewBridges(_ context.Context, _ string, doc bridges.Document) (deploy.PreviewResult, error) {
	f.previewCall++
	f.previewDoc = doc
	return f.previewRes, f.previewErr
}

func (f *fakeBridgeService) ApplyBridges(_ context.Context, _, revisionID string, force, adopt bool) (storage.Deployment, error) {
	f.applyRevID = revisionID
	f.applyForce = force
	f.applyAdopt = adopt
	return f.applyRes, f.applyErr
}

// newAppWithBridges wires a bridge fake into the App's bridgeSvc slot.
func newAppWithBridges(t *testing.T, svc bridgeServicer) (*App, *storage.Store) {
	t.Helper()
	app, store := newTestApp(t)
	app.bridgeSvc = svc
	return app, store
}

// TestHandleBridgeList covers GET /api/v1/broker/bridges.
func TestHandleBridgeList(t *testing.T) {
	t.Run("returns 200 with the bridges", func(t *testing.T) {
		svc := &fakeBridgeService{listResult: deploy.BridgesResult{Bridges: []bridges.Bridge{{
			Name:        "upstream",
			Address:     "remote.example.org:1884",
			PasswordSet: true,
			Topics:      []bridges.TopicRule{{Topic: "both/#", Direction: bridges.DirectionBoth, QoS: 1}},
		}}}}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/bridges", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Bridges []bridges.Bridge `json:"bridges"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Bridges) != 1 {
			t.Fatalf("bridges = %d, want 1", len(resp.Bridges))
		}
		if resp.Bridges[0].Name != "upstream" {
			t.Errorf("name = %q, want upstream", resp.Bridges[0].Name)
		}
		if !resp.Bridges[0].PasswordSet {
			t.Errorf("password_set = false, want true")
		}
		if resp.Bridges[0].Password != "" {
			t.Errorf("the response leaked the password: %q", resp.Bridges[0].Password)
		}
	})

	t.Run("returns 503 when the service is not configured", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/bridges", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("requires at least the auditor role", func(t *testing.T) {
		svc := &fakeBridgeService{}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
		token := loginAs(t, app, "viewer", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/bridges", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestHandleBridgePreview covers POST /api/v1/broker/bridges/preview.
func TestHandleBridgePreview(t *testing.T) {
	t.Run("returns 200 with the preview", func(t *testing.T) {
		svc := &fakeBridgeService{previewRes: deploy.PreviewResult{
			RevisionID:        "revision-1",
			Kind:              "bridges",
			ConfDiff:          "diff",
			ConfBody:          "connection upstream",
			ReloadKind:        "restart",
			RequiresRestart:   true,
			ChangedDirectives: []string{"connection"},
		}}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
		token := loginAs(t, app, "operator", "secret")

		body := `{"bridges":[{"name":"upstream","address":"remote.example.org:1884","topics":[{"topic":"both/#","direction":"both","qos":1}]}]}`
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/preview", body, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if len(svc.previewDoc.Bridges) != 1 {
			t.Fatalf("the handler sent %d bridges, want 1", len(svc.previewDoc.Bridges))
		}
		if svc.previewDoc.Bridges[0].Name != "upstream" {
			t.Errorf("name = %q, want upstream", svc.previewDoc.Bridges[0].Name)
		}
		if len(svc.previewDoc.Bridges[0].Topics) != 1 {
			t.Errorf("topics = %d, want 1", len(svc.previewDoc.Bridges[0].Topics))
		}
	})

	t.Run("returns 400 with the issues", func(t *testing.T) {
		svc := &fakeBridgeService{previewErr: &deploy.BridgesError{Issues: []bridges.Issue{{
			Kind:     bridges.IssueKindLoop,
			Severity: bridges.SeverityError,
			Bridge:   "upstream",
			Field:    "address",
			Message:  "the bridge connects to itself",
		}}}}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
		token := loginAs(t, app, "operator", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/preview", `{"bridges":[]}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Error  string          `json:"error"`
			Issues []bridges.Issue `json:"issues"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(resp.Issues))
		}
		if resp.Issues[0].Kind != bridges.IssueKindLoop {
			t.Errorf("kind = %q, want loop", resp.Issues[0].Kind)
		}
	})

	t.Run("requires at least the operator role", func(t *testing.T) {
		svc := &fakeBridgeService{}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "auditor", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "auditor", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/preview", `{"bridges":[]}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestHandleBridgeApply covers POST /api/v1/broker/bridges/apply.
func TestHandleBridgeApply(t *testing.T) {
	t.Run("returns 200 with the deployment", func(t *testing.T) {
		svc := &fakeBridgeService{applyRes: storage.Deployment{ID: 7, Status: "active_verified", Kind: "bridges"}}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/apply", `{"revision_id":"revision-1","force":true}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		if svc.applyRevID != "revision-1" {
			t.Errorf("revision_id = %q, want revision-1", svc.applyRevID)
		}
		if !svc.applyForce {
			t.Error("force = false, want true")
		}
	})

	t.Run("returns 400 without a revision id", func(t *testing.T) {
		svc := &fakeBridgeService{}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/apply", `{}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 409 when the change needs a restart", func(t *testing.T) {
		svc := &fakeBridgeService{applyErr: deploy.ErrRequiresRestart}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/apply", `{"revision_id":"revision-1"}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("requires the admin role", func(t *testing.T) {
		svc := &fakeBridgeService{}
		app, store := newAppWithBridges(t, svc)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
		token := loginAs(t, app, "operator", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/bridges/apply", `{"revision_id":"revision-1"}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})
}
