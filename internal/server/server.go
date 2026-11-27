package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// NewHandler returns the initial MCM HTTP handler with ACL endpoints enabled.
func NewHandler(store acl.Store) http.Handler {
	api := &aclAPI{store: store}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", api.handleLivez)
	mux.HandleFunc("GET /healthz", api.handleHealthz)
	mux.HandleFunc("GET /readyz", api.handleReadyz)
	mux.HandleFunc("GET /api/v1/acls", api.handleListRules)
	mux.HandleFunc("POST /api/v1/acls", api.handleCreateRule)
	mux.HandleFunc("PUT /api/v1/acls/{id}", api.handleUpdateRule)
	mux.HandleFunc("DELETE /api/v1/acls/{id}", api.handleDeleteRule)

	return mux
}

type aclAPI struct {
	store                 acl.Store
	audit                 func(r *http.Request, action string, resourceType string, resourceID string, result string, metadata map[string]any)
	recordSecurityFailure func(*http.Request, string, string, string)
	recordSecurityChange  func(*http.Request, string, string, string)
	// scopeSync keeps the ACL file of a listener in sync with the scoped
	// rules. It is nil when the deployment does not manage the ACL files.
	scopeSync *aclScopeSync
}

type aclRuleRequest struct {
	// Selector chooses the native Mosquitto scope of the rule. An omitted
	// selector keeps the legacy user scope, so an existing client cannot
	// change the meaning of a rule by accident.
	Selector acl.Selector `json:"selector"`
	// Scope chooses the ACL file the rule is rendered into. An omitted
	// scope keeps the global scope.
	Scope acl.Scope `json:"scope"`
	// ListenerID names the listener whose ACL file carries the rule. It is
	// required for the listener scope.
	ListenerID  string         `json:"listener_id"`
	Principal   string         `json:"principal"`
	TopicFilter string         `json:"topic_filter"`
	Permission  acl.Permission `json:"permission"`
	Description string         `json:"description"`
}

type aclErrorResponse struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func (api *aclAPI) handleLivez(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (api *aclAPI) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *aclAPI) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// syncScopes keeps the ACL file of the listeners in sync with the scoped
// rules.
//
// A failure is reported to the operator but does not undo the rule: the
// deploy preview names the listener that is not wired yet, so the change
// stays visible until the listener configuration is applied.
func (api *aclAPI) syncScopes(r *http.Request) {
	if api.scopeSync == nil {
		return
	}
	if err := api.scopeSync.Sync(r.Context()); err != nil {
		api.recordACLSecurityFailure(r, "acl_scope_sync_failed", "scope_sync_failed", "")
	}
}

// handleListRules handles GET /api/v1/acls?user=&principal=&topic=&limit=&offset=.
func (api *aclAPI) handleListRules(w http.ResponseWriter, r *http.Request) {
	limit, offset := parsePageParams(r)
	query := r.URL.Query()
	rules, total, err := api.store.ListRulesPaged(r.Context(), query.Get("user"), query.Get("principal"), query.Get("topic"), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to list acl rules"})
		return
	}
	if rules == nil {
		rules = []acl.Rule{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"rules": rules, "total": total, "limit": limit, "offset": offset})
}

func (api *aclAPI) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	rule, err := decodeRuleRequest(r)
	if err != nil {
		api.recordAudit(r, "acl.create", "acl_rule", "", "failure", map[string]any{"reason": "invalid_request"})
		api.recordACLSecurityFailure(r, "acl_create_failed", "invalid_request", "")
		writeAPIError(w, err)
		return
	}

	created, err := api.store.CreateRule(r.Context(), rule)
	if err != nil {
		api.recordAudit(r, "acl.create", "acl_rule", "", "failure", map[string]any{"principal": rule.Principal, "topic_filter": rule.TopicFilter, "permission": string(rule.Permission), "reason": err.Error()})
		api.recordACLSecurityFailure(r, "acl_create_failed", reasonForACLError(err), "")
		writeAPIError(w, err)
		return
	}
	api.recordACLSecurityChange(r, "acl_rule_created", "acl_rule", created.ID)
	api.syncScopes(r)

	api.recordAudit(r, "acl.create", "acl_rule", created.ID, "success", map[string]any{"principal": created.Principal, "topic_filter": created.TopicFilter, "permission": string(created.Permission)})
	writeJSON(w, http.StatusCreated, created)
}

func (api *aclAPI) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		api.recordACLSecurityFailure(r, "acl_update_failed", "missing_rule_id", "")
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "acl rule id is required"})
		return
	}

	rule, expectedPrincipal, err := decodeUpdateRuleRequest(r)
	if err != nil {
		api.recordAudit(r, "acl.update", "acl_rule", id, "failure", map[string]any{"reason": "invalid_request"})
		api.recordACLSecurityFailure(r, "acl_update_failed", "invalid_request", id)
		writeAPIError(w, err)
		return
	}

	updated, err := api.store.UpdateRule(r.Context(), id, rule, expectedPrincipal)
	if err != nil {
		api.recordAudit(r, "acl.update", "acl_rule", id, "failure", map[string]any{"principal": rule.Principal, "topic_filter": rule.TopicFilter, "permission": string(rule.Permission), "reason": err.Error()})
		api.recordACLSecurityFailure(r, "acl_update_failed", reasonForACLError(err), id)
		writeAPIError(w, err)
		return
	}
	api.recordACLSecurityChange(r, "acl_rule_updated", "acl_rule", updated.ID)
	api.syncScopes(r)

	api.recordAudit(r, "acl.update", "acl_rule", updated.ID, "success", map[string]any{"principal": updated.Principal, "topic_filter": updated.TopicFilter, "permission": string(updated.Permission)})
	writeJSON(w, http.StatusOK, updated)
}

