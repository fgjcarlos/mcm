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
	store acl.Store
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
		writeAPIError(w, err)
		return
	}

	created, err := api.store.CreateRule(r.Context(), rule)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, created)
}

func (api *aclAPI) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "acl rule id is required"})
		return
	}

	rule, err := decodeRuleRequest(r)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	updated, err := api.store.UpdateRule(r.Context(), id, rule)
	if err != nil {
		writeAPIError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

func (api *aclAPI) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		writeJSON(w, http.StatusBadRequest, aclErrorResponse{Error: "acl rule id is required"})
		return
	}

	if err := api.store.DeleteRule(r.Context(), id); err != nil {
		writeAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
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
