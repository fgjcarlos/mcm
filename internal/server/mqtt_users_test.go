package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// seedMQTTUser creates an MQTT user directly in the store for test setup.
func seedMQTTUser(t *testing.T, store *storage.Store, username string) storage.MQTTUser {
	t.Helper()
	user, err := store.CreateMQTTUser(context.Background(), storage.CreateMQTTUserParams{
		Username:     username,
		PasswordHash: "$7$101$dummysaltsaltx==$dummyhashhashhashhashhashhashhashhashhashhashhashhashhashhashxxxx=",
	})
	if err != nil {
		t.Fatalf("seedMQTTUser: CreateMQTTUser returned error: %v", err)
	}
	return user
}

// TestHandleCreateMQTTUser covers the POST /api/v1/mqtt-users endpoint.
func TestHandleCreateMQTTUser(t *testing.T) {
	t.Run("success returns 201 with password and no password_hash", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users", `{"username":"device-01"}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
		}

		var resp mqttUserWithPasswordResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Username != "device-01" {
			t.Errorf("username = %q, want device-01", resp.Username)
		}
		if resp.Password == "" {
			t.Error("password field must be present in create response")
		}
		if resp.ID == 0 {
			t.Error("id must be non-zero")
		}
		if resp.CreatedAt.IsZero() {
			t.Error("created_at must be set")
		}
		// Ensure password_hash is NOT in JSON output
		raw := rec.Body.String()
		if strings.Contains(raw, "password_hash") {
			t.Errorf("response must not contain password_hash: %s", raw)
		}
	})

	t.Run("missing username returns 400", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users", `{"username":""}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "username") {
			t.Errorf("response body missing 'username' mention: %s", rec.Body.String())
		}
	})

	t.Run("duplicate username returns 409", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")
		seedMQTTUser(t, store, "device-01")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users", `{"username":"device-01"}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusConflict, rec.Body.String())
		}
	})

	t.Run("auditor is forbidden", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "audit", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "audit", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users", `{"username":"device-01"}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusForbidden, rec.Body.String())
		}
	})
}

// TestHandleListMQTTUsers covers GET /api/v1/mqtt-users.
func TestHandleListMQTTUsers(t *testing.T) {
	t.Run("empty list returns [] not null", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "audit", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "audit", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/mqtt-users", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		body := strings.TrimSpace(rec.Body.String())
		if !strings.HasPrefix(body, "[") {
			t.Errorf("empty list response must be JSON array, got: %s", body)
		}
		var list []mqttUserResponse
		if err := json.Unmarshal([]byte(body), &list); err != nil {
			t.Fatalf("decode empty list: %v", err)
		}
		if list == nil {
			t.Error("decoded list must not be nil (want empty slice [])")
		}
		if len(list) != 0 {
			t.Errorf("list length = %d, want 0", len(list))
		}
	})

	t.Run("list with users returns array", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "audit", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "audit", "secret")
		seedMQTTUser(t, store, "device-a")
		seedMQTTUser(t, store, "device-b")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/mqtt-users", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var list []mqttUserResponse
		if err := json.NewDecoder(rec.Body).Decode(&list); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		if len(list) != 2 {
			t.Errorf("list length = %d, want 2", len(list))
		}
		// password_hash must never appear
		raw := rec.Body.String()
		if strings.Contains(raw, "password_hash") {
			t.Errorf("list response must not contain password_hash: %s", raw)
		}
	})

	t.Run("unauthenticated returns 401", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/mqtt-users", "", "")
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
		}
	})
}

// TestHandleGetMQTTUser covers GET /api/v1/mqtt-users/{id}.
func TestHandleGetMQTTUser(t *testing.T) {
	t.Run("found returns 200 with user", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "audit", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "audit", "secret")
		user := seedMQTTUser(t, store, "device-x")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp mqttUserResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.ID != user.ID {
			t.Errorf("id = %d, want %d", resp.ID, user.ID)
		}
		if resp.Username != "device-x" {
			t.Errorf("username = %q, want device-x", resp.Username)
		}
		raw := rec.Body.String()
		if strings.Contains(raw, "password_hash") {
			t.Errorf("response must not contain password_hash: %s", raw)
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "audit", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "audit", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/mqtt-users/9999", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "audit", "secret", auth.RoleAuditor)
		token := loginAs(t, app, "audit", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodGet, "/api/v1/mqtt-users/notanid", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
		}
	})
}

// TestHandleUpdateMQTTUser covers PUT /api/v1/mqtt-users/{id}.
func TestHandleUpdateMQTTUser(t *testing.T) {
	t.Run("update username returns 200 with updated user", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")
		user := seedMQTTUser(t, store, "device-old")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"username":"device-new"}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp mqttUserResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Username != "device-new" {
			t.Errorf("username = %q, want device-new", resp.Username)
		}
	})

	t.Run("update disabled returns 200", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")
		user := seedMQTTUser(t, store, "device-enable")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"disabled":true}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp mqttUserResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.Disabled {
			t.Error("disabled = false, want true")
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPut, "/api/v1/mqtt-users/9999", `{"username":"x"}`, token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})
}

// TestHandleDeleteMQTTUser covers DELETE /api/v1/mqtt-users/{id}.
func TestHandleDeleteMQTTUser(t *testing.T) {
	t.Run("success returns 204", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")
		user := seedMQTTUser(t, store, "device-del")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodDelete, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNoContent, rec.Body.String())
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodDelete, "/api/v1/mqtt-users/9999", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})
}

// TestHandleResetMQTTUserPassword covers POST /api/v1/mqtt-users/{id}/reset-password.
func TestHandleResetMQTTUserPassword(t *testing.T) {
	t.Run("success returns 200 with new password", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")
		user := seedMQTTUser(t, store, "device-reset")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10)+"/reset-password", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
		}
		var resp mqttUserWithPasswordResponse
		if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if resp.Password == "" {
			t.Error("password field must be present in reset-password response")
		}
		if resp.ID != user.ID {
			t.Errorf("id = %d, want %d", resp.ID, user.ID)
		}
		// password_hash must not be in response
		raw := rec.Body.String()
		if strings.Contains(raw, "password_hash") {
			t.Errorf("response must not contain password_hash: %s", raw)
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users/9999/reset-password", "", token)
		app.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusNotFound, rec.Body.String())
		}
	})
}
