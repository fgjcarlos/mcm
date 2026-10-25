package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// postACLRule posts a raw JSON body to the ACL collection endpoint.
func postACLRule(t *testing.T, handler http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/acls", bytes.NewReader([]byte(body)))
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	return resp
}

// TestACLAPIAcceptsSelectorRequest pins that the ACL API accepts the native
// selector field and reports the stored scope back. Before native selectors
// existed the field was rejected as an unknown request field.
func TestACLAPIAcceptsSelectorRequest(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"selector":"anonymous","topic_filter":"public/#","permission":"read"}`)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), `"selector":"anonymous"`) {
		t.Fatalf("response body = %s, want the stored selector", resp.Body.String())
	}
}

// TestACLAPI_CreatesNativeSelectorRules pins that the API accepts the
// native selectors and the deny permission, and that the response reports
// the stored scope.
func TestACLAPI_CreatesNativeSelectorRules(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	tests := []struct {
		name         string
		body         string
		wantSelector acl.Selector
		wantPerm     acl.Permission
	}{
		{
			name:         "anonymous rule",
			body:         `{"selector":"anonymous","topic_filter":"public/#","permission":"read"}`,
			wantSelector: acl.SelectorAnonymous,
			wantPerm:     acl.PermissionRead,
		},
		{
			name:         "pattern rule with deny",
			body:         `{"selector":"pattern","topic_filter":"clients/%u/state","permission":"deny"}`,
			wantSelector: acl.SelectorPattern,
			wantPerm:     acl.PermissionDeny,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := postACLRule(t, handler, tt.body)
			if resp.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusCreated, resp.Body.String())
			}

			var created acl.Rule
			if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
				t.Fatalf("Unmarshal(create) returned error: %v", err)
			}
			if created.Selector != tt.wantSelector {
				t.Fatalf("selector = %q, want %q", created.Selector, tt.wantSelector)
			}
			if created.Permission != tt.wantPerm {
				t.Fatalf("permission = %q, want %q", created.Permission, tt.wantPerm)
			}
			if created.Principal != "" {
				t.Fatalf("principal = %q, want empty", created.Principal)
			}
		})
	}
}

// TestACLAPI_LegacyRequestStaysUserScoped pins that a request without a
// selector keeps the legacy user scope: it must not silently turn into an
// anonymous rule.
func TestACLAPI_LegacyRequestStaysUserScoped(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"principal":"alice","topic_filter":"sensors/#","permission":"read"}`)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}

	var created acl.Rule
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(create) returned error: %v", err)
	}
	if created.Selector != acl.SelectorUser {
		t.Fatalf("selector = %q, want %q", created.Selector, acl.SelectorUser)
	}
	if created.Principal != "alice" {
		t.Fatalf("principal = %q, want %q", created.Principal, "alice")
	}
}

// TestACLAPI_EmptyPrincipalWithoutSelectorIsRejected pins that a malformed
// legacy request (no principal, no selector) is still a validation error
// instead of an anonymous rule.
func TestACLAPI_EmptyPrincipalWithoutSelectorIsRejected(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"topic_filter":"sensors/#","permission":"read"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	var payload aclErrorResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(error) returned error: %v", err)
	}
	if len(payload.Details) == 0 {
		t.Fatalf("expected validation details; body=%s", resp.Body.String())
	}
	found := false
	for _, detail := range payload.Details {
		if detail == "principal is required" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'principal is required' detail, got %v", payload.Details)
	}
}

// TestACLAPI_RejectsUnknownSelector pins that an unsupported selector is a
// validation error instead of a stored rule.
func TestACLAPI_RejectsUnknownSelector(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"selector":"bogus","topic_filter":"sensors/#","permission":"read"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}

	var payload aclErrorResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(error) returned error: %v", err)
	}
	found := false
	for _, detail := range payload.Details {
		if detail == `selector must be one of: "user", "anonymous", "pattern"` {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected selector validation detail, got %v", payload.Details)
	}
}

// TestACLAPI_RejectsUnknownField pins that unknown request fields are still
// rejected after the request shape grew a selector.
func TestACLAPI_RejectsUnknownField(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"principal":"alice","topic_filter":"sensors/#","permission":"read","kind":"bogus"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

// TestACLAPI_RejectsPrincipalOnAnonymousRule pins that an anonymous rule
// cannot carry a principal.
func TestACLAPI_RejectsPrincipalOnAnonymousRule(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"selector":"anonymous","principal":"alice","topic_filter":"public/#","permission":"read"}`)
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

// TestACLAPI_UpdatesSelector pins that the update endpoint round-trips the
// selector.
func TestACLAPI_UpdatesSelector(t *testing.T) {
	t.Parallel()

	handler := NewHandler(acl.NewMemoryStore())

	resp := postACLRule(t, handler, `{"principal":"alice","topic_filter":"sensors/#","permission":"read"}`)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var created acl.Rule
	if err := json.Unmarshal(resp.Body.Bytes(), &created); err != nil {
		t.Fatalf("Unmarshal(create) returned error: %v", err)
	}

	updateBody := []byte(`{"selector":"anonymous","topic_filter":"public/#","permission":"read"}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/acls/"+created.ID, bytes.NewReader(updateBody))
	updateResp := httptest.NewRecorder()
	handler.ServeHTTP(updateResp, updateReq)

	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d; body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	var updated acl.Rule
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("Unmarshal(update) returned error: %v", err)
	}
	if updated.Selector != acl.SelectorAnonymous {
		t.Fatalf("selector = %q, want %q", updated.Selector, acl.SelectorAnonymous)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/acls", nil)
	listResp := httptest.NewRecorder()
	handler.ServeHTTP(listResp, listReq)

	var payload struct {
		Rules []acl.Rule `json:"rules"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal(list) returned error: %v", err)
	}
	if len(payload.Rules) != 1 {
		t.Fatalf("listed %d rules, want 1", len(payload.Rules))
	}
	if payload.Rules[0].Selector != acl.SelectorAnonymous {
		t.Fatalf("listed selector = %q, want %q", payload.Rules[0].Selector, acl.SelectorAnonymous)
	}
}
