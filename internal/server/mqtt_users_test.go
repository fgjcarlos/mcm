package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fgjcarlos/mcm/internal/acl"
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

func TestHandleCreateMQTTUserRejectsControlCharacters(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPost, "/api/v1/mqtt-users", `{"username":"device\nforged"}`, token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "control characters") {
		t.Fatalf("response missing validation detail: %s", rec.Body.String())
	}
	users, err := store.ListMQTTUsers(context.Background())
	if err != nil {
		t.Fatalf("ListMQTTUsers returned error: %v", err)
	}
	if len(users) != 0 {
		t.Fatalf("invalid user was persisted: %+v", users)
	}
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
		app.rememberMQTTPassword(user.Username, "device-password")
		if _, err := store.ACLStore().CreateRule(context.Background(), acl.Rule{
			Principal:   user.Username,
			TopicFilter: "devices/#",
			Permission:  acl.PermissionRead,
		}); err != nil {
			t.Fatalf("create ACL rule: %v", err)
		}

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
		rules, err := store.ACLStore().ListRules(context.Background())
		if err != nil {
			t.Fatalf("list ACL rules: %v", err)
		}
		if len(rules) != 1 || rules[0].Principal != "device-new" {
			t.Fatalf("renamed user ACL rules = %+v, want principal device-new", rules)
		}
		if password, ok := app.CleartextPassword("device-new"); !ok || password != "device-password" {
			t.Fatalf("renamed user cleartext password = %q, %t; want device-password, true", password, ok)
		}
		if _, ok := app.CleartextPassword("device-old"); ok {
			t.Fatal("old username retained a cleartext password after rename")
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

		reenableRec := httptest.NewRecorder()
		reenableReq := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"disabled":false}`, token)
		app.Handler().ServeHTTP(reenableRec, reenableReq)
		if reenableRec.Code != http.StatusOK {
			t.Fatalf("re-enable status = %d, want %d, body = %s", reenableRec.Code, http.StatusOK, reenableRec.Body.String())
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

	t.Run("service account username is reserved", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })
		app.mosquitto.Username = "svc-mcm"

		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")

		createRec := httptest.NewRecorder()
		createReq := authedRequest(http.MethodPost, "/api/v1/mqtt-users", `{"username":"svc-mcm"}`, token)
		app.Handler().ServeHTTP(createRec, createReq)
		if createRec.Code != http.StatusConflict || !strings.Contains(createRec.Body.String(), "reserved") {
			t.Fatalf("reserved create status/body = %d/%s, want 409 with reservation error", createRec.Code, createRec.Body.String())
		}

		user := seedMQTTUser(t, store, "device-normal")
		updateRec := httptest.NewRecorder()
		updateReq := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"username":"svc-mcm"}`, token)
		app.Handler().ServeHTTP(updateRec, updateReq)
		if updateRec.Code != http.StatusConflict || !strings.Contains(updateRec.Body.String(), "reserved") {
			t.Fatalf("reserved update status/body = %d/%s, want 409 with reservation error", updateRec.Code, updateRec.Body.String())
		}
	})
}

