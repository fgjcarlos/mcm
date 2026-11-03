package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
	"github.com/pquerna/otp/totp"
)

// recentMFATestProof enrolls and confirms through HTTP, without bypassing the
// production authorization middleware. It returns a fresh step-up proof bound
// to the given session and verifies that the TOTP step cannot be replayed.
func recentMFATestProof(t *testing.T, app *App, token string) string {
	t.Helper()
	call := func(path, body string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, authedRequest("POST", path, body, token))
		return rec
	}
	setup := call("/api/v1/auth/mfa/setup", "{}")
	if setup.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", setup.Code, setup.Body.String())
	}
	var enrollment mfaSetupResponse
	if err := json.Unmarshal(setup.Body.Bytes(), &enrollment); err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(enrollment.Secret, app.now())
	if err != nil {
		t.Fatal(err)
	}
	verified := call("/api/v1/auth/mfa/verify", `{"code":"`+code+`"}`)
	if verified.Code != http.StatusNoContent {
		t.Fatalf("enrollment: %d %s", verified.Code, verified.Body.String())
	}
	// The confirmation consumed the current TOTP step, so the step-up uses
	// the next one.
	now := app.now().Add(30 * time.Second)
	app.now = func() time.Time { return now }
	code, err = totp.GenerateCode(enrollment.Secret, app.now())
	if err != nil {
		t.Fatal(err)
	}
	step := call("/api/v1/auth/mfa/step-up", `{"code":"`+code+`"}`)
	if step.Code != http.StatusOK {
		t.Fatalf("step-up: %d %s", step.Code, step.Body.String())
	}
	var result struct {
		Proof string `json:"proof"`
	}
	if err := json.Unmarshal(step.Body.Bytes(), &result); err != nil || result.Proof == "" {
		t.Fatalf("step-up body: %s %v", step.Body.String(), err)
	}
	replay := call("/api/v1/auth/mfa/step-up", `{"code":"`+code+`"}`)
	if replay.Code != http.StatusUnauthorized {
		t.Fatalf("TOTP replay accepted: %d", replay.Code)
	}
	return result.Proof
}

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

// withRecentMFATest attaches a fresh recent-MFA proof to an authenticated
// request, so an operation test keeps testing its original handler contract
// now that the sensitive routes require the proof.
func withRecentMFATest(t *testing.T, app *App, req *http.Request) *http.Request {
	t.Helper()
	token := strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer ")
	if token != "" {
		req.Header.Set("X-MCM-MFA", recentMFATestProof(t, app, token))
	}
	return req
}

func TestRecentMFARequiredForApply(t *testing.T) {
	app, store := newAppWithBroker(t, &fakeBrokerConfigService{depRes: storage.Deployment{ID: 42}}, nil, "")
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
	token := loginAs(t, app, "admin", "secret")
	clock := app.now().Add(time.Second)
	app.now = func() time.Time { return clock }
	secondToken := loginAs(t, app, "admin", "secret")
	apply := func(proof string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := authedRequest("POST", "/api/v1/broker/config/apply", `{"revision_id":"rev-1"}`, token)
		req.Header.Set("X-MCM-MFA", proof)
		app.Handler().ServeHTTP(rec, req)
		return rec
	}
	denied := apply("")
	if denied.Code != 403 || denied.Body.String() != "{\"error\":\"recent MFA verification required\",\"code\":\"mfa_required\"}\n" {
		t.Fatalf("unverified apply: %d %s", denied.Code, denied.Body.String())
	}
	proof := recentMFATestProof(t, app, token)
	accepted := apply(proof)
	if accepted.Code != 200 {
		t.Fatalf("verified apply: %d %s", accepted.Code, accepted.Body.String())
	}
	// A proof cannot be used as a bearer access token.
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest("GET", "/api/v1/auth/me", "", proof))
	if rec.Code != 401 {
		t.Fatalf("proof accepted as session: %d", rec.Code)
	}
	// It is bound to the exact session, even for the same account.
	original := token
	token = secondToken
	if r := apply(proof); r.Code != 403 {
		t.Fatalf("MFA proof copied to a different valid session: %d", r.Code)
	}
	token = original
	if r := apply(proof + "x"); r.Code != 403 {
		t.Fatalf("tampered proof accepted: %d", r.Code)
	}
	now := app.now().Add(5 * time.Minute)
	app.now = func() time.Time { return now }
	if r := apply(proof); r.Code != 403 {
		t.Fatalf("expired MFA accepted: %d %s", r.Code, r.Body.String())
	}
	// A still-unexpired proof is also revoked when enrollment is removed.
	clock = now.Add(-4 * time.Minute)
	app.now = func() time.Time { return clock }
	disabled := httptest.NewRecorder()
	app.Handler().ServeHTTP(disabled, authedRequest("DELETE", "/api/v1/auth/mfa", `{"password":"secret"}`, token))
	if disabled.Code != 204 {
		t.Fatalf("disable MFA: %d %s", disabled.Code, disabled.Body.String())
	}
	if r := apply(proof); r.Code != 403 {
		t.Fatalf("revoked enrollment accepted: %d", r.Code)
	}
}
