package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/fgjcarlos/mcm/internal/storage"
)

// edgeAPI groups the HTTP handlers for the edge site heartbeat feature.
type edgeAPI struct {
	store *storage.Store
}

// heartbeatRequest is the JSON body for POST /api/v1/edge/heartbeat.
type heartbeatRequest struct {
	SiteID  string `json:"site_id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// handleHeartbeat handles POST /api/v1/edge/heartbeat.
// An edge agent calls this endpoint to register or update its health status.
func (e *edgeAPI) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req heartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	siteID := strings.TrimSpace(req.SiteID)
	if siteID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "site_id is required"})
		return
	}

	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = "unknown"
	}

	site := &storage.EdgeSite{
		ID:      siteID,
		Name:    strings.TrimSpace(req.Name),
		Version: strings.TrimSpace(req.Version),
		Status:  status,
		Message: strings.TrimSpace(req.Message),
	}

	if err := e.store.UpsertEdgeSite(r.Context(), site); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, site)
}

// handleListSites handles GET /api/v1/edge/sites.
func (e *edgeAPI) handleListSites(w http.ResponseWriter, r *http.Request) {
	sites, err := e.store.ListEdgeSites(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sites": sites})
}

// handleGetSite handles GET /api/v1/edge/sites/{id}.
func (e *edgeAPI) handleGetSite(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if strings.TrimSpace(id) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "edge site id is required"})
		return
	}

	site, err := e.store.GetEdgeSite(r.Context(), id)
	if errors.Is(err, storage.ErrEdgeSiteNotFound) {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "edge site not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, site)
}
