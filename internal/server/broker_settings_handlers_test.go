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

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/diagnostics"
	"github.com/fgjcarlos/mcm/internal/mosquitto"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
)

// TestHandleBrokerSettingsGet covers GET /api/v1/broker/settings.
func TestHandleBrokerSettingsGet(t *testing.T) {
	t.Run("returns the settings and the form description", func(t *testing.T) {
		confPath := filepath.Join(t.TempDir(), "mosquitto.conf")
		cat := loadTestCatalog(t)
		svc := &fakeBrokerConfigService{settingsResult: deploy.BrokerSettingsResult{
			Path:    confPath,
			Hash:    "deadbeef",
			Version: "2.0",
			Document: settings.Document{Entries: []settings.Entry{
				{Directive: "persistence", Values: []string{"true"}},
			}},
			Sections: []settings.Section{{
				ID:    catalog.SectionPersistence,
				Title: "Persistence and autosave",
				Fields: []settings.Field{{
					Directive: "persistence",
					Section:   catalog.SectionPersistence,
					Type:      catalog.TypeBoolean,
					Present:   true,
					Values:    []string{"true"},
				}},
			}},
		}}
		app, store := newAppWithBroker(t, svc, cat, confPath)
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/settings", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp deploy.BrokerSettingsResult
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Path != confPath {
			t.Errorf("path = %q, want %q", resp.Path, confPath)
		}
		if resp.Version != "2.0" {
			t.Errorf("version = %q, want 2.0", resp.Version)
		}
		if len(resp.Sections) != 1 || resp.Sections[0].ID != catalog.SectionPersistence {
			t.Errorf("sections = %+v, want one persistence section", resp.Sections)
		}
		if len(resp.Document.Entries) != 1 {
			t.Errorf("document = %+v, want one entry", resp.Document)
		}
	})

	t.Run("returns 503 when the broker settings are disabled", func(t *testing.T) {
		svc := &fakeBrokerConfigService{settingsErr: deploy.ErrBrokerConfigDisabled}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/settings", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 401 without auth", func(t *testing.T) {
		app, store := newAppWithBroker(t, &fakeBrokerConfigService{}, nil, "")
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/broker/settings", "", "")
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", rec.Code)
		}
	})
}

// TestHandleBrokerSettingsPreview covers POST /api/v1/broker/settings/preview.
func TestHandleBrokerSettingsPreview(t *testing.T) {
	t.Run("returns the preview of the edited settings", func(t *testing.T) {
		svc := &fakeBrokerConfigService{settingsPreview: deploy.PreviewResult{
			RevisionID:        "rev-1",
			Kind:              "broker_settings",
			ConfDiff:          "-persistence true\n+persistence false\n",
			ReloadKind:        "reload",
			ChangedDirectives: []string{"persistence"},
		}}
		app, store := newAppWithBroker(t, svc, loadTestCatalog(t), "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		body := `{"entries":[{"directive":"persistence","values":["false"]}]}`
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", body, token)
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
		if len(resp.ChangedDirectives) != 1 || resp.ChangedDirectives[0] != "persistence" {
			t.Errorf("changed_directives = %v, want [persistence]", resp.ChangedDirectives)
		}
		// The handler must hand the document to the service untouched.
		if len(svc.settingsDoc.Entries) != 1 {
			t.Fatalf("the service received %+v, want one entry", svc.settingsDoc)
		}
		if svc.settingsDoc.Entries[0].Directive != "persistence" {
			t.Errorf("directive = %q, want persistence", svc.settingsDoc.Entries[0].Directive)
		}
	})

	t.Run("returns 400 with the issues of an invalid document", func(t *testing.T) {
		svc := &fakeBrokerConfigService{settingsPreviewErr: &deploy.SettingsError{
			Issues: []settings.Issue{{
				Kind:      "range",
				Directive: "max_inflight_messages",
				Message:   "\"max_inflight_messages\" must be 65535 messages or less",
			}},
		}}
		app, store := newAppWithBroker(t, svc, loadTestCatalog(t), "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		body := `{"entries":[{"directive":"max_inflight_messages","values":["70000"]}]}`
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", body, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Error  string           `json:"error"`
			Issues []settings.Issue `json:"issues"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(resp.Issues) != 1 || resp.Issues[0].Directive != "max_inflight_messages" {
			t.Errorf("issues = %+v, want one issue for max_inflight_messages", resp.Issues)
		}
	})

	t.Run("returns 400 for a malformed body", func(t *testing.T) {
		app, store := newAppWithBroker(t, &fakeBrokerConfigService{}, loadTestCatalog(t), "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", "not json", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("returns 503 when the broker settings are disabled", func(t *testing.T) {
		svc := &fakeBrokerConfigService{settingsPreviewErr: deploy.ErrBrokerConfigDisabled}
		app, store := newAppWithBroker(t, svc, nil, "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", `{"entries":[]}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("refuses an auditor", func(t *testing.T) {
		app, store := newAppWithBroker(t, &fakeBrokerConfigService{}, loadTestCatalog(t), "")
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "admin", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", `{"entries":[]}`, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403, body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestHandleBrokerSettingsPreview_RealService exercises the handler with
// the real deploy service so the catalog checks run end to end.
func TestHandleBrokerSettingsPreview_RealService(t *testing.T) {
	confPath := filepath.Join(t.TempDir(), "mosquitto.conf")
	if err := os.WriteFile(confPath, []byte("persistence true\nlistener 1883 0.0.0.0\n"), 0o644); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	cat := loadTestCatalog(t)
	svc := deploy.NewService(
		mosquitto.FileApplier{},
		store.ACLStore(),
		store,
		store,
		diagnostics.VerifierFunc(diagnostics.VerifyActive),
		app,
		config.MosquittoConfig{Host: "localhost", Port: 1883},
		config.DeployConfig{Mode: "file"},
		func(context.Context, string, string, string, string, string, []byte) {},
	)
	svc.WithBrokerConfig(confPath, cat)
	app.brokerCfgSvc = svc
	app.brokerCfgCatalog = cat
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleOperator)
	token := loginAs(t, app, "admin", "secret")

	t.Run("refuses a value outside the catalog range", func(t *testing.T) {
		body := `{"entries":[{"directive":"max_packet_size","values":["10"]}]}`
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", body, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "max_packet_size") {
			t.Errorf("body does not mention the directive: %s", rec.Body.String())
		}
	})

	t.Run("previews a valid change", func(t *testing.T) {
		body := `{"entries":[{"directive":"persistence","values":["false"]}]}`
		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/broker/settings/preview", body, token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
		}
		var resp deploy.PreviewResult
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.RevisionID == "" {
			t.Error("the preview carries no revision id")
		}
		if len(resp.ChangedDirectives) != 1 || resp.ChangedDirectives[0] != "persistence" {
			t.Errorf("changed_directives = %v, want [persistence]", resp.ChangedDirectives)
		}
		if resp.RequiresRestart {
			t.Error("requires_restart = true, want false for a reload-only change")
		}
	})
}
