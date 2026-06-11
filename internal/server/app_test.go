package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/config"
	"github.com/fgjcarlos/mcm/internal/logging"
	"github.com/fgjcarlos/mcm/internal/storage"
)

func TestStatusEndpointReportsBrokerSnapshotAndTarget(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	observedAt := time.Date(2026, 5, 20, 10, 30, 0, 0, time.UTC)
	app.brokerEvents.Publish(BrokerEvent{Type: "broker_status", Status: "connected", ObservedAt: observedAt})
	app.brokerEvents.Publish(BrokerEvent{Type: "topic_message", Topic: "factory/line1", ObservedAt: observedAt.Add(time.Second)})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status endpoint status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	if resp.Broker.Status != "connected" {
		t.Fatalf("broker status = %q, want connected", resp.Broker.Status)
	}
	if resp.Broker.Target.Address != "127.0.0.1:1883" {
		t.Fatalf("broker target address = %q, want 127.0.0.1:1883", resp.Broker.Target.Address)
	}
	if resp.Broker.Metrics.StatusEvents != 1 || resp.Broker.Metrics.TopicMessages != 1 {
		t.Fatalf("broker metrics = %+v, want one status event and one topic message", resp.Broker.Metrics)
	}
	if resp.Broker.Metrics.LastMessageAt == nil || !resp.Broker.Metrics.LastMessageAt.Equal(observedAt.Add(time.Second)) {
		t.Fatalf("last_message_at = %v, want %s", resp.Broker.Metrics.LastMessageAt, observedAt.Add(time.Second))
	}
}

func TestStatusEndpointRequiresAuthWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{}
	cfg.Status.RequireAuth = true

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	app, err := New(cfg, store, logging.Discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Unauthenticated request must be rejected.
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/status", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Authenticated request (viewer role) must succeed.
	hash, _ := auth.HashPassword("viewer-password-ok")
	_, err = store.CreateAdminUser(context.Background(), storage.CreateAdminUserParams{
		Username: "viewer", PasswordHash: hash, Role: "viewer",
	})
	if err != nil {
		t.Fatalf("CreateAdminUser: %v", err)
	}
	token := issueToken(t, app, "viewer", "viewer-password-ok")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("authenticated status = %d, want %d; body = %s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
}

func TestMetricsEndpointRequiresAuthWhenConfigured(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{}
	cfg.Metrics.RequireAuth = true

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	app, err := New(cfg, store, logging.Discard())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Unauthenticated request must be rejected.
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated metrics = %d, want %d", rec.Code, http.StatusUnauthorized)
	}

	// Authenticated request (auditor role) must succeed.
	seedAdminUser(t, store, "scraper", "scraper-password-ok", false)
	token := issueToken(t, app, "scraper", "scraper-password-ok")
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req2.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("authenticated metrics = %d, want %d; body = %s", rec2.Code, http.StatusOK, rec2.Body.String())
	}
}

func issueToken(t *testing.T, app *App, username, password string) string {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct{ Token string `json:"token"` }
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return resp.Token
}

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

func TestFailedLoginCreatesSecurityEvent(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "admin_login_failed",
		Reason:   "invalid_credentials",
		Username: "admin",
		SourceIP: "203.0.113.9",
		Method:   http.MethodPost,
		Path:     "/api/v1/auth/login",
	})
}

func TestDisabledUserLoginCreatesSecurityEvent(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "disabled-user", "secret-password", true)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"disabled-user","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "admin_login_failed",
		Reason:   "disabled_user",
		Username: "disabled-user",
		Method:   http.MethodPost,
		Path:     "/api/v1/auth/login",
	})
}

func TestProtectedEndpointFailuresCreateSecurityEvents(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	tests := []struct {
		name       string
		authHeader string
	}{
		{name: "missing bearer"},
		{name: "invalid bearer", authHeader: "Bearer not-a-valid-token"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			app.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
			}
		})
	}

	events, err := store.ListSecurityEvents(context.Background(), storage.SecurityEventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListSecurityEvents returned error: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("security event count = %d, want 2", len(events))
	}
	if events[0].Reason != "invalid_bearer_token" || events[1].Reason != "missing_bearer_token" {
		t.Fatalf("event reasons = %q, %q; want invalid_bearer_token, missing_bearer_token", events[0].Reason, events[1].Reason)
	}
	for _, event := range events {
		if event.Category != "protected_api_access_failed" || event.Path != "/api/v1/auth/me" || event.Username != "" {
			t.Fatalf("unexpected protected failure event: %+v", event)
		}
	}
}

