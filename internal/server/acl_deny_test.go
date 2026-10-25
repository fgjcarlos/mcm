package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// TestACLDenyRuleRoundTripsThroughTheAPI pins that the native deny
// permission survives the full CRUD round trip over the HTTP API: create,
// list, update and delete.
//
// The API contract keeps its existing fields, so a deny rule uses the same
// principal/topic_filter/permission shape as every other rule. The rule is
// stored by the real SQLite-backed store, so the round trip also proves the
// permission is persisted as written.
func TestACLDenyRuleRoundTripsThroughTheAPI(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	token := loginAsSeededUser(t, app, store, "admin", "secret-password")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", strings.NewReader(
		`{"principal":"sensor-reader","topic_filter":"sensors/+/temperature","permission":"deny","description":"block one sensor"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create deny rule status = %d, want %d, body = %s", rec.Code, http.StatusCreated, rec.Body.String())
	}

	var created acl.Rule
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Permission != acl.PermissionDeny {
		t.Fatalf("created permission = %q, want %q", created.Permission, acl.PermissionDeny)
	}
	if created.ID == "" {
		t.Fatal("created deny rule has no ID")
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/acls", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list acls status = %d, want %d, body = %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listResp struct {
		Rules []acl.Rule `json:"rules"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Rules) != 1 {
		t.Fatalf("listed %d rules, want 1", len(listResp.Rules))
	}
	if listResp.Rules[0].Permission != acl.PermissionDeny {
		t.Fatalf("listed permission = %q, want %q", listResp.Rules[0].Permission, acl.PermissionDeny)
	}
	if listResp.Rules[0].TopicFilter != "sensors/+/temperature" {
		t.Fatalf("listed topic filter = %q, want %q", listResp.Rules[0].TopicFilter, "sensors/+/temperature")
	}

	updateRec := httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/acls/"+created.ID, strings.NewReader(
		`{"principal":"sensor-reader","topic_filter":"sensors/#","permission":"deny","description":"block every sensor"}`))
	updateReq.Header.Set("Content-Type", "application/json")
	updateReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update deny rule status = %d, want %d, body = %s", updateRec.Code, http.StatusOK, updateRec.Body.String())
	}
	var updated acl.Rule
	if err := json.NewDecoder(updateRec.Body).Decode(&updated); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updated.Permission != acl.PermissionDeny {
		t.Fatalf("updated permission = %q, want %q", updated.Permission, acl.PermissionDeny)
	}
	if updated.TopicFilter != "sensors/#" {
		t.Fatalf("updated topic filter = %q, want %q", updated.TopicFilter, "sensors/#")
	}

	deleteRec := httptest.NewRecorder()
	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/acls/"+created.ID, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete deny rule status = %d, want %d, body = %s", deleteRec.Code, http.StatusNoContent, deleteRec.Body.String())
	}

	finalRec := httptest.NewRecorder()
	finalReq := httptest.NewRequest(http.MethodGet, "/api/v1/acls", nil)
	finalReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(finalRec, finalReq)
	if finalRec.Code != http.StatusOK {
		t.Fatalf("list acls status = %d, want %d, body = %s", finalRec.Code, http.StatusOK, finalRec.Body.String())
	}
	var finalResp struct {
		Rules []acl.Rule `json:"rules"`
	}
	if err := json.NewDecoder(finalRec.Body).Decode(&finalResp); err != nil {
		t.Fatalf("decode final list response: %v", err)
	}
	if len(finalResp.Rules) != 0 {
		t.Fatalf("listed %d rules after delete, want 0", len(finalResp.Rules))
	}
}

// TestACLLegacyPermissionsStillRoundTrip pins that the deny permission does
// not change the contract of the permissions that existed before it: read,
// write and readwrite keep working over the same API.
func TestACLLegacyPermissionsStillRoundTrip(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	token := loginAsSeededUser(t, app, store, "admin", "secret-password")

	permissions := []string{"read", "write", "readwrite"}
	for _, permission := range permissions {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", strings.NewReader(
			`{"principal":"operator","topic_filter":"factory/+/temperature","permission":"`+permission+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		app.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s rule status = %d, want %d, body = %s", permission, rec.Code, http.StatusCreated, rec.Body.String())
		}

		var created acl.Rule
		if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		if string(created.Permission) != permission {
			t.Fatalf("created permission = %q, want %q", created.Permission, permission)
		}
	}

	listRec := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/acls", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list acls status = %d, want %d, body = %s", listRec.Code, http.StatusOK, listRec.Body.String())
	}
	var listResp struct {
		Rules []acl.Rule `json:"rules"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	if len(listResp.Rules) != len(permissions) {
		t.Fatalf("listed %d rules, want %d", len(listResp.Rules), len(permissions))
	}
}
