package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// --- deploy test fakes ---

type fakeDeployACLStore struct {
	rules []acl.Rule
}

func (f *fakeDeployACLStore) ListRules(_ context.Context) ([]acl.Rule, error) {
	return f.rules, nil
}

func (f *fakeDeployACLStore) ListRulesPaged(_ context.Context, _, _ string, _, _ int) ([]acl.Rule, int, error) {
	return f.rules, len(f.rules), nil
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

func (f *fakeDeployStore) ListDeploymentsPage(_ context.Context, status string, limit, offset int) ([]storage.Deployment, int, error) {
	matches := make([]storage.Deployment, 0, len(f.deployments))
	for _, d := range f.deployments {
		if status == "" || d.Status == status {
			matches = append(matches, *d)
		}
	}
	total := len(matches)
	start := min(offset, total)
	return matches[start:min(start+limit, total)], total, nil
}

type fakeDeployApplier struct {
	failAll bool
}

func (f *fakeDeployApplier) Apply(_ context.Context, _ mosquitto.ApplyRequest) error {
	if f.failAll {
		return errors.New("applier: write failed")
	}
	return nil
}

func (f *fakeDeployApplier) ReloadBrokerOnly() error { return nil }

func okDeployVerifier(_ context.Context, _ diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	return diagnostics.VerifyActiveResult{OK: true, Stage: "ok", Message: "ok"}
}

func failDeployVerifier(_ context.Context, _ diagnostics.VerifyActiveOptions) diagnostics.VerifyActiveResult {
	return diagnostics.VerifyActiveResult{OK: false, Stage: "positive", Message: "verifier rejected"}
}

// fakeDeployPasswordLookup is the empty CleartextPasswordLookup used by
// the server-package deploy handler tests. The tests use empty ACL /
// user fixtures, so the verifier returns "no test subject" and the
// apply fails to "rolled_back" — which is what these tests assert.
type fakeDeployPasswordLookup struct{}

func (fakeDeployPasswordLookup) CleartextPassword(string) (string, bool) { return "", false }

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
		diagnostics.VerifierFunc(okDeployVerifier),
		&fakeDeployPasswordLookup{},
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

func previewRevisionForTest(t *testing.T, app *App, token string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if rec.Header().Get("Location") == "" {
		t.Error("preview response must include a Location header")
	}
	var response deploy.PreviewResult
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if response.RevisionID == "" {
		t.Fatal("preview response did not include revision_id")
	}
	return response.RevisionID
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
		req := authedRequest(http.MethodGet, "/api/v1/deployments?status=failed&limit=5&offset=5", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
		var resp errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Error != "deploy is not configured" {
			t.Errorf("error = %q, want %q", resp.Error, "deploy is not configured")
		}
	})
}

// recordingDeployService is a deployServicer that records the listing
// arguments and answers with a fixed page and total.
type recordingDeployService struct {
	status        string
	limit, offset int
	page          []storage.Deployment
	total         int
}

func (r *recordingDeployService) Preview(_ context.Context, _ string) (deploy.PreviewResult, error) {
	return deploy.PreviewResult{}, nil
}

func (r *recordingDeployService) Apply(_ context.Context, _, _ string) (storage.Deployment, error) {
	return storage.Deployment{}, nil
}

func (r *recordingDeployService) List(_ context.Context, status string, limit, offset int) ([]storage.Deployment, int, error) {
	r.status, r.limit, r.offset = status, limit, offset
	return r.page, r.total, nil
}

type deploymentsPage struct {
	Deployments []storage.Deployment `json:"deployments"`
	Total       int                  `json:"total"`
	Limit       int                  `json:"limit"`
	Offset      int                  `json:"offset"`
}

func getDeploymentsPage(t *testing.T, app *App, token, query string) deploymentsPage {
	t.Helper()
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/deployments"+query, "", token))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d, body = %s", query, rec.Code, http.StatusOK, rec.Body.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, key := range []string{"deployments", "total", "limit", "offset"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("GET %s response is missing key %q: %s", query, key, rec.Body.String())
		}
	}
	var page deploymentsPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	return page
}