func TestSecurityEventsEndpointReturnsRecentEvents(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	failedRec := httptest.NewRecorder()
	failedReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
	failedReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(failedRec, failedReq)
	if failedRec.Code != http.StatusUnauthorized {
		t.Fatalf("failed login status = %d, want %d", failedRec.Code, http.StatusUnauthorized)
	}

	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d, body = %s", loginRec.Code, http.StatusOK, loginRec.Body.String())
	}
	var loginResp loginResponse
	if err := json.NewDecoder(loginRec.Body).Decode(&loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/security/events?limit=10", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("security events status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var resp struct {
		Events []storage.SecurityEvent `json:"events"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode security events response: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].Reason != "invalid_credentials" || resp.Events[0].Username != "admin" {
		t.Fatalf("security events response = %+v, want failed admin login event", resp.Events)
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

func TestProtectedEndpointsRejectDisabledUserWithExistingToken(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	user := seedAdminUser(t, store, "admin", "secret-password", false)
	seedAdminUser(t, store, "admin-2", "secret-password", false)
	token := loginAs(t, app, "admin", "secret-password")

	_, err := store.UpdateAdminUser(context.Background(), user.ID, storage.UpdateAdminUserParams{
		Username: user.Username,
		Disabled: true,
		Role:     user.Role,
	})
	if err != nil {
		t.Fatalf("UpdateAdminUser returned error: %v", err)
	}

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, "/api/v1/auth/me", "", token))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}

	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "protected_api_access_failed",
		Reason:   "inactive_user",
		Username: "admin",
		Method:   http.MethodGet,
		Path:     "/api/v1/auth/me",
	})
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

	events, err := store.ListAuditEvents(context.Background(), storage.AuditEventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(events))
	}
	if events[0].Actor != "admin" || events[0].Action != "admin_user.create" || events[0].ResourceType != "admin_user" || events[0].ResourceID != strconv.FormatInt(storedUser.ID, 10) || events[0].Result != "success" {
		t.Fatalf("unexpected audit event: %#v", events[0])
	}
	if strings.Contains(string(events[0].Metadata), "new-password") || strings.Contains(strings.ToLower(string(events[0].Metadata)), "password") {
		t.Fatalf("audit metadata includes password material: %s", string(events[0].Metadata))
	}
}

func TestCreateACLRuleWritesAuditEvent(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	loginToken := loginAsSeededUser(t, app, store, "admin", "secret-password")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", strings.NewReader(`{"principal":"sensor-writer","topic_filter":"sensors/+/temperature","permission":"write","description":"allow test writer"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+loginToken)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create acl status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	events, err := store.ListAuditEvents(context.Background(), storage.AuditEventQuery{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("got %d audit events, want 1", len(events))
	}
	if events[0].Actor != "admin" || events[0].Action != "acl.create" || events[0].ResourceType != "acl_rule" || events[0].Result != "success" {
		t.Fatalf("unexpected acl audit event: %#v", events[0])
	}
	if !strings.Contains(string(events[0].Metadata), "sensor-writer") {
		t.Fatalf("audit metadata missing safe principal: %s", string(events[0].Metadata))
	}
}

func TestListAuditEventsEndpointRequiresAuthAndPaginates(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	if _, err := store.RecordAuditEvent(context.Background(), storage.CreateAuditEventParams{Actor: "admin", Action: "admin_user.create", ResourceType: "admin_user", ResourceID: "1", Result: "success"}); err != nil {
		t.Fatalf("RecordAuditEvent returned error: %v", err)
	}
	loginToken := loginAsSeededUser(t, app, store, "admin", "secret-password")

	unauthRec := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events", nil)
	app.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth audit events status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-events?limit=1&offset=0", nil)
	req.Header.Set("Authorization", "Bearer "+loginToken)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit events status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "admin_user.create") {
		t.Fatalf("audit events response missing recorded event: %s", rec.Body.String())
	}
}

func TestJSONSchemaAPIRequiresAuthAndSupportsCRUD(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	unauthRec := httptest.NewRecorder()
	unauthReq := httptest.NewRequest(http.MethodGet, "/api/v1/json-schemas", nil)
	app.Handler().ServeHTTP(unauthRec, unauthReq)
	if unauthRec.Code != http.StatusUnauthorized {
		t.Fatalf("unauth json schema status = %d, want %d", unauthRec.Code, http.StatusUnauthorized)
	}

	token := loginAsSeededUser(t, app, store, "admin", "secret-password")
	createBody := `{"name":"Temperature","topic_filter":"factory/+/temperature","schema":{"type":"object","required":["temperature"],"properties":{"temperature":{"type":"number"}}},"description":"temperature payload","enabled":true}`
	createRec := httptest.NewRecorder()
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/json-schemas", strings.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create json schema status = %d, want %d, body = %s", createRec.Code, http.StatusCreated, createRec.Body.String())
	}
	var created storage.JSONSchemaDefinition
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created schema: %v", err)
	}
	if created.ID == 0 || created.TopicFilter != "factory/+/temperature" {
		t.Fatalf("unexpected created schema: %#v", created)
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/json-schemas", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list json schemas status = %d, want %d, body = %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), "Temperature") {
		t.Fatalf("list response missing created schema: %s", listRec.Body.String())
	}

	updateBody := `{"name":"Temperature disabled","topic_filter":"factory/line1/temperature","schema":{"type":"object"},"enabled":false}`
	updateRec := httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/json-schemas/"+strconv.FormatInt(created.ID, 10), strings.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update json schema status = %d, want %d, body = %s", updateRec.Code, http.StatusOK, updateRec.Body.String())
	}

	deleteRec := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/json-schemas/"+strconv.FormatInt(created.ID, 10), nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete json schema status = %d, want %d, body = %s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}
}