func (api *aclAPI) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		api.recordACLSecurityFailure(r, "acl_delete_failed", "missing_rule_id", "")
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "acl rule id is required"})
		return
	}

	expectedPrincipal, err := expectedPrincipalQuery(r)
	if err != nil {
		api.recordAudit(r, "acl.delete", "acl_rule", id, "failure", map[string]any{"reason": "invalid_request"})
		api.recordACLSecurityFailure(r, "acl_delete_failed", "invalid_request", id)
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "invalid expected_principal"})
		return
	}
	if err := api.store.DeleteRule(r.Context(), id, expectedPrincipal); err != nil {
		api.recordAudit(r, "acl.delete", "acl_rule", id, "failure", map[string]any{"reason": err.Error()})
		api.recordACLSecurityFailure(r, "acl_delete_failed", reasonForACLError(err), id)
		writeAPIError(w, err)
		return
	}
	api.recordACLSecurityChange(r, "acl_rule_deleted", "acl_rule", id)
	api.syncScopes(r)

	api.recordAudit(r, "acl.delete", "acl_rule", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (api *aclAPI) recordAudit(r *http.Request, action string, resourceType string, resourceID string, result string, metadata map[string]any) {
	if api.audit != nil {
		api.audit(r, action, resourceType, resourceID, result, metadata)
	}
}

func decodeRuleRequest(r *http.Request) (acl.Rule, error) {
	var input aclRuleRequest
	if err := decodeStrictJSON(r, &input); err != nil {
		return acl.Rule{}, err
	}
	return ruleFromRequest(input), nil
}

type aclUpdateRequest struct {
	aclRuleRequest
	ExpectedPrincipal *string `json:"expected_principal"`
}

func decodeUpdateRuleRequest(r *http.Request) (acl.Rule, *string, error) {
	var input aclUpdateRequest
	if err := decodeStrictJSON(r, &input); err != nil {
		return acl.Rule{}, nil, err
	}
	return ruleFromRequest(input.aclRuleRequest), input.ExpectedPrincipal, nil
}

func expectedPrincipalQuery(r *http.Request) (*string, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	values, supplied := query["expected_principal"]
	if !supplied {
		return nil, nil
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("expected_principal must be supplied once")
	}
	return &values[0], nil
}

func decodeStrictJSON(r *http.Request, target any) error {
	defer r.Body.Close() //nolint:errcheck
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("decode request body: request body must contain a single JSON object")
		}
		return fmt.Errorf("decode request body: %w", err)
	}
	return nil
}

func ruleFromRequest(input aclRuleRequest) acl.Rule {
	return acl.Rule{
		Selector:    input.Selector,
		Scope:       input.Scope,
		ListenerID:  input.ListenerID,
		Principal:   input.Principal,
		TopicFilter: input.TopicFilter,
		Permission:  input.Permission,
		Description: input.Description,
	}
}

func writeAPIError(w http.ResponseWriter, err error) {
	var validationErr *acl.ValidationError
	switch {
	case isMaxBytesError(err):
		writeJSON(w, http.StatusRequestEntityTooLarge, aclErrorResponse{Error: "request body too large"})
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{
			Error:   "acl validation failed",
			Details: validationErr.Problems,
		})
	case errors.Is(err, acl.ErrRuleNotFound):
		writeJSON(w, http.StatusNotFound, aclErrorResponse{Error: "acl rule not found"})
	case errors.Is(err, acl.ErrPrincipalMismatch):
		writeJSON(w, http.StatusConflict, aclErrorResponse{Error: "acl principal precondition failed"})
	default:
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: err.Error()})
	}
}

func (api *aclAPI) recordACLSecurityFailure(r *http.Request, category string, reason string, resourceID string) {
	if api.recordSecurityFailure != nil {
		api.recordSecurityFailure(r, category, reason, resourceID)
	}
}

func (api *aclAPI) recordACLSecurityChange(r *http.Request, category string, reason string, resourceID string) {
	if api.recordSecurityChange != nil {
		api.recordSecurityChange(r, category, reason, resourceID)
	}
}

func reasonForACLError(err error) string {
	var validationErr *acl.ValidationError
	switch {
	case errors.As(err, &validationErr):
		return "validation_failed"
	case errors.Is(err, acl.ErrRuleNotFound):
		return "rule_not_found"
	case errors.Is(err, acl.ErrPrincipalMismatch):
		return "principal_mismatch"
	default:
		return "request_failed"
	}
}
