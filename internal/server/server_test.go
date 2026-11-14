package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestACLAPI_Readyz(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte(`"status":"ready"`)) {
		t.Fatalf("response missing ready status; body=%s", resp.Body.String())
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

func TestACLAPI_RejectsControlCharactersWithoutPersistingRule(t *testing.T) {
	t.Parallel()

	store := acl.NewMemoryStore()
	handler := NewHandler(store)
	tests := []struct {
		name string
		body string
	}{
		{
			name: "principal newline",
			body: `{"principal":"operator\nforged","topic_filter":"factory/#","permission":"read"}`,
		},
		{
			name: "topic filter NUL",
			body: `{"principal":"operator","topic_filter":"factory/\u0000/#","permission":"read"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader([]byte(tt.body)))
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, req)

			if resp.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
			}
			if !bytes.Contains(resp.Body.Bytes(), []byte(`"acl validation failed"`)) {
				t.Fatalf("response missing validation error; body=%s", resp.Body.String())
			}
			rules, err := store.ListRules(context.Background())
			if err != nil {
				t.Fatalf("ListRules returned error: %v", err)
			}
			if len(rules) != 0 {
				t.Fatalf("invalid rule was persisted: %+v", rules)
			}
		})
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

func TestACLAPI_ListRulesPaged(t *testing.T) {
	t.Parallel()

	store := acl.NewMemoryStore()
	ctx := context.Background()
	for _, r := range []acl.Rule{
		{Principal: "alice", TopicFilter: "sensors/temp", Permission: acl.PermissionRead},
		{Principal: "bob", TopicFilter: "factory/#", Permission: acl.PermissionWrite},
		{Principal: "alice", TopicFilter: "factory/alice", Permission: acl.PermissionRead},
		{Principal: "pct%user", TopicFilter: "sensors/x", Permission: acl.PermissionRead},
	} {
		if _, err := store.CreateRule(ctx, r); err != nil {
			t.Fatalf("CreateRule returned error: %v", err)
		}
	}
	for i := 0; i < 120; i++ {
		if _, err := store.CreateRule(ctx, acl.Rule{Principal: "bulk", TopicFilter: "bulk/x", Permission: acl.PermissionRead}); err != nil {
			t.Fatalf("CreateRule bulk returned error: %v", err)
		}
	}
	handler := NewHandler(store)

	tests := []struct {
		name       string
		query      string
		wantIDs    []string
		wantCount  int
		wantTotal  int
		wantLimit  int
		wantOffset int
	}{
		{"no params uses defaults", "", nil, 50, 124, 50, 0},
		{"user filter", "?user=bob", []string{"2"}, 1, 1, 50, 0},
		{"topic filter", "?topic=SENSORS", []string{"1", "4"}, 2, 2, 50, 0},
		{"user and topic", "?user=alice&topic=factory", []string{"3"}, 1, 1, 50, 0},
		{"empty filters match everything", "?user=&topic=", nil, 50, 124, 50, 0},
		{"percent is literal", "?user=%25", []string{"4"}, 1, 1, 50, 0},
		{"total exceeds the page", "?user=alice&limit=1&offset=1", []string{"3"}, 1, 2, 1, 1},
		{"limit clamped to 100", "?limit=500", nil, 100, 124, 100, 0},
		{"unparsable limit defaults", "?limit=abc", nil, 50, 124, 50, 0},
		{"zero limit defaults", "?limit=0", nil, 50, 124, 50, 0},
		{"negative limit defaults", "?limit=-3", nil, 50, 124, 50, 0},
		{"negative offset clamped", "?offset=-5&limit=2", []string{"1", "2"}, 2, 124, 2, 0},
		{"unparsable offset defaults", "?offset=xyz&limit=2", []string{"1", "2"}, 2, 124, 2, 0},
		{"no match is an empty array", "?user=nobody", []string{}, 0, 0, 50, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := httptest.NewRecorder()
			handler.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/acls"+tt.query, nil))
			if resp.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", resp.Code, resp.Body.String())
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(resp.Body.Bytes(), &raw); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			if len(raw) != 4 || raw["rules"] == nil || raw["total"] == nil || raw["limit"] == nil || raw["offset"] == nil {
				keys := make([]string, 0, len(raw))
				for k := range raw {
					keys = append(keys, k)
				}
				t.Fatalf("envelope keys = %v, want rules/total/limit/offset", keys)
			}
			if string(raw["rules"]) == "null" {
				t.Fatalf("rules is null, want an array")
			}
			var payload struct {
				Rules  []acl.Rule `json:"rules"`
				Total  int        `json:"total"`
				Limit  int        `json:"limit"`
				Offset int        `json:"offset"`
			}
			if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
				t.Fatalf("Unmarshal returned error: %v", err)
			}
			if len(payload.Rules) != tt.wantCount || payload.Total != tt.wantTotal || payload.Limit != tt.wantLimit || payload.Offset != tt.wantOffset {
				t.Fatalf("got %d rules total=%d limit=%d offset=%d, want %d total=%d limit=%d offset=%d",
					len(payload.Rules), payload.Total, payload.Limit, payload.Offset, tt.wantCount, tt.wantTotal, tt.wantLimit, tt.wantOffset)
			}
			if tt.wantIDs != nil {
				ids := make([]string, 0, len(payload.Rules))
				for _, r := range payload.Rules {
					ids = append(ids, r.ID)
				}
				if !slices.Equal(ids, tt.wantIDs) {
					t.Fatalf("ids = %v, want %v", ids, tt.wantIDs)
				}
			}
		})
	}
}