func TestTopicEventIncludesJSONSchemaValidationResult(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	_, err := store.CreateJSONSchema(context.Background(), storage.CreateJSONSchemaParams{
		Name:        "Temperature",
		TopicFilter: "factory/+/temperature",
		Schema:      json.RawMessage(`{"type":"object","required":["temperature","unit"],"properties":{"temperature":{"type":"number"},"unit":{"type":"string"}}}`),
		Enabled:     true,
	})
	if err != nil {
		t.Fatalf("CreateJSONSchema returned error: %v", err)
	}

	validEvent := app.TopicEvent("factory/line1/temperature", []byte(`{"temperature":21.5,"unit":"c"}`), 256)
	if validEvent.SchemaValidation == nil || !validEvent.SchemaValidation.Valid || validEvent.SchemaValidation.SchemaName != "Temperature" {
		t.Fatalf("valid event schema validation = %#v, want valid Temperature result", validEvent.SchemaValidation)
	}

	invalidEvent := app.TopicEvent("factory/line1/temperature", []byte(`{"temperature":"hot"}`), 256)
	if invalidEvent.SchemaValidation == nil || invalidEvent.SchemaValidation.Valid || len(invalidEvent.SchemaValidation.Errors) == 0 {
		t.Fatalf("invalid event schema validation = %#v, want bounded errors", invalidEvent.SchemaValidation)
	}

	unmatchedEvent := app.TopicEvent("factory/line1/humidity", []byte(`{"humidity":60}`), 256)
	if unmatchedEvent.SchemaValidation != nil {
		t.Fatalf("unmatched event schema validation = %#v, want nil", unmatchedEvent.SchemaValidation)
	}
}

