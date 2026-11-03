package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const brokerCopyFormat = "mcm-broker-config/v1"

type brokerConfigCopy struct {
	Format string `json:"format"`
	Body   string `json:"body"`
	Hash   string `json:"hash"`
}

func (a *App) handleBrokerConfigRevisions(w http.ResponseWriter, r *http.Request) {
	revisions, err := a.store.ListBrokerConfigRevisions(r.Context())
	if err != nil {
		a.recordAuditFromRequest(r, "broker_config.revisions", "broker_config", "", "failure", nil)
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"revisions": revisions})
}

// Recovery never reuses a consumed revision. It validates the selected bytes
// against today's files and returns a new immutable preview for explicit apply.
func (a *App) handleBrokerConfigRecover(w http.ResponseWriter, r *http.Request) {
	revisionID := r.PathValue("id")
	rev, err := a.store.GetBrokerConfigRevision(r.Context(), revisionID)
	if errors.Is(err, storage.ErrBrokerConfigRevisionNotFound) {
		a.recordAuditFromRequest(r, "broker_config.recover", "broker_config", revisionID, "failure", nil)
		writeJSON(w, 404, errorResponse{Error: "broker config revision not found"})
		return
	}
	if err != nil {
		a.recordAuditFromRequest(r, "broker_config.recover", "broker_config", revisionID, "failure", nil)
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	var req struct {
		BaseHash string `json:"base_hash"`
	}
	if err = json.NewDecoder(r.Body).Decode(&req); err != nil || req.BaseHash == "" {
		a.recordAuditFromRequest(r, "broker_config.recover", "broker_config", revisionID, "failure", nil)
		writeJSON(w, 400, errorResponse{Error: "base_hash is required"})
		return
	}
	a.writeRecoveryPreview(w, r, deploy.BrokerConfigDraft{Body: rev.ConfRendered, BaseHash: req.BaseHash}, "broker_config.recover", rev.ID)
}

// Copies contain the root configuration only. They deliberately do not claim
// to be a complete broker/database/certificate/credential backup (#295).
func (a *App) handleBrokerConfigExport(w http.ResponseWriter, r *http.Request) {
	if a.brokerCfgSvc == nil {
		a.recordAuditFromRequest(r, "broker_config.export", "broker_config", "", "failure", nil)
		writeJSON(w, 503, errorResponse{Error: "broker config is not configured"})
		return
	}
	cfg, err := a.brokerCfgSvc.GetBrokerConfig(r.Context())
	if err != nil {
		a.recordAuditFromRequest(r, "broker_config.export", "broker_config", "", "failure", nil)
		writeJSON(w, 503, errorResponse{Error: "broker config is unavailable"})
		return
	}
	a.recordAuditFromRequest(r, "broker_config.export", "broker_config", "", "success", nil)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="mosquitto-config.json"`)
	writeJSON(w, 200, brokerConfigCopy{Format: brokerCopyFormat, Body: cfg.Body, Hash: cfg.Hash})
}

func (a *App) handleBrokerConfigCopyImport(w http.ResponseWriter, r *http.Request) {
	var copy brokerConfigCopy
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&copy); err != nil {
		a.recordAuditFromRequest(r, "broker_config.import_copy", "broker_config", "", "failure", nil)
		if isMaxBytesError(err) {
			writeJSON(w, 413, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, 422, errorResponse{Error: "invalid configuration copy"})
		return
	}
	digest := sha256.Sum256([]byte(copy.Body))
	if copy.Format != brokerCopyFormat || copy.Hash != hex.EncodeToString(digest[:]) {
		a.recordAuditFromRequest(r, "broker_config.import_copy", "broker_config", "", "failure", nil)
		writeJSON(w, 422, errorResponse{Error: "invalid configuration copy format or hash"})
		return
	}
	a.writeRecoveryPreview(w, r, deploy.BrokerConfigDraft{Body: copy.Body}, "broker_config.import_copy", "")
}

func (a *App) writeRecoveryPreview(w http.ResponseWriter, r *http.Request, draft deploy.BrokerConfigDraft, action, id string) {
	svc, ok := a.brokerCfgSvc.(interface {
		PreviewBrokerConfigDraft(context.Context, string, deploy.BrokerConfigDraft) (deploy.PreviewResult, error)
	})
	if !ok {
		a.recordAuditFromRequest(r, action, "broker_config", id, "failure", nil)
		writeJSON(w, 503, errorResponse{Error: "configuration editing is unavailable"})
		return
	}
	preview, err := svc.PreviewBrokerConfigDraft(r.Context(), actorFromRequest(r), draft)
	status := 200
	switch {
	case errors.Is(err, deploy.ErrRevisionMismatch), errors.Is(err, deploy.ErrSensitiveBrokerConfig):
		status = 409
	case errors.Is(err, deploy.ErrInvalidBrokerConfig):
		status = 422
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		status = 503
	case err != nil:
		status = 500
	}
	outcome := "success"
	if err != nil {
		outcome = "failure"
	}
	a.recordAuditFromRequest(r, action, "broker_config", id, outcome, nil)
	if err != nil {
		writeJSON(w, status, map[string]any{"error": "configuration recovery preview failed", "validation_issues": preview.ValidationIssues})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, preview)
}
