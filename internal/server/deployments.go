package server

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/fgjcarlos/mcm/internal/deploy"
)

// deployAPI groups the HTTP handlers for the deployment lifecycle endpoints.
type deployAPI struct {
	svc *deploy.Service
}

// previewResponse is the JSON shape returned by POST /api/v1/deployments/preview.
type previewResponse struct {
	ACLDiff    string `json:"acl_diff"`
	PasswdDiff string `json:"passwd_diff"`
	HasChanges bool   `json:"has_changes"`
}

// applyResponse is the JSON shape returned by POST /api/v1/deployments/apply.
type applyResponse struct {
	ID      int64  `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// handleDeployPreview handles POST /api/v1/deployments/preview.
// It returns a unified diff between the current on-disk Mosquitto configuration
// and the rendered configuration stored in MCM.
func (api *deployAPI) handleDeployPreview(w http.ResponseWriter, r *http.Request) {
	actor := "anonymous"
	if claims, ok := currentUserFromContext(r.Context()); ok && claims.Username != "" {
		actor = claims.Username
	}

	result, err := api.svc.Preview(r.Context(), actor)
	if err != nil {
		if errors.Is(err, deploy.ErrDeployDisabled) {
			writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "deploy mode is not configured"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, previewResponse{
		ACLDiff:    result.ACLDiff,
		PasswdDiff: result.PasswdDiff,
		HasChanges: result.HasChanges,
	})
}

// handleDeployApply handles POST /api/v1/deployments/apply.
// It atomically writes Mosquitto configuration, runs a healthcheck, and rolls
// back on failure. All terminal outcomes return HTTP 200; the response status
// field conveys the result.
func (api *deployAPI) handleDeployApply(w http.ResponseWriter, r *http.Request) {
	actor := "anonymous"
	if claims, ok := currentUserFromContext(r.Context()); ok && claims.Username != "" {
		actor = claims.Username
	}

	deployment, err := api.svc.Apply(r.Context(), actor)
	if err != nil {
		if errors.Is(err, deploy.ErrDeployDisabled) {
			writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "deploy mode is not configured"})
			return
		}
		// If we have a deployment record (rollback_failed, etc.) return it even
		// when there is an error — the spec mandates 200 for all terminal states.
		if deployment.ID != 0 {
			writeJSON(w, http.StatusOK, applyResponse{
				ID:      deployment.ID,
				Status:  deployment.Status,
				Message: deployment.Message,
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, applyResponse{
		ID:      deployment.ID,
		Status:  deployment.Status,
		Message: deployment.Message,
	})
}

// handleListDeployments handles GET /api/v1/deployments.
// It returns a paginated list of deployment records ordered newest first.
func (api *deployAPI) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0

	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > 100 {
		limit = 100
	}

	if rawOffset := r.URL.Query().Get("offset"); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	deployments, err := api.svc.List(r.Context(), limit, offset)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"deployments": deployments,
		"limit":       limit,
		"offset":      offset,
	})
}
