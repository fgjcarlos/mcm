package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
)

// uploadAndActivateCertificate stores a material and activates it.
func uploadAndActivateCertificate(t *testing.T, app *App, operatorToken string, adminToken string, profile string, material certificateMaterial) string {
	t.Helper()

	upload := httptest.NewRecorder()
	app.Handler().ServeHTTP(upload, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/"+profile+"/revisions",
		material.uploadBody(t, profile),
		operatorToken))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body = %s", upload.Code, http.StatusCreated, upload.Body.String())
	}
	var revision struct {
		RevisionID string `json:"revision_id"`
	}
	if err := json.NewDecoder(upload.Body).Decode(&revision); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	apply := httptest.NewRecorder()
	app.Handler().ServeHTTP(apply, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/apply",
		`{"profile":"`+profile+`","revision_id":"`+revision.RevisionID+`"}`,
		adminToken))
	if apply.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want %d, body = %s", apply.Code, http.StatusOK, apply.Body.String())
	}
	return revision.RevisionID
}

func TestListenerPreviewBindsACertificateProfile(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	uploadAndActivateCertificate(t, app, loginAs(t, app, "operator", "secret"), loginAs(t, app, "admin", "secret"), "production", material)

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/listeners/preview",
		`{"specs":[{"id":"tls","port":8883,"bind":"0.0.0.0","protocols":["mqtt"],"tls_profile":"production"}],"confirm":true}`,
		loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"certfile /mosquitto/config/certs/production/active/server.crt",
		"keyfile /mosquitto/config/certs/production/active/server.key",
		"cafile /mosquitto/config/certs/production/active/ca.crt",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("the preview response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "PRIVATE KEY") {
		t.Fatalf("the preview response leaked private key material: %s", body)
	}
}

func TestListenerPreviewRefusesAnUnknownCertificateProfile(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/listeners/preview",
		`{"specs":[{"id":"tls","port":8883,"bind":"0.0.0.0","protocols":["mqtt"],"tls_profile":"missing"}],"confirm":true}`,
		loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("preview status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tls_profile_unavailable") {
		t.Fatalf("the preview response missing the unavailable profile issue: %s", rec.Body.String())
	}
}

func TestListenerListExposesTheCertificateProfile(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	uploadAndActivateCertificate(t, app, loginAs(t, app, "operator", "secret"), loginAs(t, app, "admin", "secret"), "production", material)

	preview := httptest.NewRecorder()
	app.Handler().ServeHTTP(preview, authedRequest(http.MethodPost,
		"/api/v1/listeners/preview",
		`{"specs":[{"id":"tls","port":8883,"bind":"0.0.0.0","protocols":["mqtt"],"tls_profile":"production"}],"confirm":true}`,
		loginAs(t, app, "operator", "secret")))
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d, body = %s", preview.Code, http.StatusOK, preview.Body.String())
	}
	var previewResult struct {
		RevisionID string `json:"revision_id"`
	}
	if err := json.NewDecoder(preview.Body).Decode(&previewResult); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}

	apply := httptest.NewRecorder()
	app.Handler().ServeHTTP(apply, authedRequest(http.MethodPost,
		"/api/v1/listeners/apply",
		`{"revision_id":"`+previewResult.RevisionID+`","confirm":true}`,
		loginAs(t, app, "admin", "secret")))
	if apply.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want %d, body = %s", apply.Code, http.StatusOK, apply.Body.String())
	}

	list := httptest.NewRecorder()
	app.Handler().ServeHTTP(list, authedRequest(http.MethodGet, "/api/v1/listeners", "", loginAs(t, app, "operator", "secret")))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", list.Code, http.StatusOK, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"tls_profile":"production"`) {
		t.Fatalf("the listener list missing the certificate profile: %s", list.Body.String())
	}
}