func TestLoginRateLimitedAfterMaxFailuresFromSameIP(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)

	for i := 0; i < app.loginMaxAttempts; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.10")
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	lockoutRec := httptest.NewRecorder()
	lockoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	lockoutReq.Header.Set("Content-Type", "application/json")
	lockoutReq.Header.Set("X-Forwarded-For", "203.0.113.10")
	app.Handler().ServeHTTP(lockoutRec, lockoutReq)

	if lockoutRec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body = %s", lockoutRec.Code, http.StatusTooManyRequests, lockoutRec.Body.String())
	}
	if retry := lockoutRec.Header().Get("Retry-After"); retry == "" {
		t.Fatal("Retry-After header missing on lockout response")
	}
	if got := lockoutRec.Body.String(); !strings.Contains(got, "too many login attempts") {
		t.Fatalf("response body = %s, want generic too-many-attempts message", got)
	}

	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "admin_login_rate_limited",
		Reason:   "ip_lockout",
		Username: "admin",
		SourceIP: "203.0.113.10",
		Method:   http.MethodPost,
		Path:     "/api/v1/auth/login",
	})
}

func TestLoginRateLimitedAcrossIPsForSameUsername(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)

	for i := 0; i < app.loginMaxAttempts; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		// Vary the IP every attempt so per-IP counters never reach the threshold.
		req.Header.Set("X-Forwarded-For", "10.0.0."+strconv.Itoa(i+1))
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	lockoutRec := httptest.NewRecorder()
	lockoutReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	lockoutReq.Header.Set("Content-Type", "application/json")
	lockoutReq.Header.Set("X-Forwarded-For", "10.0.0.99")
	app.Handler().ServeHTTP(lockoutRec, lockoutReq)

	if lockoutRec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d, body = %s", lockoutRec.Code, http.StatusTooManyRequests, lockoutRec.Body.String())
	}

	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "admin_login_rate_limited",
		Reason:   "username_lockout",
		Username: "admin",
		SourceIP: "10.0.0.99",
		Method:   http.MethodPost,
		Path:     "/api/v1/auth/login",
	})
}

func TestLoginSucceedsUnderRateLimitThreshold(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)

	// Stay strictly below the per-IP threshold.
	for i := 0; i < app.loginMaxAttempts-1; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100.5")
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	successRec := httptest.NewRecorder()
	successReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	successReq.Header.Set("Content-Type", "application/json")
	successReq.Header.Set("X-Forwarded-For", "198.51.100.5")
	app.Handler().ServeHTTP(successRec, successReq)

	if successRec.Code != http.StatusOK {
		t.Fatalf("login status under threshold = %d, want %d, body = %s", successRec.Code, http.StatusOK, successRec.Body.String())
	}
}

func TestSuccessfulLoginResetsFailedAttempts(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)

	for i := 0; i < app.loginMaxAttempts-1; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "198.51.100.20")
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", i+1, rec.Code, http.StatusUnauthorized)
		}
	}

	successRec := httptest.NewRecorder()
	successReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	successReq.Header.Set("Content-Type", "application/json")
	successReq.Header.Set("X-Forwarded-For", "198.51.100.20")
	app.Handler().ServeHTTP(successRec, successReq)
	if successRec.Code != http.StatusOK {
		t.Fatalf("success status = %d, want %d, body = %s", successRec.Code, http.StatusOK, successRec.Body.String())
	}

	statsByIP, err := store.CountFailedLoginAttemptsByIP(context.Background(), "198.51.100.20", app.now().Add(-app.loginLockoutWindow))
	if err != nil {
		t.Fatalf("CountFailedLoginAttemptsByIP: %v", err)
	}
	if statsByIP.Count != 0 {
		t.Fatalf("failed attempts by IP after success = %d, want 0", statsByIP.Count)
	}

	statsByUser, err := store.CountFailedLoginAttemptsByUsername(context.Background(), "admin", app.now().Add(-app.loginLockoutWindow))
	if err != nil {
		t.Fatalf("CountFailedLoginAttemptsByUsername: %v", err)
	}
	if statsByUser.Count != 0 {
		t.Fatalf("failed attempts by username after success = %d, want 0", statsByUser.Count)
	}
}

