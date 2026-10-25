package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
)

// handleGetBrokerSettings serves GET /api/v1/broker/settings — the
// current value of every managed setting together with the catalog
// description of the form (type, unit, bounds, default and help text).
//
// The payload is derived from the versioned catalog, so the form never
// guesses a range and the server enforces the same rules it advertises.
func (b *brokerConfigAPI) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker settings are not configured"})
		return
	}
	res, err := b.svc.GetBrokerSettings(r.Context())
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker settings are not configured"})
		return
	}
	if err != nil {
		slog.Error("broker settings read failed", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handlePreviewBrokerSettings serves POST /api/v1/broker/settings/preview —
// applies the settings the operator edited to the on-disk configuration,
// validates them against the catalog and stores an immutable preview
// revision. The operator confirms the diff and applies the revision
// through POST /api/v1/broker/config/apply.
func (b *brokerConfigAPI) handlePreviewSettings(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker settings are not configured"})
		return
	}
	actor := actorFromRequest(r)
	var doc settings.Document
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	res, err := b.svc.PreviewBrokerSettings(r.Context(), actor, doc)
	var settingsErr *deploy.SettingsError
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker settings are not configured"})
	case errors.As(err, &settingsErr):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "broker settings are invalid",
			"issues": settingsErr.Issues,
		})
	case err != nil:
		slog.Error("broker settings preview failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
