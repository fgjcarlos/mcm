package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/fgjcarlos/mcm/internal/acl"
)

// NewHandler returns the initial MCM HTTP handler with ACL endpoints enabled.
func NewHandler(store acl.Store) http.Handler {
	api := &aclAPI{store: store}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.handleHealthz)
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
}

type aclRuleRequest struct {
	Principal   string         `json:"principal"`
	TopicFilter string         `json:"topic_filter"`
	Permission  acl.Permission `json:"permission"`
	Description string         `json:"description"`
}

type aclErrorResponse struct {
	Error   string   `json:"error"`
	Details []string `json:"details,omitempty"`
}

func (api *aclAPI) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *aclAPI) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := api.store.ListRules(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, aclErrorResponse{Error: "failed to list acl rules"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
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

	rule, err := decodeRuleRequest(r)
	if err != nil {
		api.recordAudit(r, "acl.update", "acl_rule", id, "failure", map[string]any{"reason": "invalid_request"})
		api.recordACLSecurityFailure(r, "acl_update_failed", "invalid_request", id)
		writeAPIError(w, err)
		return
	}

	updated, err := api.store.UpdateRule(r.Context(), id, rule)
	if err != nil {
		api.recordAudit(r, "acl.update", "acl_rule", id, "failure", map[string]any{"principal": rule.Principal, "topic_filter": rule.TopicFilter, "permission": string(rule.Permission), "reason": err.Error()})
		api.recordACLSecurityFailure(r, "acl_update_failed", reasonForACLError(err), id)
		writeAPIError(w, err)
		return
	}
	api.recordACLSecurityChange(r, "acl_rule_updated", "acl_rule", updated.ID)

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

	if err := api.store.DeleteRule(r.Context(), id); err != nil {
		api.recordAudit(r, "acl.delete", "acl_rule", id, "failure", map[string]any{"reason": err.Error()})
		api.recordACLSecurityFailure(r, "acl_delete_failed", reasonForACLError(err), id)
		writeAPIError(w, err)
		return
	}
	api.recordACLSecurityChange(r, "acl_rule_deleted", "acl_rule", id)

	api.recordAudit(r, "acl.delete", "acl_rule", id, "success", nil)
	w.WriteHeader(http.StatusNoContent)
}

func (api *aclAPI) recordAudit(r *http.Request, action string, resourceType string, resourceID string, result string, metadata map[string]any) {
	if api.audit != nil {
		api.audit(r, action, resourceType, resourceID, result, metadata)
	}
}

func decodeRuleRequest(r *http.Request) (acl.Rule, error) {
	defer r.Body.Close()

	var input aclRuleRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&input); err != nil {
		return acl.Rule{}, fmt.Errorf("decode request body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return acl.Rule{}, fmt.Errorf("decode request body: request body must contain a single JSON object")
		}
		return acl.Rule{}, fmt.Errorf("decode request body: %w", err)
	}

	return acl.Rule{
		Principal:   input.Principal,
		TopicFilter: input.TopicFilter,
		Permission:  input.Permission,
		Description: input.Description,
	}, nil
}

func writeAPIError(w http.ResponseWriter, err error) {
	var validationErr *acl.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{
			Error:   "acl validation failed",
			Details: validationErr.Problems,
		})
	case errors.Is(err, acl.ErrRuleNotFound):
		writeJSON(w, http.StatusNotFound, aclErrorResponse{Error: "acl rule not found"})
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
	default:
		return "request_failed"
	}
}
