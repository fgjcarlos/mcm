package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// deployServicer is the interface the HTTP layer depends on.
// It allows injecting fakes in tests without importing the full deploy.Service.
type deployServicer interface {
	Preview(ctx context.Context, actor string) (deploy.PreviewResult, error)
	Apply(ctx context.Context, actor string) (storage.Deployment, error)
	List(ctx context.Context, limit, offset int) ([]storage.Deployment, error)
}

// deployAPI groups the HTTP handlers for the deploy lifecycle feature.
// It follows the same struct pattern as aclAPI.
type deployAPI struct {
	svc deployServicer
}

// handleList handles GET /api/v1/deployments.
func (d *deployAPI) handleList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	deployments, err := d.svc.List(r.Context(), limit, offset)
	if errors.Is(err, deploy.ErrDeployDisabled) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "deploy is not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deployments": deployments})
}

// handlePreview handles POST /api/v1/deployments/preview.
func (d *deployAPI) handlePreview(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)

	result, err := d.svc.Preview(r.Context(), actor)
	if errors.Is(err, deploy.ErrDeployDisabled) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "deploy is not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// handleApply handles POST /api/v1/deployments/apply.
func (d *deployAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	actor := actorFromRequest(r)

	deployment, err := d.svc.Apply(r.Context(), actor)
	if errors.Is(err, deploy.ErrDeployDisabled) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "deploy is not configured"})
		return
	}
	if errors.Is(err, deploy.ErrDeployInProgress) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "deploy already in progress"})
		return
	}
	if err != nil {
		slog.Error("deploy apply failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, deployment)
}

// actorFromRequest extracts the authenticated username from the request context,
// falling back to "anonymous" when no authentication context is present.
func actorFromRequest(r *http.Request) string {
	if claims, ok := currentUserFromContext(r.Context()); ok {
		return claims.Username
	}
	return "anonymous"
}
