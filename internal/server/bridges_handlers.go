package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/mosquitto/bridges"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// bridgeServicer is the bridge lifecycle contract used by the HTTP
// handlers. Production wires the deploy service; tests substitute an
// in-memory fake.
type bridgeServicer interface {
	GetBridges(ctx context.Context) (deploy.BridgesResult, error)
	PreviewBridges(ctx context.Context, actor string, doc bridges.Document) (deploy.PreviewResult, error)
	ApplyBridges(ctx context.Context, actor, revisionID string, force, adopt bool) (storage.Deployment, error)
}

// bridgeAPI groups the HTTP handlers of the bridge surface (issue #303).
type bridgeAPI struct {
	svc bridgeServicer
}

// bridgePreviewRequest is the body of POST /api/v1/broker/bridges/preview.
// The operator sends the complete set of bridges they want; MCM replaces
// the connection sections of the managed configuration with them.
type bridgePreviewRequest struct {
	Bridges []bridges.Bridge `json:"bridges"`
}

// bridgeApplyRequest is the body of POST /api/v1/broker/bridges/apply.
type bridgeApplyRequest struct {
	RevisionID string `json:"revision_id"`
	Force      bool   `json:"force,omitempty"`
	Adopt      bool   `json:"adopt,omitempty"`
}

// handleList serves GET /api/v1/broker/bridges — the bridge connections
// declared in the managed mosquitto.conf.
//
// The response never carries a credential: a password is replaced by the
// flag that says whether one is set.
func (b *bridgeAPI) handleList(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "bridge management is not configured"})
		return
	}
	result, err := b.svc.GetBridges(r.Context())
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "bridge management is not configured"})
		return
	}
	if err != nil {
		slog.Error("bridge read failed", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if result.Bridges == nil {
		result.Bridges = []bridges.Bridge{}
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePreview serves POST /api/v1/broker/bridges/preview — it validates
// the desired bridges, renders them into the managed configuration and
// stores the result as an immutable revision the operator can review.
//
// The diff and the rendered body are redacted: a bridge password never
// travels through a response.
func (b *bridgeAPI) handlePreview(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "bridge management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var request bridgePreviewRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	result, err := b.svc.PreviewBridges(r.Context(), actor, bridges.Document{Bridges: request.Bridges})
	var bridgesErr *deploy.BridgesError
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "bridge management is not configured"})
	case errors.As(err, &bridgesErr):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "bridge configuration is invalid",
			"issues": bridgesErr.Issues,
		})
	case err != nil:
		slog.Error("bridge preview failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// handleApply serves POST /api/v1/broker/bridges/apply — it writes the
// reviewed revision to the managed configuration and signals the broker.
//
// Mosquitto reads a bridge at start-up, so the apply refuses the revision
// with 409 when the change needs a restart and the operator did not
// confirm it.
func (b *bridgeAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	if b.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "bridge management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var request bridgeApplyRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	if request.RevisionID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "revision_id is required; preview the bridges first"})
		return
	}
	dep, err := b.svc.ApplyBridges(r.Context(), actor, request.RevisionID, request.Force, request.Adopt)
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "bridge management is not configured"})
	case errors.Is(err, deploy.ErrRevisionMissing):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "revision_id is required; preview the bridges first"})
	case errors.Is(err, deploy.ErrRequiresRestart):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":      "apply requires broker restart",
			"directives": directivesFromRestartErr(err),
		})
	case errors.Is(err, deploy.ErrAdoptionRequired):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "broker config adoption required; call POST /api/v1/broker/config/adopt first or pass adopt=true"})
	case errors.Is(err, deploy.ErrRevisionMismatch):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision base no longer matches the on-disk configuration"})
	case errors.Is(err, deploy.ErrRevisionExpired):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision has expired; preview the bridges again"})
	case errors.Is(err, deploy.ErrRevisionConsumed):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision was already applied"})
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "deploy already in progress"})
	case err != nil:
		slog.Error("bridge apply failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, dep)
	}
}