// TestHandleDeployListPageParams pins the S10b/S10c query contract: status is
// forwarded verbatim, limit defaults to 50 and is clamped to 100, offset
// defaults to 0 and a negative one becomes 0, garbage never becomes 0 nor
// fails the request, and the applied values are echoed.
func TestHandleDeployListPageParams(t *testing.T) {
	cases := []struct {
		query      string
		wantStatus string
		wantLimit  int
		wantOffset int
	}{
		{query: "", wantLimit: 50, wantOffset: 0},
		{query: "?limit=500", wantLimit: 100, wantOffset: 0},
		{query: "?limit=100", wantLimit: 100, wantOffset: 0},
		{query: "?limit=7", wantLimit: 7, wantOffset: 0},
		{query: "?limit=abc", wantLimit: 50, wantOffset: 0},
		{query: "?limit=0", wantLimit: 50, wantOffset: 0},
		{query: "?limit=-4", wantLimit: 50, wantOffset: 0},
		{query: "?offset=20", wantLimit: 50, wantOffset: 20},
		{query: "?offset=-3", wantLimit: 50, wantOffset: 0},
		{query: "?offset=xyz", wantLimit: 50, wantOffset: 0},
		{query: "?status=failed&limit=2&offset=4", wantStatus: "failed", wantLimit: 2, wantOffset: 4},
	}
	for _, tc := range cases {
		t.Run("query "+tc.query, func(t *testing.T) {
			app, store := newTestApp(t)
			t.Cleanup(func() { _ = store.Close() })
			svc := &recordingDeployService{page: []storage.Deployment{{ID: 9, Status: "failed"}}, total: 12}
			app.deploySvc = svc
			seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
			token := loginAs(t, app, "admin", "secret")

			page := getDeploymentsPage(t, app, token, tc.query)

			if svc.status != tc.wantStatus || svc.limit != tc.wantLimit || svc.offset != tc.wantOffset {
				t.Errorf("service got status=%q limit=%d offset=%d, want status=%q limit=%d offset=%d",
					svc.status, svc.limit, svc.offset, tc.wantStatus, tc.wantLimit, tc.wantOffset)
			}
			if page.Limit != tc.wantLimit || page.Offset != tc.wantOffset {
				t.Errorf("echoed limit=%d offset=%d, want limit=%d offset=%d", page.Limit, page.Offset, tc.wantLimit, tc.wantOffset)
			}
			if page.Total != 12 || len(page.Deployments) != 1 || page.Deployments[0].ID != 9 {
				t.Errorf("page = %+v, want the service page with total 12", page)
			}
		})
	}
}

