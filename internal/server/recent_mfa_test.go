package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/pquerna/otp/totp"
)

// stepUp drives POST /api/v1/auth/mfa/step-up over HTTP, so the production
// authorization and enrollment checks are exercised.
func stepUp(t *testing.T, app *App, token, secret string) *httptest.ResponseRecorder {
	t.Helper()
	code, err := totp.GenerateCode(secret, app.now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	body, _ := json.Marshal(mfaVerifyRequest{Code: code})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/step-up", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func TestRecentMFAStepUpIssuesSessionBoundProof(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	secret, _ := enrollAndEnableMFA(t, app, "secret", "admin")

	rec := stepUp(t, app, token, secret)
	if rec.Code != http.StatusOK {
		t.Fatalf("step-up status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var result struct {
		Proof     string `json:"proof"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode step-up: %v", err)
	}
	if result.Proof == "" || result.ExpiresAt == "" {
		t.Fatalf("step-up body missing proof fields: %s", rec.Body.String())
	}
	// A proof authorizes one sensitive operation. It never authenticates.
	me := httptest.NewRecorder()
	app.Handler().ServeHTTP(me, authedRequest("GET", "/api/v1/auth/me", "", result.Proof))
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("proof accepted as an access token: %d", me.Code)
	}
}

func TestRecentMFAStepUpRejectsReplayedAndWrongCodes(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	secret, _ := enrollAndEnableMFA(t, app, "secret", "admin")

	code, err := totp.GenerateCode(secret, app.now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	body, _ := json.Marshal(mfaVerifyRequest{Code: code})
	call := func(payload string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/step-up", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		app.Handler().ServeHTTP(rec, req)
		return rec
	}
	if rec := call(string(body)); rec.Code != http.StatusOK {
		t.Fatalf("first step-up status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// The same TOTP step cannot be exchanged twice.
	if rec := call(string(body)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed TOTP status = %d, want 401", rec.Code)
	}
	wrong, _ := json.Marshal(mfaVerifyRequest{Code: "000000"})
	if rec := call(string(wrong)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code status = %d, want 401", rec.Code)
	}
}

func TestRecentMFAStepUpRequiresEnrolledMFA(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/step-up", strings.NewReader(`{"code":"123456"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("step-up without enrollment status = %d, want 403, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "mfa_required") {
		t.Fatalf("step-up without enrollment body = %s", rec.Body.String())
	}

	anonymous := httptest.NewRecorder()
	app.Handler().ServeHTTP(anonymous, httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/step-up", strings.NewReader(`{"code":"123456"}`)))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous step-up status = %d, want 401", anonymous.Code)
	}
}
