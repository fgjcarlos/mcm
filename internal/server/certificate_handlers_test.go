package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// certificateMaterial is an uploaded certificate material.
type certificateMaterial struct {
	CACertPEM     string
	ServerCertPEM string
	ServerKeyPEM  string
}

// uploadBody renders the upload request body of a material.
func (m certificateMaterial) uploadBody(t *testing.T, profile string) string {
	t.Helper()
	payload := map[string]any{
		"ca_cert_pem":     m.CACertPEM,
		"server_cert_pem": m.ServerCertPEM,
		"server_key_pem":  m.ServerKeyPEM,
		"expected_names":  []string{"broker.example.com"},
		"description":     "broker certificates",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal upload body: %v", err)
	}
	return string(encoded)
}

// newCertificateMaterial builds a certificate authority and a server certificate.
func newCertificateMaterial(t *testing.T, commonName string, notBefore time.Time, notAfter time.Time) certificateMaterial {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ca key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mcm-test-root"},
		NotBefore:             notBefore.Add(-time.Hour),
		NotAfter:              notAfter.Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create ca certificate: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse ca certificate: %v", err)
	}

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate leaf key: %v", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{commonName},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create leaf certificate: %v", err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("marshal leaf key: %v", err)
	}

	return certificateMaterial{
		CACertPEM:     string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
		ServerCertPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
		ServerKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})),
	}
}

// newCertificateTestApp returns an app with the certificate surface enabled.
func newCertificateTestApp(t *testing.T) (*App, *storage.Store) {
	t.Helper()
	return newTestApp(t, func(cfg *config.Config) {
		cfg.Mosquitto.ConfigDir = t.TempDir()
	})
}

func TestCertificateUploadStoresTheRevisionWithoutLeakingTheKey(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/production/revisions",
		material.uploadBody(t, "production"),
		loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	body := rec.Body.String()
	for _, forbidden := range []string{"PRIVATE KEY", material.ServerKeyPEM} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the upload response leaked private key material: %s", forbidden)
		}
	}

	var revision struct {
		RevisionID   string `json:"revision_id"`
		State        string `json:"state"`
		Subject      string `json:"subject"`
		ExpiryStatus string `json:"expiry_status"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&revision); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}
	if revision.RevisionID == "" || revision.State != "pending" {
		t.Fatalf("upload response = %#v, want a pending revision", revision)
	}
	if revision.ExpiryStatus != "valid" {
		t.Fatalf("upload response expiry status = %q, want valid", revision.ExpiryStatus)
	}
}

func TestCertificateUploadRejectsInvalidMaterial(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-48*time.Hour), time.Now().UTC().Add(-time.Hour))
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/production/revisions",
		material.uploadBody(t, "production"),
		loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("upload status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "issues") {
		t.Fatalf("upload response missing the issues: %s", rec.Body.String())
	}
}

func TestCertificateUploadRequiresOperator(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(time.Hour))
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/production/revisions",
		material.uploadBody(t, "production"),
		loginAs(t, app, "viewer", "secret")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("upload status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCertificateListRequiresAuditor(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "viewer", "secret", auth.RoleViewer)

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/broker/certificates", "", loginAs(t, app, "viewer", "secret")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("list status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCertificateApplyRequiresAdmin(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/apply",
		`{"profile":"production","revision_id":"rev-1"}`,
		loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("apply status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestCertificateListReturnsTheStoredProfile(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	seedAdminUserWithRole(t, store, "auditor", "secret", auth.RoleAuditor)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	upload := httptest.NewRecorder()
	app.Handler().ServeHTTP(upload, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/production/revisions",
		material.uploadBody(t, "production"),
		loginAs(t, app, "operator", "secret")))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body = %s", upload.Code, http.StatusCreated, upload.Body.String())
	}

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/broker/certificates", "", loginAs(t, app, "auditor", "secret")))
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, material.ServerKeyPEM) {
		t.Fatalf("the list response leaked private key material: %s", body)
	}

	var response struct {
		Profiles []struct {
			Name           string `json:"name"`
			ActiveRevision string `json:"active_revision"`
			Revisions      []struct {
				RevisionID string `json:"revision_id"`
				State      string `json:"state"`
			} `json:"revisions"`
		} `json:"profiles"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(response.Profiles) != 1 || response.Profiles[0].Name != "production" {
		t.Fatalf("list response = %#v, want the production profile", response)
	}
	if len(response.Profiles[0].Revisions) != 1 {
		t.Fatalf("list response revisions = %#v, want one revision", response.Profiles[0].Revisions)
	}
}

func TestCertificatePreviewDescribesTheActivationWithoutLeakingTheKey(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	upload := httptest.NewRecorder()
	app.Handler().ServeHTTP(upload, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/production/revisions",
		material.uploadBody(t, "production"),
		loginAs(t, app, "operator", "secret")))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body = %s", upload.Code, http.StatusCreated, upload.Body.String())
	}
	var revision struct {
		RevisionID string `json:"revision_id"`
	}
	if err := json.NewDecoder(upload.Body).Decode(&revision); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/preview",
		`{"profile":"production","revision_id":"`+revision.RevisionID+`"}`,
		loginAs(t, app, "operator", "secret")))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, material.ServerKeyPEM) {
		t.Fatalf("the preview response leaked private key material: %s", body)
	}
	if !strings.Contains(body, "restart_required") {
		t.Fatalf("the preview response missing the restart flag: %s", body)
	}
}

func TestCertificateApplyActivatesTheRevision(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "operator", "secret", auth.RoleOperator)
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)

	material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
	upload := httptest.NewRecorder()
	app.Handler().ServeHTTP(upload, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/profiles/production/revisions",
		material.uploadBody(t, "production"),
		loginAs(t, app, "operator", "secret")))
	if upload.Code != http.StatusCreated {
		t.Fatalf("upload status = %d, want %d, body = %s", upload.Code, http.StatusCreated, upload.Body.String())
	}
	var revision struct {
		RevisionID string `json:"revision_id"`
	}
	if err := json.NewDecoder(upload.Body).Decode(&revision); err != nil {
		t.Fatalf("decode upload response: %v", err)
	}

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/apply",
		`{"profile":"production","revision_id":"`+revision.RevisionID+`"}`,
		loginAs(t, app, "admin", "secret")))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "PRIVATE KEY") || strings.Contains(body, material.ServerKeyPEM) {
		t.Fatalf("the apply response leaked private key material: %s", body)
	}

	list := httptest.NewRecorder()
	app.Handler().ServeHTTP(list, authedRequest(http.MethodGet, "/api/v1/broker/certificates", "", loginAs(t, app, "operator", "secret")))
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d, body = %s", list.Code, http.StatusOK, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), revision.RevisionID) {
		t.Fatalf("the list response missing the active revision: %s", list.Body.String())
	}
}

func TestCertificateApplyReportsAnUnknownRevision(t *testing.T) {
	app, store := newCertificateTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost,
		"/api/v1/broker/certificates/apply",
		`{"profile":"production","revision_id":"rev-404"}`,
		loginAs(t, app, "admin", "secret")))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("apply status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
