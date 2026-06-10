package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// --- deploy test fakes ---

type fakeDeployACLStore struct {
	rules []acl.Rule
}

func (f *fakeDeployACLStore) ListRules(_ context.Context) ([]acl.Rule, error) {
	return f.rules, nil
}

func (f *fakeDeployACLStore) CreateRule(_ context.Context, rule acl.Rule) (acl.Rule, error) {
	return rule, nil
}

func (f *fakeDeployACLStore) UpdateRule(_ context.Context, _ string, rule acl.Rule) (acl.Rule, error) {
	return rule, nil
}

func (f *fakeDeployACLStore) DeleteRule(_ context.Context, _ string) error {
	return nil
}

type fakeDeployMQTTStore struct{}

func (f *fakeDeployMQTTStore) ListMQTTUsers(_ context.Context) ([]storage.MQTTUser, error) {
	return nil, nil
}

type fakeDeployStore struct {
	deployments map[int64]*storage.Deployment
	nextID      int64
}

func newFakeDeployStore() *fakeDeployStore {
	return &fakeDeployStore{
		deployments: make(map[int64]*storage.Deployment),
		nextID:      1,
	}
}

func (f *fakeDeployStore) InsertDeployment(_ context.Context, d *storage.Deployment) error {
	d.ID = f.nextID
	f.nextID++
	clone := *d
	f.deployments[d.ID] = &clone
	return nil
}

func (f *fakeDeployStore) GetDeployment(_ context.Context, id int64) (storage.Deployment, error) {
	d, ok := f.deployments[id]
	if !ok {
		return storage.Deployment{}, storage.ErrDeploymentNotFound
	}
	return *d, nil
}

func (f *fakeDeployStore) UpdateDeploymentStatus(_ context.Context, id int64, status, message string) error {
	d, ok := f.deployments[id]
	if !ok {
		return storage.ErrDeploymentNotFound
	}
	d.Status = status
	d.Message = message
	return nil
}

func (f *fakeDeployStore) ListDeployments(_ context.Context, _, _ int) ([]storage.Deployment, error) {
	result := make([]storage.Deployment, 0, len(f.deployments))
	for _, d := range f.deployments {
		result = append(result, *d)
	}
	return result, nil
}

type fakeDeployApplier struct {
	failAll bool
}

func (f *fakeDeployApplier) Apply(_ context.Context, _, _ string) error {
	if f.failAll {
		return errors.New("applier: write failed")
	}
	return nil
}

func okDeployHealthCheck(_ context.Context, _ config.MosquittoConfig) diagnostics.MQTTResult {
	return diagnostics.MQTTResult{OK: true, Message: "ok"}
}

// newTestDeployService builds a deploy.Service wired with fakes and optionally enabled.
func newTestDeployService(t *testing.T, enabled bool) *deploy.Service {
	t.Helper()
	var deployCfg config.DeployConfig
	if enabled {
		dir := t.TempDir()
		deployCfg = config.DeployConfig{
			Mode:       "file",
			ACLPath:    dir + "/acl",
			PasswdPath: dir + "/passwd",
		}
	}
	return deploy.NewService(
		&fakeDeployApplier{},
		&fakeDeployACLStore{},
		&fakeDeployMQTTStore{},
		newFakeDeployStore(),
		okDeployHealthCheck,
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		deployCfg,
		func(_ context.Context, _, _, _, _, _ string, _ []byte) {},
	)
}

// newTestAppWithDeploy creates an App wired with a deploy.Service.
func newTestAppWithDeploy(t *testing.T, deployEnabled bool) (*App, *storage.Store, *deploy.Service) {
	t.Helper()
	app, store := newTestApp(t)
	svc := newTestDeployService(t, deployEnabled)
	app.deploySvc = svc
	return app, store, svc
}

// --- Tests ---

// TestHandleDeployList covers GET /api/v1/deployments.
func TestHandleDeployList(t *testing.T) {
	t.Run("success returns 200 with deployments array", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/deployments", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp struct {
			Deployments []storage.Deployment `json:"deployments"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Deployments == nil {
			t.Error("deployments field must be present (even if empty)")
		}
	})

	t.Run("returns 401 without auth", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/deployments", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("returns 404 when deploy is disabled", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, false)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/deployments", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})
}

// TestHandleDeployPreview covers POST /api/v1/deployments/preview.
func TestHandleDeployPreview(t *testing.T) {
	t.Run("success returns 200 with diff", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp deploy.PreviewResult
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	})

	t.Run("returns 401 without auth", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("returns 404 when deploy is disabled", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, false)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})
}

// TestHandleDeployApply covers POST /api/v1/deployments/apply.
func TestHandleDeployApply(t *testing.T) {
	t.Run("success returns 200 with deployment", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp storage.Deployment
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Status == "" {
			t.Error("deployment status must be set")
		}
	})

	t.Run("returns 401 without auth", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})

	t.Run("returns 404 when deploy is disabled", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, false)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})

	t.Run("returns rolled_back deployment body when healthcheck fails", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")

		dir := t.TempDir()
		app.deploySvc = deploy.NewService(
			&fakeDeployApplier{},
			&fakeDeployACLStore{},
			&fakeDeployMQTTStore{},
			newFakeDeployStore(),
			func(_ context.Context, _ config.MosquittoConfig) diagnostics.MQTTResult {
				return diagnostics.MQTTResult{OK: false, Message: "broker unreachable"}
			},
			config.MosquittoConfig{Host: "localhost", Port: 1883},
			config.DeployConfig{Mode: "file", ACLPath: dir + "/acl", PasswdPath: dir + "/passwd"},
			func(_ context.Context, _, _, _, _, _ string, _ []byte) {},
		)

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp storage.Deployment
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Status != "rolled_back" {
			t.Fatalf("deployment status = %q, want rolled_back; body = %s", resp.Status, rec.Body.String())
		}
		if resp.Message == "" {
			t.Fatalf("rolled_back response must include a message; body = %s", rec.Body.String())
		}
	})

	t.Run("returns 409 when in-progress", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")
		app.deploySvc = &lockedDeployService{}

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
		}
		var resp errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Error != "deploy already in progress" {
			t.Fatalf("error response = %q, want deploy already in progress", resp.Error)
		}
	})
}

// lockedDeployService implements deployServicer and always returns ErrDeployInProgress.
type lockedDeployService struct{}

func (l *lockedDeployService) Preview(_ context.Context, _ string) (deploy.PreviewResult, error) {
	return deploy.PreviewResult{}, nil
}

func (l *lockedDeployService) Apply(_ context.Context, _ string) (storage.Deployment, error) {
	return storage.Deployment{}, deploy.ErrDeployInProgress
}

func (l *lockedDeployService) List(_ context.Context, _, _ int) ([]storage.Deployment, error) {
	return nil, nil
}