func TestAppReadyzChecksDatabase(t *testing.T) {
	app, store := newTestApp(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"status":"ready"`)) {
		t.Fatalf("ready response missing status; body=%s", rec.Body.String())
	}

	if err := store.Close(); err != nil {
		t.Fatalf("store.Close returned error: %v", err)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/readyz", nil)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d, want %d; body=%s", rec.Code, http.StatusServiceUnavailable, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"status":"not_ready"`)) {
		t.Fatalf("not-ready response missing status; body=%s", rec.Body.String())
	}
}

func newTestApp(t *testing.T) (*App, *storage.Store) {
	t.Helper()

	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{}
	// Tests model MCM behind a reverse proxy: trust the default httptest peer
	// (192.0.2.1) so X-Forwarded-For client IPs are honored in test requests.
	cfg.HTTP.TrustedProxies = []string{"192.0.2.1"}
	// Disable auth on observability endpoints in unit tests; auth-gating is
	// exercised explicitly in TestMetricsRequireAuth / TestStatusRequireAuth.
	cfg.Metrics.RequireAuth = false
	cfg.Status.RequireAuth = false

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}

	app, err := New(cfg, store, logging.Discard())
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

func assertLatestSecurityEvent(t *testing.T, store *storage.Store, want storage.SecurityEvent) {
	t.Helper()

	events, err := store.ListSecurityEvents(context.Background(), storage.SecurityEventQuery{Limit: 1})
	if err != nil {
		t.Fatalf("ListSecurityEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("security event count = %d, want 1", len(events))
	}
	got := events[0]
	if got.Category != want.Category || got.Reason != want.Reason || got.Username != want.Username || got.Method != want.Method || got.Path != want.Path {
		t.Fatalf("security event = %+v, want category=%q reason=%q username=%q method=%q path=%q", got, want.Category, want.Reason, want.Username, want.Method, want.Path)
	}
	if want.SourceIP != "" && got.SourceIP != want.SourceIP {
		t.Fatalf("security event source_ip = %q, want %q", got.SourceIP, want.SourceIP)
	}
	if got.ObservedAt.IsZero() || got.CreatedAt.IsZero() {
		t.Fatalf("security event timestamps must be set: %+v", got)
	}
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

func seedAdminUserWithRole(t *testing.T, store *storage.Store, username string, password string, role auth.Role) storage.AdminUser {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword returned error: %v", err)
	}
	user, err := store.CreateAdminUser(context.Background(), storage.CreateAdminUserParams{
		Username:     username,
		PasswordHash: hash,
		Role:         string(role),
	})
	if err != nil {
		t.Fatalf("CreateAdminUser returned error: %v", err)
	}
	return user
}

func loginAs(t *testing.T, app *App, username string, password string) string {
	t.Helper()
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

func authedRequest(method, path, body, token string) *http.Request {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func TestRBACBootstrapAdminGetsAdminRole(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{Username: "boot-admin", Password: "boot-secret"}

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	app, err := New(cfg, store, logging.Discard())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := app.BootstrapAdmin(context.Background(), cfg); err != nil {
		t.Fatalf("BootstrapAdmin returned error: %v", err)
	}
	created, err := store.GetAdminUserByUsername(context.Background(), "boot-admin")
	if err != nil {
		t.Fatalf("GetAdminUserByUsername returned error: %v", err)
	}
	if created.Role != string(auth.RoleAdmin) {
		t.Fatalf("bootstrap admin role = %q, want %q", created.Role, auth.RoleAdmin)
	}
}

func TestBootstrapAdminCreatesWhenOnlyDisabledAdminsExist(t *testing.T) {
	cfg := config.Default()
	cfg.Database.Path = filepath.Join(t.TempDir(), "mcm.db")
	cfg.Auth.JWTSecret = "0123456789abcdef0123456789abcdef"
	cfg.Auth.TokenTTL = "1h"
	cfg.Auth.BootstrapAdmin = config.BootstrapAdminConfig{Username: "boot-admin", Password: "boot-secret"}

	store, err := storage.Open(cfg.Database.Path)
	if err != nil {
		t.Fatalf("storage.Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "disabled-admin", "secret-password", true)

	app, err := New(cfg, store, logging.Discard())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if err := app.BootstrapAdmin(context.Background(), cfg); err != nil {
		t.Fatalf("BootstrapAdmin returned error: %v", err)
	}

	created, err := store.GetAdminUserByUsername(context.Background(), "boot-admin")
	if err != nil {
		t.Fatalf("GetAdminUserByUsername returned error: %v", err)
	}
	if created.Disabled {
		t.Fatal("bootstrap admin should be active")
	}
}

func TestAdminEndpointsRejectDisablingLastActiveAdmin(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	admin := seedAdminUser(t, store, "admin", "secret-password", false)
	token := loginAs(t, app, "admin", "secret-password")

	rec := httptest.NewRecorder()
	body := `{"username":"admin","disabled":true,"role":"admin"}`
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPut, "/api/v1/admin-users/"+strconv.FormatInt(admin.ID, 10), body, token))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), storage.ErrLastActiveAdmin.Error()) {
		t.Fatalf("response body = %s, want %q", rec.Body.String(), storage.ErrLastActiveAdmin.Error())
	}
}

func TestAdminEndpointsRejectDeletingLastActiveAdmin(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	admin := seedAdminUser(t, store, "admin", "secret-password", false)
	token := loginAs(t, app, "admin", "secret-password")

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/v1/admin-users/"+strconv.FormatInt(admin.ID, 10), "", token))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), storage.ErrLastActiveAdmin.Error()) {
		t.Fatalf("response body = %s, want %q", rec.Body.String(), storage.ErrLastActiveAdmin.Error())
	}
}

