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
	"github.com/fgjcarlos/mcm/internal/mosquitto/settings"
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
	GetBrokerSettings(ctx context.Context) (deploy.BrokerSettingsResult, error)
	PreviewBrokerSettings(ctx context.Context, actor string, doc settings.Document) (deploy.PreviewResult, error)
}

// brokerConfigAPI groups the HTTP handlers for the broker-config
// preview/apply lifecycle (issue #298). It mirrors deployAPI: a thin
// shell over the deploy.Service and the loaded directive catalog.
type brokerConfigAPI struct {
	svc     brokerConfigServicer
	catalog *catalog.Catalog
}

// brokerConfigImportRequest optionally supplies an edited root and its base
// hash. Path is retained for compatibility but cannot select another file.
type brokerConfigImportRequest struct {
	Path     string  `json:"path,omitempty"`
	Body     *string `json:"body,omitempty"`
	BaseHash string  `json:"base_hash,omitempty"`
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
	if errors.Is(err, deploy.ErrSensitiveBrokerConfig) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config is not configured"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, res)
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
	req, err := decodeBrokerImport(r)
	if err != nil && !errors.Is(err, io.EOF) {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	var res deploy.PreviewResult
	if req != nil && req.Body != nil {
		drafter, ok := b.svc.(interface {
			PreviewBrokerConfigDraft(context.Context, string, deploy.BrokerConfigDraft) (deploy.PreviewResult, error)
		})
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "configuration editing is unavailable"})
			return
		}
		res, err = drafter.PreviewBrokerConfigDraft(r.Context(), actor, deploy.BrokerConfigDraft{Body: *req.Body, BaseHash: req.BaseHash})
	} else {
		res, err = b.svc.PreviewBrokerConfig(r.Context(), actor)
	}
	if errors.Is(err, deploy.ErrSensitiveBrokerConfig) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
		return
	}
	if errors.Is(err, deploy.ErrRevisionMismatch) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision base no longer matches the on-disk configuration"})
		return
	}
	if errors.Is(err, deploy.ErrInvalidBrokerConfig) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "broker configuration validation failed", "validation_issues": res.ValidationIssues})
		return
	}
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
	case errors.Is(err, deploy.ErrBrokerRestartUnavailable), errors.Is(err, deploy.ErrBrokerVerificationFailed), errors.Is(err, deploy.ErrSensitiveBrokerConfig):
		writeJSON(w, http.StatusConflict, errorResponse{Error: err.Error()})
	case errors.Is(err, deploy.ErrInvalidBrokerConfig):
		writeJSON(w, http.StatusUnprocessableEntity, errorResponse{Error: "broker configuration validation failed"})
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

// handleBrokerConfigSupportMatrix serves
// GET /api/v1/broker/config/support-matrix — returns the versioned
// support matrix: for every directive the Mosquitto manual documents
// for the loaded broker version, whether MCM manages it, partially
// supports it or does not support it.
//
// The matrix is the machine-readable form of the published page at
// docs/support-matrix.md. It states the exact coverage of MCM so no
// consumer has to assume universal compatibility.
func (b *brokerConfigAPI) handleSupportMatrix(w http.ResponseWriter, r *http.Request) {
	if b.catalog == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "broker config catalog is not loaded"})
		return
	}
	version := b.catalog.VersionName()
	upstream, err := catalog.UpstreamDirectives(version)
	if err != nil {
		// A broker version without an upstream inventory has no
		// matrix to publish. Saying so is honest; inventing an empty
		// matrix would look like "MCM manages nothing".
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{
			Error: "no upstream directive inventory for broker version " + version,
		})
		return
	}
	matrix, err := catalog.SupportMatrix(b.catalog, upstream)
	if err != nil {
		slog.Error("support matrix build failed", "version", version, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	counts := matrix.Counts()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": matrix.Version,
		"counts": map[string]int{
			"managed":     counts[catalog.SupportManaged],
			"partial":     counts[catalog.SupportPartial],
			"unsupported": counts[catalog.SupportUnsupported],
		},
		"directives": matrix.Entries,
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

// decodeBrokerImport accepts an optional draft body; an omitted body imports
// the current configured root. A request cannot select an arbitrary path.
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