func TestHandleUpdateMQTTUserKeepsVerifierMigrationUnderMutationLock(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret")
	user := seedMQTTUser(t, store, "device-before")
	app.rememberMQTTPassword(user.Username, "device-password")

	// Hold the verifier map lock after the database commit point. The rename
	// handler must still hold the storage mutation lock while it migrates the
	// cleartext entry, otherwise Apply could acquire the mutation lock and
	// render the new username before its verifier is available.
	app.userPasswordsMu.Lock()
	verifierLockHeld := true
	defer func() {
		if verifierLockHeld {
			app.userPasswordsMu.Unlock()
		}
	}()

	record := httptest.NewRecorder()
	request := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"username":"device-after"}`, token)
	renameDone := make(chan struct{})
	go func() {
		app.Handler().ServeHTTP(record, request)
		close(renameDone)
	}()

	deadline := time.After(time.Second)
	for {
		updated, err := store.GetMQTTUser(context.Background(), user.ID)
		if err != nil {
			t.Fatalf("get renamed MQTT user: %v", err)
		}
		if updated.Username == "device-after" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("rename did not commit while verifier migration was blocked")
		default:
			time.Sleep(time.Millisecond)
		}
	}

	mutationLockAcquired := make(chan struct{})
	go func() {
		store.LockMutations()
		close(mutationLockAcquired)
		store.UnlockMutations()
	}()
	select {
	case <-mutationLockAcquired:
		t.Fatal("mutation lock was released before the rename verifier migration completed")
	case <-time.After(50 * time.Millisecond):
	}

	app.userPasswordsMu.Unlock()
	verifierLockHeld = false
	select {
	case <-renameDone:
	case <-time.After(time.Second):
		t.Fatal("rename did not complete after verifier migration was released")
	}
	select {
	case <-mutationLockAcquired:
	case <-time.After(time.Second):
		t.Fatal("mutation lock was not released after rename completed")
	}
	if record.Code != http.StatusOK {
		t.Fatalf("rename status = %d, want %d, body = %s", record.Code, http.StatusOK, record.Body.String())
	}
	if password, ok := app.CleartextPassword("device-after"); !ok || password != "device-password" {
		t.Fatalf("renamed user cleartext password = %q, %t; want device-password, true", password, ok)
	}
}

func TestHandleMQTTUserUpdateRejectsControlCharacters(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret")
	user := seedMQTTUser(t, store, "device-update")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"username":"device\rforged"}`, token)
	app.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "control characters") {
		t.Fatalf("status/body = %d/%s, want 400 with validation detail", rec.Code, rec.Body.String())
	}
	got, err := store.GetMQTTUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetMQTTUser returned error: %v", err)
	}
	if got.Username != "device-update" {
		t.Fatalf("username after rejected update = %q, want device-update", got.Username)
	}
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

func TestHandleConfiguredLegacyMQTTServiceUserCannotBeDisabled(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	app.mosquitto.Username = "svc-mcm"
	seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret")
	user := seedMQTTUser(t, store, "svc-mcm")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodPut, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), `{"disabled":true}`, token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cannot be disabled, deleted, or reset") {
		t.Fatalf("status/body = %d/%s, want 409 with service-user protection", rec.Code, rec.Body.String())
	}
	got, err := store.GetMQTTUser(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("GetMQTTUser returned error: %v", err)
	}
	if got.Disabled {
		t.Fatal("configured service user was disabled")
	}
}

func TestHandleConfiguredLegacyMQTTServiceUserCannotBeDeleted(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	app.mosquitto.Username = "svc-mcm"
	seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret")
	user := seedMQTTUser(t, store, "svc-mcm")

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodDelete, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), "", token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cannot be disabled, deleted, or reset") {
		t.Fatalf("status/body = %d/%s, want 409 with service-user protection", rec.Code, rec.Body.String())
	}
	if _, err := store.GetMQTTUser(context.Background(), user.ID); err != nil {
		t.Fatalf("service user lookup after rejected delete: %v", err)
	}
}

func TestHandleDeleteMQTTUserLookupFailureDoesNotDelete(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })
	seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
	token := loginAs(t, app, "ops", "secret")
	user := seedMQTTUser(t, store, "device-delete-lookup-error")
	lookupErr := errors.New("lookup failed")
	app.lookupMQTTUser = func(context.Context, int64) (storage.MQTTUser, error) {
		return storage.MQTTUser{}, lookupErr
	}

	rec := httptest.NewRecorder()
	req := authedRequest(http.MethodDelete, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10), "", token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	if _, err := store.GetMQTTUser(context.Background(), user.ID); err != nil {
		t.Fatalf("user lookup after failed pre-delete lookup: %v", err)
	}
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
		if password, ok := app.CleartextPassword(user.Username); !ok || password != resp.Password {
			t.Fatalf("reset cleartext lookup = %q, %t; want returned password", password, ok)
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

	t.Run("configured legacy service user cannot be reset", func(t *testing.T) {
		app, store := newTestApp(t)
		t.Cleanup(func() { _ = store.Close() })
		app.mosquitto.Username = "svc-mcm"
		seedAdminUserWithRole(t, store, "ops", "secret", auth.RoleOperator)
		token := loginAs(t, app, "ops", "secret")
		user := seedMQTTUser(t, store, "svc-mcm")

		rec := httptest.NewRecorder()
		req := authedRequest(http.MethodPost, "/api/v1/mqtt-users/"+strconv.FormatInt(user.ID, 10)+"/reset-password", "", token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "cannot be disabled, deleted, or reset") {
			t.Fatalf("status/body = %d/%s, want 409 with service-user protection", rec.Code, rec.Body.String())
		}
	})
}
