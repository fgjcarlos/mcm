package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

func TestACLAPI_CreateAndListRules(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	createBody := []byte(`{"principal":"operator","topic_filter":"factory/+/temperature","permission":"read","description":"read telemetry"}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader(createBody))
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	var created acl.Rule
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(create) returned error: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created rule missing ID")
	}
	if created.Permission != acl.PermissionRead {
		t.Fatalf("created permission = %q, want %q", created.Permission, acl.PermissionRead)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/acls", nil)
	listResp := httptest.NewRecorder()
	handler.ServeHTTP(listResp, listReq)

	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d; body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}

	var payload struct {
		Rules []acl.Rule `json:"rules"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(list) returned error: %v", err)
	}
	if len(payload.Rules) != 1 {
		t.Fatalf("listed %d rules, want 1", len(payload.Rules))
	}
	if payload.Rules[0].TopicFilter != "factory/+/temperature" {
		t.Fatalf("listed topic_filter = %q, want %q", payload.Rules[0].TopicFilter, "factory/+/temperature")
	}
}

func TestACLAPI_RejectsInvalidTopicFilter(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader([]byte(`{"principal":"operator","topic_filter":"factory/#/temperature","permission":"read"}`)))
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte(`"acl validation failed"`)) {
		t.Fatalf("response missing validation error; body=%s", resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte(`must only appear in the final topic level`)) {
		t.Fatalf("response missing topic filter detail; body=%s", resp.Body.String())
	}
}

func TestACLAPI_UpdateRuleSupportsWritePermission(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader([]byte(`{"principal":"writer","topic_filter":"factory/line1/#","permission":"write"}`)))
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	var created acl.Rule
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(create) returned error: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/acls/"+created.ID, bytes.NewReader([]byte(`{"principal":"writer","topic_filter":"factory/line1/#","permission":"readwrite","description":"line supervisor"}`)))
	updateResp := httptest.NewRecorder()
	handler.ServeHTTP(updateResp, updateReq)

	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	var updated acl.Rule
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("Unmarshal(update) returned error: %v", err)
	}
	if updated.Permission != acl.PermissionReadWrite {
		t.Fatalf("updated permission = %q, want %q", updated.Permission, acl.PermissionReadWrite)
	}
	if updated.Description != "line supervisor" {
		t.Fatalf("updated description = %q, want %q", updated.Description, "line supervisor")
	}
}

func TestACLAPI_DeleteRule(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader([]byte(`{"principal":"writer","topic_filter":"factory/line1/#","permission":"write"}`)))
	createResp := httptest.NewRecorder()
	handler.ServeHTTP(createResp, createReq)

	var created acl.Rule
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(create) returned error: %v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/acls/"+created.ID, nil)
	deleteResp := httptest.NewRecorder()
	handler.ServeHTTP(deleteResp, deleteReq)

	if deleteResp.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d; body=%s", deleteResp.Code, http.StatusNoContent, deleteResp.Body.String())
	}
}