// TestHandleDeployListFiltersAndPagesStoredDeployments drives the endpoint
// over the real SQLite store: the status filter, the full total, the next
// slice for a non-zero offset and paging forward and back without skipping
// or repeating a row (S7, S8, S10).
func TestHandleDeployListFiltersAndPagesStoredDeployments(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	dir := t.TempDir()
	app.deploySvc = deploy.NewService(
		&fakeDeployApplier{},
		&fakeDeployACLStore{},
		&fakeDeployMQTTStore{},
		store,
		diagnostics.VerifierFunc(okDeployVerifier),
		&fakeDeployPasswordLookup{},
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		config.DeployConfig{Mode: "file", ACLPath: dir + "/acl", PasswdPath: dir + "/passwd"},
		func(_ context.Context, _, _, _, _, _ string, _ []byte) {},
	)
	ctx := context.Background()
	var failedIDs []int64
	for i := 0; i < 7; i++ {
		status := "failed"
		if i%3 == 1 {
			status = "active_verified"
		}
		d := &storage.Deployment{Actor: "operator", Status: status}
		if err := store.InsertDeployment(ctx, d); err != nil {
			t.Fatalf("InsertDeployment %d: %v", i, err)
		}
		if status == "failed" {
			failedIDs = append(failedIDs, d.ID)
		}
	}
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	all := getDeploymentsPage(t, app, token, "")
	if all.Total != 7 || len(all.Deployments) != 7 {
		t.Errorf("no filter: got %d rows total %d, want 7 and 7", len(all.Deployments), all.Total)
	}

	first := getDeploymentsPage(t, app, token, "?status=failed&limit=2")
	if first.Total != len(failedIDs) {
		t.Errorf("total = %d, want %d", first.Total, len(failedIDs))
	}
	if len(first.Deployments) != 2 || first.Total <= len(first.Deployments) {
		t.Errorf("page size = %d with total %d, want 2 rows and a larger total", len(first.Deployments), first.Total)
	}

	ids := func(p deploymentsPage) []int64 {
		out := make([]int64, 0, len(p.Deployments))
		for _, d := range p.Deployments {
			if d.Status != "failed" {
				t.Errorf("status filter leaked row %d with status %q", d.ID, d.Status)
			}
			out = append(out, d.ID)
		}
		return out
	}
	var forward []int64
	for offset := 0; offset < first.Total; offset += 2 {
		forward = append(forward, ids(getDeploymentsPage(t, app, token, fmt.Sprintf("?status=failed&limit=2&offset=%d", offset)))...)
	}
	var backward []int64
	for offset := ((first.Total - 1) / 2) * 2; offset >= 0; offset -= 2 {
		backward = append(ids(getDeploymentsPage(t, app, token, fmt.Sprintf("?status=failed&limit=2&offset=%d", offset))), backward...)
	}
	if len(forward) != len(failedIDs) {
		t.Errorf("forward pages returned %d rows, want %d: %v", len(forward), len(failedIDs), forward)
	}
	seen := make(map[int64]bool, len(forward))
	for _, id := range forward {
		if seen[id] {
			t.Errorf("row %d repeated across pages: %v", id, forward)
		}
		seen[id] = true
	}
	for _, id := range failedIDs {
		if !seen[id] {
			t.Errorf("row %d skipped across pages: %v", id, forward)
		}
	}
	if fmt.Sprint(backward) != fmt.Sprint(forward) {
		t.Errorf("backward pages = %v, want %v", backward, forward)
	}

	negative := getDeploymentsPage(t, app, token, "?status=failed&limit=2&offset=-5")
	if negative.Offset != 0 || fmt.Sprint(ids(negative)) != fmt.Sprint(ids(first)) {
		t.Errorf("negative offset page = %+v, want the first page", negative)
	}
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
	t.Run("returns 400 without a preview revision", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", "", token)
		withRecentMFATest(t, app, req)
		if req.Method == http.MethodPost && (req.URL.Path == "/api/v1/broker/config/apply" || req.URL.Path == "/api/v1/deployments/apply") && req.Header.Get("Authorization") != "" {
			withRecentMFATest(t, app, req)
		}
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})

	t.Run("success returns 200 with deployment", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")
		revisionID := previewRevisionForTest(t, app, token)

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", `{"revision_id":"`+revisionID+`"}`, token)
		withRecentMFATest(t, app, req)
		if req.Method == http.MethodPost && (req.URL.Path == "/api/v1/broker/config/apply" || req.URL.Path == "/api/v1/deployments/apply") && req.Header.Get("Authorization") != "" {
			withRecentMFATest(t, app, req)
		}
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
		withRecentMFATest(t, app, req)
		if req.Method == http.MethodPost && (req.URL.Path == "/api/v1/broker/config/apply" || req.URL.Path == "/api/v1/deployments/apply") && req.Header.Get("Authorization") != "" {
			withRecentMFATest(t, app, req)
		}
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
		withRecentMFATest(t, app, req)
		if req.Method == http.MethodPost && (req.URL.Path == "/api/v1/broker/config/apply" || req.URL.Path == "/api/v1/deployments/apply") && req.Header.Get("Authorization") != "" {
			withRecentMFATest(t, app, req)
		}
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
			diagnostics.VerifierFunc(failDeployVerifier),
			&fakeDeployPasswordLookup{},
			config.MosquittoConfig{Host: "localhost", Port: 1883},
			config.DeployConfig{Mode: "file", ACLPath: dir + "/acl", PasswdPath: dir + "/passwd"},
			func(_ context.Context, _, _, _, _, _ string, _ []byte) {},
		)
		revisionID := previewRevisionForTest(t, app, token)

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", `{"revision_id":"`+revisionID+`"}`, token)
		withRecentMFATest(t, app, req)
		if req.Method == http.MethodPost && (req.URL.Path == "/api/v1/broker/config/apply" || req.URL.Path == "/api/v1/deployments/apply") && req.Header.Get("Authorization") != "" {
			withRecentMFATest(t, app, req)
		}
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

	t.Run("returns 409 with the blocking entries when the ACL content is unmanaged", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")
		app.deploySvc = &unmanagedACLDeployService{}

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", `{"revision_id":"rev-1"}`, token)
		withRecentMFATest(t, app, req)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
		}
		var resp errorResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Error == "" {
			t.Error("the conflict response must explain the refusal")
		}
		if len(resp.Details) != 1 || resp.Details[0] != "unknown | acl_file /etc/mosquitto/extra.acl" {
			t.Fatalf("details = %v, want the blocking ACL entry", resp.Details)
		}
	})

	t.Run("returns 409 when in-progress", func(t *testing.T) {
		app, store, _ := newTestAppWithDeploy(t, true)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
		token := loginAs(t, app, "admin", "secret")
		app.deploySvc = &lockedDeployService{}

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/deployments/apply", `{"revision_id":"locked"}`, token)
		withRecentMFATest(t, app, req)
		if req.Method == http.MethodPost && (req.URL.Path == "/api/v1/broker/config/apply" || req.URL.Path == "/api/v1/deployments/apply") && req.Header.Get("Authorization") != "" {
			withRecentMFATest(t, app, req)
		}
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

// unmanagedACLDeployService implements deployServicer and always refuses the
// apply because the rendered ACL would drop ACL content MCM does not manage.
type unmanagedACLDeployService struct{}

func (u *unmanagedACLDeployService) Preview(_ context.Context, _ string) (deploy.PreviewResult, error) {
	return deploy.PreviewResult{}, nil
}

func (u *unmanagedACLDeployService) Apply(_ context.Context, _, _ string) (storage.Deployment, error) {
	return storage.Deployment{}, &deploy.UnmanagedACLContentError{
		Entries: []string{"unknown | acl_file /etc/mosquitto/extra.acl"},
	}
}

func (u *unmanagedACLDeployService) List(_ context.Context, _ string, _, _ int) ([]storage.Deployment, int, error) {
	return nil, 0, nil
}

// lockedDeployService implements deployServicer and always returns ErrDeployInProgress.
type lockedDeployService struct{}

func (l *lockedDeployService) Preview(_ context.Context, _ string) (deploy.PreviewResult, error) {
	return deploy.PreviewResult{}, nil
}

func (l *lockedDeployService) Apply(_ context.Context, _, _ string) (storage.Deployment, error) {
	return storage.Deployment{}, deploy.ErrDeployInProgress
}

func (l *lockedDeployService) List(_ context.Context, _ string, _, _ int) ([]storage.Deployment, int, error) {
	return nil, 0, nil
}

// shadowedMQTTStore serves a stored MQTT user that shares the configured
// broker service username.
type shadowedMQTTStore struct {
	users []storage.MQTTUser
}

func (f *shadowedMQTTStore) ListMQTTUsers(_ context.Context) ([]storage.MQTTUser, error) {
	return f.users, nil
}

// TestHandleDeployPreviewNamesShadowedServiceAccount pins the issue #297
// remedy: a preview blocked by a stored user shadowing the service account
// must answer 409 naming that user and the one action the protection rules
// allow — renaming it — instead of an opaque 500 duplicate-entry error.
func TestHandleDeployPreviewNamesShadowedServiceAccount(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	dir := t.TempDir()
	for _, name := range []string{"acl", "passwd"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	svc := deploy.NewService(
		&fakeDeployApplier{},
		&fakeDeployACLStore{},
		&shadowedMQTTStore{users: []storage.MQTTUser{{Username: "svc-mcm", PasswordHash: "$7$101$c2FsdA$a2V5"}}},
		newFakeDeployStore(),
		diagnostics.VerifierFunc(okDeployVerifier),
		&fakeDeployPasswordLookup{},
		config.MosquittoConfig{Host: "localhost", Port: 1883, Username: "svc-mcm", Password: "service-password"},
		config.DeployConfig{Mode: "file", ACLPath: filepath.Join(dir, "acl"), PasswdPath: filepath.Join(dir, "passwd")},
		func(_ context.Context, _, _, _, _, _ string, _ []byte) {},
	)
	app.deploySvc = svc
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/deployments/preview", "", token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "svc-mcm") || !strings.Contains(body, "rename") {
		t.Fatalf("body must name the shadowing user and the remedy: %s", body)
	}
}
