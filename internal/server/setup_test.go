package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const testSetupToken = "setup-token-for-interactive-first-admin"

func TestSetupStatusPublicRoute(t *testing.T) {
	app, store := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/setup", nil))
	var status setupStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode GET setup status: %v", err)
	}
	if rec.Code != http.StatusOK || status.Status != "required" {
		t.Fatalf("GET setup = %d %+v, want required", rec.Code, status)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

func TestSetupCreatesFirstAdminAndIssuesSession(t *testing.T) {
	app, store := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
	t.Cleanup(func() { _ = store.Close() })

	body := `{"username":"first-admin","password":"chosen-password-9","password_confirmation":"chosen-password-9","setup_token":"` + testSetupToken + `"}`
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST setup = %d %s, want 201", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
	var got loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode setup response: %v", err)
	}
	if got.Token == "" || got.User.Username != "first-admin" {
		t.Fatalf("setup response missing session/user: %+v", got)
	}
	if strings.Contains(rec.Body.String(), testSetupToken) || strings.Contains(rec.Body.String(), "chosen-password-9") {
		t.Fatalf("setup response echoed submitted secret material: %s", rec.Body.String())
	}
	me := httptest.NewRecorder()
	app.Handler().ServeHTTP(me, authedRequest(http.MethodGet, "/api/v1/auth/me", "", got.Token))
	if me.Code != http.StatusOK {
		t.Fatalf("setup-issued token failed authentication: %d %s", me.Code, me.Body.String())
	}
	var status setupStatusResponse
	statusRec := httptest.NewRecorder()
	app.Handler().ServeHTTP(statusRec, httptest.NewRequest(http.MethodGet, "/api/v1/auth/setup", nil))
	if err := json.Unmarshal(statusRec.Body.Bytes(), &status); err != nil || status.Status != "complete" {
		t.Fatalf("GET completed setup = %+v err=%v body=%s", status, err, statusRec.Body.String())
	}
	user, err := store.GetAdminUserByUsername(t.Context(), "first-admin")
	if err != nil || user.PasswordHash == "chosen-password-9" {
		t.Fatalf("persisted admin = %+v, err=%v", user, err)
	}
	conflict := httptest.NewRecorder()
	app.Handler().ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body)))
	if conflict.Code != http.StatusConflict || conflict.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("repeat setup = %d headers=%v body=%s, want no-store 409", conflict.Code, conflict.Header(), conflict.Body.String())
	}
}

