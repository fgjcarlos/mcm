package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/listener"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
	"github.com/fgjcarlos/mcm/internal/storage"
)

func listenerSpecsJSON() string {
	return `[{"id":"mqtt","port":1883,"bind":"0.0.0.0","protocols":["mqtt"]},{"id":"websocket","port":9001,"bind":"127.0.0.1","protocols":["websockets"]}]`
}

func listenerPreview(t *testing.T, app *App, token string) listener.ListenerPreviewResult {
	t.Helper()
	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/listeners/preview", `{"specs":`+listenerSpecsJSON()+`,"confirm":true}`, token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var result listener.ListenerPreviewResult
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if result.RevisionID == "" {
		t.Fatal("preview response missing revision_id")
	}
	return result
}

func TestHandleListenerList_OK(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	if err := store.ReplaceAllListenerSpecs(context.Background(), []storage.ListenerSpecRow{{ID: "mqtt", Port: 1883, Bind: "0.0.0.0", Protocols: []string{"mqtt"}}}); err != nil {
		t.Fatalf("seed listener specs: %v", err)
	}

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/listeners", "", loginAs(t, app, "viewer", "secret")))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		Specs []storage.ListenerSpecRow `json:"specs"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Specs) != 1 || response.Specs[0].ID != "mqtt" {
		t.Fatalf("specs = %#v, want persisted mqtt listener", response.Specs)
	}
}

func TestHandleListenerList_RejectsAnonymous(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/listeners", "", ""))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestHandleListenerPreview_OK(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	result := listenerPreview(t, app, loginAs(t, app, "operator", "secret"))
	if !result.NeedsRestart || result.Diff == "" {
		t.Fatalf("preview = %#v, want restart-required diff", result)
	}
}

func TestHandleListenerPreview_AdminAllowed(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	_ = listenerPreview(t, app, loginAs(t, app, "admin", "secret"))
}

func TestHandleListenerPreview_RejectsViewer(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/preview", `{"specs":`+listenerSpecsJSON()+`}`, loginAs(t, app, "viewer", "secret")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestHandleListenerPreview_DuplicateRejected(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	body := `{"specs":[{"id":"one","port":1883,"bind":"0.0.0.0","protocols":["mqtt"]},{"id":"two","port":1883,"bind":"0.0.0.0","protocols":["mqtt"]}]}`
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/preview", body, loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleListenerApply_OK(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	preview := listenerPreview(t, app, token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/apply", `{"revision_id":"`+preview.RevisionID+`","confirm":true}`, token))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response struct {
		RevisionID string `json:"revision_id"`
		Applied    bool   `json:"applied"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.RevisionID != preview.RevisionID || !response.Applied {
		t.Fatalf("apply response = %#v", response)
	}
}

func TestHandleListenerApply_RejectsOperator(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/apply", `{"revision_id":"any","confirm":true}`, loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestHandleListenerApply_NotConfirmed(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	preview := listenerPreview(t, app, token)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/apply", `{"revision_id":"`+preview.RevisionID+`","confirm":false}`, token))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleListenerApply_Expired(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	clock := time.Now().UTC().Add(-2 * time.Hour)
	app.listenerSvc = listener.NewListenerService(store, nil, &listener.NoopRestartRunner{}, nil, listener.WithListenerClock(func() time.Time { return clock }))
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	preview := listenerPreview(t, app, token)
	clock = clock.Add(2 * time.Hour)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/apply", `{"revision_id":"`+preview.RevisionID+`","confirm":true}`, token))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
	}
}

func TestHandleListenerApply_BadRevision(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/listeners/apply", `{"revision_id":"missing","confirm":true}`, loginAs(t, app, "admin", "secret")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestListenerRoutesAreRegistered(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/listeners", "", loginAs(t, app, "viewer", "secret")))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/listeners status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

type failingListenerService struct{ err error }

func (s failingListenerService) List(context.Context) ([]storage.ListenerSpecRow, error) {
	return nil, s.err
}
func (s failingListenerService) Preview(context.Context, []listeners.ListenerSpec, listener.PreviewOptions) (listener.ListenerPreviewResult, error) {
	return listener.ListenerPreviewResult{}, s.err
}
func (s failingListenerService) Apply(context.Context, string, bool) error { return s.err }

func TestHandleListenerApply_ErrorMapping(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "missing preview", err: listener.ErrListenerPreviewNotFound, want: http.StatusNotFound},
		{name: "expired preview", err: listener.ErrListenerPreviewExpired, want: http.StatusConflict},
		{name: "consumed preview", err: listener.ErrListenerPreviewConsumed, want: http.StatusConflict},
		{name: "confirmation required", err: listener.ErrListenerRestartNotConfirmed, want: http.StatusConflict},
		{name: "unmapped compose port", err: listener.ErrListenerComposeUnmapped, want: http.StatusConflict},
		{name: "restart failed", err: errors.Join(listener.ErrListenerRestartFailed, errors.New("docker unavailable")), want: http.StatusBadGateway},
		{name: "restart configuration missing", err: listener.ErrListenerRestartConfigMissing, want: http.StatusInternalServerError},
		{name: "apply in progress", err: listener.ErrListenerApplyInProgress, want: http.StatusConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/listeners/apply", nil)
			(&listenerAPI{svc: failingListenerService{err: test.err}}).handleApply(rec, request)
			if rec.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, test.want, rec.Body.String())
			}
		})
	}
}
