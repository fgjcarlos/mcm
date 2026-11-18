package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
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

// fakeApplyCounter counts the plugin and dynamic security calls that change
// the broker, so a guard test can prove a rejected request had no effect.
type fakeApplyCounter struct{ calls int }

func (f *fakeApplyCounter) GetPlugins(context.Context) (deploy.PluginsResult, error) {
	return deploy.PluginsResult{}, nil
}

func (f *fakeApplyCounter) PreviewPlugins(context.Context, string, plugins.Document) (deploy.PreviewResult, error) {
	return deploy.PreviewResult{}, nil
}

func (f *fakeApplyCounter) ApplyPlugins(context.Context, string, string, bool, bool) (storage.Deployment, error) {
	f.calls++
	return storage.Deployment{ID: 1}, nil
}

func (f *fakeApplyCounter) GetDynSec(context.Context) (deploy.DynSecResult, error) {
	return deploy.DynSecResult{}, nil
}

func (f *fakeApplyCounter) PreviewDynSec(context.Context, string, dynsec.Document) (deploy.DynSecPreviewResult, error) {
	return deploy.DynSecPreviewResult{}, nil
}

func (f *fakeApplyCounter) ApplyDynSec(context.Context, string, string, *dynsec.Document) (deploy.DynSecApplyResult, error) {
	f.calls++
	return deploy.DynSecApplyResult{}, nil
}

func (f *fakeApplyCounter) AdoptDynSec(context.Context, string) (deploy.DynSecPreviewResult, error) {
	f.calls++
	return deploy.DynSecPreviewResult{}, nil
}

// TestRecentMFARequiredForEveryApplyRoute pins that every route that changes
// what the broker serves refuses a request without a recent-MFA proof, audits
// the refusal, leaves the broker untouched, and still reaches the handler once
// the proof is present.
func TestRecentMFARequiredForEveryApplyRoute(t *testing.T) {
	// Each case prepares its own app and returns the request body plus the
	// number of changes the handler made so far.
	type prepared struct {
		body    string
		changes func() int
	}
	// counted wires fakes that count every call that changes the broker.
	counted := func(t *testing.T, app *App, token string) prepared {
		fake := &fakeApplyCounter{}
		bridge := &fakeBridgeService{applyRes: storage.Deployment{ID: 1}}
		app.pluginSvc, app.dynsecSvc, app.bridgeSvc = fake, fake, bridge
		return prepared{
			body: `{"revision_id":"revision-1","plan_id":"plan-1"}`,
			changes: func() int {
				if bridge.applyRevID != "" {
					return fake.calls + 1
				}
				return fake.calls
			},
		}
	}
	cases := []struct {
		path    string
		cert    bool
		prepare func(t *testing.T, app *App, token string) prepared
	}{
		{path: "/api/v1/listeners/apply", prepare: func(t *testing.T, app *App, token string) prepared {
			preview := listenerPreview(t, app, token)
			// A rejected apply that consumed the preview would make the
			// verified apply fail with 409.
			return prepared{body: `{"revision_id":"` + preview.RevisionID + `","confirm":true}`}
		}},
		{path: "/api/v1/broker/certificates/apply", cert: true, prepare: func(t *testing.T, app *App, token string) prepared {
			material := newCertificateMaterial(t, "broker.example.com", time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(90*24*time.Hour))
			upload := httptest.NewRecorder()
			app.Handler().ServeHTTP(upload, authedRequest(http.MethodPost, "/api/v1/broker/certificates/profiles/production/revisions", material.uploadBody(t, "production"), token))
			if upload.Code != http.StatusCreated {
				t.Fatalf("upload: %d %s", upload.Code, upload.Body.String())
			}
			var revision struct {
				RevisionID string `json:"revision_id"`
			}
			if err := json.NewDecoder(upload.Body).Decode(&revision); err != nil {
				t.Fatal(err)
			}
			// A rejected apply that activated the revision would make the
			// verified apply fail because the revision is already active.
			return prepared{body: `{"profile":"production","revision_id":"` + revision.RevisionID + `"}`}
		}},
		{path: "/api/v1/broker/bridges/apply", prepare: counted},
		{path: "/api/v1/broker/plugins/apply", prepare: counted},
		{path: "/api/v1/broker/dynsec/apply", prepare: counted},
		{path: "/api/v1/broker/dynsec/adopt", prepare: counted},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			var app *App
			var store *storage.Store
			if tc.cert {
				app, store = newCertificateTestApp(t)
			} else {
				app, store = newTestApp(t)
			}
			t.Cleanup(func() { _ = store.Close() })
			seedAdminUserWithRole(t, store, "admin", "secret", auth.RoleAdmin)
			token := loginAs(t, app, "admin", "secret")
			p := tc.prepare(t, app, token)
			changes := func() int {
				if p.changes == nil {
					return 0
				}
				return p.changes()
			}
			call := func(proof string) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				req := authedRequest(http.MethodPost, tc.path, p.body, token)
				if proof != "" {
					req.Header.Set("X-MCM-MFA", proof)
				}
				app.Handler().ServeHTTP(rec, req)
				return rec
			}

			denied := call("")
			if denied.Code != http.StatusForbidden || denied.Body.String() != "{\"error\":\"recent MFA verification required\",\"code\":\"mfa_required\"}\n" {
				t.Fatalf("unverified request: %d %s", denied.Code, denied.Body.String())
			}
			if n := changes(); n != 0 {
				t.Fatalf("unverified request changed the broker %d time(s)", n)
			}
			events, err := store.ListAuditEvents(context.Background(), storage.AuditEventQuery{Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			audited := false
			for _, e := range events {
				if e.Action == "mfa.required" && e.ResourceID == tc.path && e.Result == "failure" && e.Actor == "admin" {
					audited = true
				}
			}
			if !audited {
				t.Fatalf("no mfa.required audit for %s: %#v", tc.path, events)
			}

			accepted := call(recentMFATestProof(t, app, token))
			if accepted.Code != http.StatusOK {
				t.Fatalf("verified request: %d %s", accepted.Code, accepted.Body.String())
			}
			if p.changes != nil && changes() != 1 {
				t.Fatalf("verified request reached the service %d time(s), want 1", changes())
			}
		})
	}
}
