package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/storage"
)

func TestLoginSuccessAndCurrentUser(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	user := seedAdminUser(t, store, "admin", "secret-password", false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var loginResp loginResponse
	if err := json.NewDecoder(rec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if loginResp.Token == "" {
		t.Fatal("login response missing token")
	}
	if loginResp.User.ID != user.ID {
		t.Fatalf("login response user id = %d, want %d", loginResp.User.ID, user.ID)
	}
	if !loginResp.ExpiresAt.After(app.now()) {
		t.Fatalf("login response expires_at = %s, want future time", loginResp.ExpiresAt)
	}

	meRec := httptest.NewRecorder()
	meReq := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	app.Handler().ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("current user status = %d, want %d, body = %s", meRec.Code, http.StatusOK, meRec.Body.String())
	}

	var meResp adminUserResponse
	if err := json.NewDecoder(meRec.Body).Decode(&meResp); err != nil {
		t.Fatalf("decode current user response: %v", err)
	}
	if meResp.Username != "admin" {
		t.Fatalf("current user username = %q, want %q", meResp.Username, "admin")
	}
}

func TestLoginRejectsUnknownDisabledAndWrongPasswordUsers(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "disabled-user", "secret-password", true)
	seedAdminUser(t, store, "good-user", "secret-password", false)

	tests := []struct {
		name           string
		body           string
		wantStatusCode int
		wantBody       string
	}{
		{
			name:           "unknown user",
			body:           `{"username":"missing","password":"secret-password"}`,
			wantStatusCode: http.StatusUnauthorized,
			wantBody:       "invalid credentials",
		},
		{
			name:           "disabled user",
			body:           `{"username":"disabled-user","password":"secret-password"}`,
			wantStatusCode: http.StatusUnauthorized,
			wantBody:       "user is disabled",
		},
		{
			name:           "wrong password",
			body:           `{"username":"good-user","password":"wrong-password"}`,
			wantStatusCode: http.StatusUnauthorized,
			wantBody:       "invalid credentials",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			app.Handler().ServeHTTP(rec, req)

			if rec.Code != tc.wantStatusCode {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, tc.wantStatusCode, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("response body missing %q: %s", tc.wantBody, rec.Body.String())
			}
		})
	}
}

func TestProtectedEndpointsRejectUnauthenticatedRequests(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "authentication required") {
		t.Fatalf("response body missing authentication error: %s", rec.Body.String())
	}
}

func TestCreateAdminUserStoresPasswordHash(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	loginToken := loginAsSeededUser(t, app, store, "admin", "secret-password")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin-users", strings.NewReader(`{"username":"operator","password":"new-password","disabled":false}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginToken)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin user status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	storedUser, err := store.GetAdminUserByUsername(context.Background(), "operator")
	if err != nil {
		t.Fatalf("GetAdminUserByUsername returned error: %v", err)
	}
	if storedUser.PasswordHash == "new-password" {
		t.Fatal("password stored in plaintext")
	}
	match, err := auth.VerifyPassword(storedUser.PasswordHash, "new-password")
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if !match {
		t.Fatal("stored password hash does not match original password")
	}
}

func newTestApp(t *testing.T) (*App, *storage.Store) {
	t.Helper()

	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{}

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}

	app, err := New(cfg, store)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	app.now = func() time.Time { return time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC) }
	return app, store
}

func seedAdminUser(t *testing.T, store *storage.Store, username string, password string, disabled bool) storage.AdminUser {
	t.Helper()

	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}

	user, err := store.CreateAdminUser(context.Background(), storage.CreateAdminUserParams{
		Username:     username,
		PasswordHash: hash,
		Disabled:     disabled,
	})
	if err != nil {
		t.Fatalf("CreateAdminUser returned error: %v", err)
	}
	return user
}

func loginAsSeededUser(t *testing.T, app *App, store *storage.Store, username string, password string) string {
	t.Helper()

	seedAdminUser(t, store, username, password, false)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var loginResp loginResponse
	if err := json.NewDecoder(rec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return loginResp.Token
}