func TestRBACLoginResponseIncludesRole(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "ops", "secret-password", auth.RoleOperator)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"ops","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp loginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if resp.User.Role != string(auth.RoleOperator) {
		t.Fatalf("login response role = %q, want %q", resp.User.Role, auth.RoleOperator)
	}
}

func TestRBACViewerCannotMutateResources(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "view", "secret-password", auth.RoleViewer)
	token := loginAs(t, app, "view", "secret-password")

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"create acl", http.MethodPost, "/api/v1/acls", `{"principal":"p","topic_filter":"t","permission":"read"}`},
		{"create json schema", http.MethodPost, "/api/v1/json-schemas", `{"name":"x","topic_filter":"t","schema":{},"enabled":true}`},
		{"create admin user", http.MethodPost, "/api/v1/admin-users", `{"username":"x","password":"secret-password"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			app.Handler().ServeHTTP(rec, authedRequest(tc.method, tc.path, tc.body, token))
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
			}
		})
	}

	assertLatestSecurityEvent(t, store, storage.SecurityEvent{
		Category: "protected_api_access_denied",
		Reason:   "insufficient_role",
		Username: "view",
		Method:   http.MethodPost,
		Path:     "/api/v1/admin-users",
	})
}

func TestRBACAuditorCanReadAuditAndSecurity(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "audit", "secret-password", auth.RoleAuditor)
	token := loginAs(t, app, "audit", "secret-password")

	for _, path := range []string{"/api/v1/audit-events", "/api/v1/security/events", "/api/v1/acls", "/api/v1/admin-users"} {
		rec := httptest.NewRecorder()
		app.Handler().ServeHTTP(rec, authedRequest(http.MethodGet, path, "", token))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d, body = %s", path, rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	// Auditor must still be denied for mutating endpoints.
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/acls", `{"principal":"p","topic_filter":"t","permission":"read"}`, token))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("auditor POST /api/v1/acls status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
}

func TestRBACOperatorCanManageMQTTButNotAdminUsers(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "ops", "secret-password", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret-password")

	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, authedRequest(http.MethodPost, "/api/v1/acls", `{"principal":"sensor-writer","topic_filter":"sensors/+/temperature","permission":"write"}`, token))
	if rec.Code != http.StatusCreated {
		t.Fatalf("operator POST /api/v1/acls status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	denyRec := httptest.NewRecorder()
	app.Handler().ServeHTTP(denyRec, authedRequest(http.MethodPost, "/api/v1/admin-users", `{"username":"extra","password":"secret-password"}`, token))
	if denyRec.Code != http.StatusForbidden {
		t.Fatalf("operator POST /api/v1/admin-users status = %d, want %d, body = %s", denyRec.Code, http.StatusForbidden, denyRec.Body.String())
	}
}
