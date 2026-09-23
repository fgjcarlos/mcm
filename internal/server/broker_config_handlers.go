package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/mosquitto/catalog"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// brokerConfigServicer is the subset of *deploy.Service the HTTP layer
// needs to expose broker-config preview/apply/adopt endpoints. Tests
// substitute an in-memory fake; production wires the WithBrokerConfig
// service so the methods are available.
type brokerConfigServicer interface {
	PreviewBrokerConfig(ctx context.Context, actor string) (deploy.PreviewResult, error)
	ApplyBrokerConfig(ctx context.Context, actor, revisionID string, force, adopt bool) (storage.Deployment, error)
	AdoptBrokerConfig(ctx context.Context, actor string) (storage.BrokerConfigAdoption, error)
	GetBrokerConfig(ctx context.Context) (deploy.GetBrokerConfigResult, error)
}

// brokerConfigAPI groups the HTTP handlers for the broker-config
// preview/apply lifecycle (issue #298). It mirrors deployAPI: a thin
// shell over the deploy.Service and the loaded directive catalog.
type brokerConfigAPI struct {
	svc     brokerConfigServicer
	catalog *catalog.Catalog
}

// brokerConfigImportRequest is the optional body for POST
// /api/v1/broker/config/import and /preview. The operator can pin a
// different conf path; today the field is accepted and ignored because
// the production deploy service only manages the path it was wired with
// via WithBrokerConfig. We keep the field so the contract matches what
// the UI sends; a future change to switchable paths is then a non-
// breaking addition.
type brokerConfigImportRequest struct {
	Path string `json:"path,omitempty"`
}

// brokerConfigApplyRequest is the body of POST /api/v1/broker/config/apply.
type brokerConfigApplyRequest struct {
	RevisionID string `json:"revision_id"`
	Force      bool   `json:"force,omitempty"`
	Adopt      bool   `json:"adopt,omitempty"`
}

// handleGetBrokerConfig serves GET /api/v1/broker/config — returns
// the current on-disk conf body and its sha256. The path comes from
// the deploy service so an operator can see exactly what file MCM
// manages.
func (b *brokerConfigAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	res, err := b.svc.GetBrokerConfig(r.Context())
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": res.Path,
		"body": res.Body,
		"hash": res.Hash,
	})
}

// handleImportBrokerConfig serves POST /api/v1/broker/config/import —
// triggers a preview and returns the PreviewResult. The UI calls this
// after the operator uploads or types new content; the apply binds to
// the returned revision_id.
func (b *brokerConfigAPI) handleImport(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	actor := actorFromRequest(r)
	if _, err := decodeBrokerImport(r); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	res, err := b.svc.PreviewBrokerConfig(r.Context(), actor)
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleAdoptBrokerConfig serves POST /api/v1/broker/config/adopt —
// records an explicit operator adoption so a subsequent apply can
// proceed without the `adopt=true` flag.
func (b *brokerConfigAPI) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	actor := actorFromRequest(r)
	adoption, err := b.svc.AdoptBrokerConfig(r.Context(), actor)
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, adoption)
}

// handleApplyBrokerConfig serves POST /api/v1/broker/config/apply —
// applies the immutable preview revision identified by revision_id.
// Maps the lifecycle sentinels to HTTP status codes per issue #298.
func (b *brokerConfigAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var req brokerConfigApplyRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	if strings.TrimSpace(req.RevisionID) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "revision_id is required; preview the broker config first"})
		return
	}
	dep, err := b.svc.ApplyBrokerConfig(r.Context(), actor, req.RevisionID, req.Force, req.Adopt)
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
	case errors.Is(err, deploy.ErrRevisionMissing):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "revision_id is required; preview the broker config first"})
	case errors.Is(err, deploy.ErrRequiresRestart):
		directives := directivesFromRestartErr(err)
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "apply requires broker restart",
			"directives": directives,
		})
	case errors.Is(err, deploy.ErrAdoptionRequired):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "broker config adoption required; call POST /api/v1/broker/config/adopt first or pass adopt=true"})
	case errors.Is(err, deploy.ErrRevisionMismatch):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision base no longer matches the on-disk configuration"})
	case errors.Is(err, deploy.ErrRevisionExpired):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision has expired; preview the broker config again"})
	case errors.Is(err, deploy.ErrRevisionConsumed):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision was already applied"})
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "deploy already in progress"})
	case err != nil:
		slog.Error("broker-config apply failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, dep)
	}
}

// handleBrokerConfigCatalog serves GET /api/v1/broker/config/catalog —
// returns the loaded directive catalog so the UI can render an editor.
func (b *brokerConfigAPI) handleCatalog(w http.ResponseWriter, r *http.Request) {
	if b.catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config catalog is not loaded"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":    b.catalog.VersionName(),
		"directives": b.catalog.Specs(),
	})
}

// directivesFromRestartErr extracts the comma-joined directive list
// the deploy service appended to ErrRequiresRestart (format
// "directives <name1>,<name2>"). Returns an empty string when the
// error does not carry that suffix.
func directivesFromRestartErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	const sep = "directives "
	i := strings.Index(msg, sep)
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(msg[i+len(sep):])
}

// decodeBrokerImport reads (and ignores) the import body so we accept
// the same payload the UI sends today while not rejecting requests
// that omit the body.
func decodeBrokerImport(r *http.Request) (*brokerConfigImportRequest, error) {
	if r.Body == nil {
		return nil, io.EOF
	}
	var req brokerConfigImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, err
	}
	return &req, nil
}

// mountBrokerConfigRoutes wires the broker-config endpoints onto the
// supplied mux. The actual mux.Handle calls live in Handler() in app.go
// so the openapi drift test (which scans app.go) sees the full surface.
// This helper is the single construction point for the brokerConfigAPI
// so tests can wire a fake by setting App.brokerCfgSvc / brokerCfgCatalog.
func (a *App) brokerConfigAPI() *brokerConfigAPI {
	return &brokerConfigAPI{svc: a.brokerCfgSvc, catalog: a.brokerCfgCatalog}
}