func TestSetupRejectsMissingOrWrongTokenAndExistingAccounts(t *testing.T) {
	t.Run("unconfigured status and token", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })
		status := httptest.NewRecorder()
		app.Handler().ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/v1/auth/setup", nil))
		if !strings.Contains(status.Body.String(), `"status":"token_not_configured"`) || status.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("GET setup = %d headers=%v body=%s", status.Code, status.Header(), status.Body.String())
		}
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(`{"username":"x","password":"valid-password","password_confirmation":"valid-password"}`)))
		if rec.Code != http.StatusForbidden || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("POST setup = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
		}
		count, err := store.CountAdminUsers(t.Context())
		if err != nil || count != 0 {
			t.Fatalf("admin count=%d err=%v, want no account", count, err)
		}
	})

	t.Run("missing token is rejected when configured", func(t *testing.T) {
		app, store := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
		t.Cleanup(func() { _ = store.Close() })
		body := `{"username":"x","password":"valid-password","password_confirmation":"valid-password"}`
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body)))
		count, err := store.CountAdminUsers(t.Context())
		if rec.Code != http.StatusForbidden || err != nil || count != 0 {
			t.Fatalf("missing-token setup = %d, count=%d err=%v body=%s", rec.Code, count, err, rec.Body.String())
		}
	})

	t.Run("wrong token cannot be bypassed by client headers", func(t *testing.T) {
		app, store := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
		t.Cleanup(func() { _ = store.Close() })
		body := `{"username":"x","password":"valid-password","password_confirmation":"valid-password","setup_token":"wrong-token"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body))
		req.Header.Set("X-Forwarded-For", "127.0.0.1")
		req.Header.Set("X-Real-IP", "127.0.0.1")
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("wrong-token setup = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "wrong-token") || strings.Contains(rec.Body.String(), "valid-password") {
			t.Fatalf("error response echoed credentials: %s", rec.Body.String())
		}
		count, err := store.CountAdminUsers(t.Context())
		if err != nil || count != 0 {
			t.Fatalf("admin count=%d err=%v, want no account", count, err)
		}
	})

	t.Run("disabled account closes setup", func(t *testing.T) {
		app, store := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
		t.Cleanup(func() { _ = store.Close() })
		seedAdminUser(t, store, "disabled", "existing-password", true)
		body := `{"username":"x","password":"valid-password","password_confirmation":"valid-password","setup_token":"` + testSetupToken + `"}`
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body)))
		if rec.Code != http.StatusConflict {
			t.Fatalf("POST setup = %d, want 409: %s", rec.Code, rec.Body.String())
		}
		if _, err := store.GetAdminUserByUsername(t.Context(), "x"); err != storage.ErrUserNotFound {
			t.Fatalf("unexpected setup account state: err=%v", err)
		}
	})
}

func TestSetupRejectsInvalidFieldsWithoutCreatingAccount(t *testing.T) {
	app, store := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
	t.Cleanup(func() { _ = store.Close() })
	cases := []struct {
		name, password, confirmation, wantError string
	}{
		{"mismatch", "chosen-password-9", "different-password-8", "passwords do not match"},
		{"short password", "short", "short", "password must be at least 8 characters"},
		{"common password", "password", "password", "password is too common; choose a stronger password"},
		{"password equals username", "operator", "operator", "password must not equal the username"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"username":"operator","password":"` + tc.password + `","password_confirmation":"` + tc.confirmation + `","setup_token":"` + testSetupToken + `"}`
			rec := httptest.NewRecorder()
			app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body)))
			if rec.Code != http.StatusBadRequest || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("invalid setup = %d headers=%v body=%s", rec.Code, rec.Header(), rec.Body.String())
			}
			if want := `{"error":"` + tc.wantError + `"}` + "\n"; rec.Body.String() != want {
				t.Fatalf("validation response = %q, want %q", rec.Body.String(), want)
			}
			count, err := store.CountAdminUsers(t.Context())
			if err != nil || count != 0 {
				t.Fatalf("admin count=%d err=%v, want no account", count, err)
			}
		})
	}
}

func TestConcurrentSetupAcrossDatabaseHandlesCreatesOneAdmin(t *testing.T) {
	app1, store1 := newTestApp(t, func(cfg *config.Config) { cfg.Auth.SetupToken = testSetupToken })
	t.Cleanup(func() { _ = store1.Close() })
	store2, err := storage.Open(app1.cfg.Database.Path)
	if err != nil {
		t.Fatalf("open second store handle: %v", err)
	}
	t.Cleanup(func() { _ = store2.Close() })
	cfg2 := app1.cfg
	app2, err := New(cfg2, store2, nil)
	if err != nil {
		t.Fatalf("create second app: %v", err)
	}
	body := `{"username":"race-admin","password":"chosen-password-9","password_confirmation":"chosen-password-9","setup_token":"` + testSetupToken + `"}`
	apps := []*App{app1, app2}
	results := make([]int, 2)
	var wg sync.WaitGroup
	for i, app := range apps {
		wg.Add(1)
		go func(i int, app *App) {
			defer wg.Done()
			rec := httptest.NewRecorder()
			app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/setup", strings.NewReader(body)))
			results[i] = rec.Code
		}(i, app)
	}
	wg.Wait()
	if !((results[0] == http.StatusCreated && results[1] == http.StatusConflict) || (results[1] == http.StatusCreated && results[0] == http.StatusConflict)) {
		t.Fatalf("concurrent setup statuses = %v, want one 201 and one 409", results)
	}
	count, err := store1.CountAdminUsers(t.Context())
	if err != nil || count != 1 {
		t.Fatalf("admin count=%d err=%v, want exactly one", count, err)
	}
}
