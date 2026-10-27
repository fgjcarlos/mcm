package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/fgjcarlos/mcm/internal/deploy"
	"github.com/fgjcarlos/mcm/internal/dynsec"
	"github.com/fgjcarlos/mcm/internal/mosquitto/plugins"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// pluginServicer is the plugin lifecycle contract used by the HTTP
// handlers. Production wires the deploy service; tests substitute an
// in-memory fake.
type pluginServicer interface {
	GetPlugins(ctx context.Context) (deploy.PluginsResult, error)
	PreviewPlugins(ctx context.Context, actor string, doc plugins.Document) (deploy.PreviewResult, error)
	ApplyPlugins(ctx context.Context, actor, revisionID string, force, adopt bool) (storage.Deployment, error)
}

// dynSecServicer is the dynamic security lifecycle contract used by the
// HTTP handlers. Production wires the deploy service; tests substitute
// an in-memory fake.
type dynSecServicer interface {
	GetDynSec(ctx context.Context) (deploy.DynSecResult, error)
	PreviewDynSec(ctx context.Context, actor string, doc dynsec.Document) (deploy.DynSecPreviewResult, error)
	ApplyDynSec(ctx context.Context, actor, planID string, desired *dynsec.Document) (deploy.DynSecApplyResult, error)
	AdoptDynSec(ctx context.Context, actor string) (deploy.DynSecPreviewResult, error)
}

// pluginAPI groups the HTTP handlers of the plugin surface (issue #304).
type pluginAPI struct {
	svc pluginServicer
}

// dynSecAPI groups the HTTP handlers of the dynamic security surface
// (issue #304).
type dynSecAPI struct {
	svc dynSecServicer
}

// pluginPreviewRequest is the body of POST /api/v1/broker/plugins/preview.
//
// The operator sends the complete set of plugins they want. A plugin or
// an option the catalog does not know is preserved as it is.
type pluginPreviewRequest struct {
	Plugins []plugins.Plugin `json:"plugins"`
}

// pluginApplyRequest is the body of POST /api/v1/broker/plugins/apply.
type pluginApplyRequest struct {
	RevisionID string `json:"revision_id"`
	Force      bool   `json:"force,omitempty"`
	Adopt      bool   `json:"adopt,omitempty"`
}

// dynSecPreviewRequest is the body of POST /api/v1/broker/dynsec/preview.
//
// The operator sends the complete security database they want. The
// passwords of the clients the plan creates travel with the request and
// never reach the store.
type dynSecPreviewRequest struct {
	Document dynsec.Document `json:"document"`
}

// dynSecApplyRequest is the body of POST /api/v1/broker/dynsec/apply.
//
// The document is optional for the adoption, which reads the state from
// the database of the plugin. It carries the passwords of the clients the
// plan creates.
type dynSecApplyRequest struct {
	PlanID   string           `json:"plan_id"`
	Document *dynsec.Document `json:"document,omitempty"`
}

// handleList serves GET /api/v1/broker/plugins — the catalog of the
// plugins MCM supports for the broker version together with the plugins
// the broker loads.
func (p *pluginAPI) handleList(w http.ResponseWriter, r *http.Request) {
	if p.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "plugin management is not configured"})
		return
	}
	result, err := p.svc.GetPlugins(r.Context())
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "plugin management is not configured"})
		return
	}
	if err != nil {
		slog.Error("plugin read failed", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if result.Catalog == nil {
		result.Catalog = []plugins.PluginSpec{}
	}
	if result.Document.Plugins == nil {
		result.Document.Plugins = []plugins.Plugin{}
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePreview serves POST /api/v1/broker/plugins/preview — it validates
// the desired plugins, renders them into the managed configuration and
// stores the result as an immutable revision the operator can review.
func (p *pluginAPI) handlePreview(w http.ResponseWriter, r *http.Request) {
	if p.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "plugin management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var request pluginPreviewRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	result, err := p.svc.PreviewPlugins(r.Context(), actor, plugins.Document{Plugins: request.Plugins})
	var pluginsErr *deploy.PluginsError
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "plugin management is not configured"})
	case errors.As(err, &pluginsErr):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "plugin configuration is invalid",
			"issues": pluginsErr.Issues,
		})
	case err != nil:
		slog.Error("plugin preview failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// handleApply serves POST /api/v1/broker/plugins/apply — it writes the
// reviewed revision to the managed configuration and signals the broker.
//
// Mosquitto loads a plugin at start-up, so the apply refuses the
// revision with 409 when the change needs a restart and the operator
// did not confirm it.
func (p *pluginAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	if p.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "plugin management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var request pluginApplyRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	if request.RevisionID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "revision_id is required; preview the plugins first"})
		return
	}
	dep, err := p.svc.ApplyPlugins(r.Context(), actor, request.RevisionID, request.Force, request.Adopt)
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "plugin management is not configured"})
	case errors.Is(err, deploy.ErrRevisionMissing):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "revision_id is required; preview the plugins first"})
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
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision has expired; preview the plugins again"})
	case errors.Is(err, deploy.ErrRevisionConsumed):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "preview revision was already applied"})
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "deploy already in progress"})
	case err != nil:
		slog.Error("plugin apply failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, dep)
	}
}

// handleGet serves GET /api/v1/broker/dynsec — the state of the security
// database of the plugin together with the report of the management
// channel.
func (d *dynSecAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	if d.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
		return
	}
	result, err := d.svc.GetDynSec(r.Context())
	if errors.Is(err, deploy.ErrBrokerConfigDisabled) {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
		return
	}
	if err != nil {
		slog.Error("dynamic security read failed", "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handlePreview serves POST /api/v1/broker/dynsec/preview — it compares
// the requested security database with the state of the broker and
// stores the plan as an immutable revision the operator can review.
//
// The preview refuses a change that would open the management channel of
// the plugin to clients that must not reach it.
func (d *dynSecAPI) handlePreview(w http.ResponseWriter, r *http.Request) {
	if d.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var request dynSecPreviewRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	result, err := d.svc.PreviewDynSec(r.Context(), actor, request.Document)
	var documentErr *deploy.DynSecDocumentError
	var guardErr *deploy.DynSecGuardError
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
	case errors.As(err, &documentErr):
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  "the dynamic security document is invalid",
			"issues": documentErr.Issues,
		})
	case errors.As(err, &guardErr):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "the management channel of the dynamic security plugin would be open",
			"guard": guardErr.Report,
		})
	case errors.Is(err, deploy.ErrDynSecNotAdopted):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the dynamic security backend has not been adopted; call POST /api/v1/broker/dynsec/adopt first"})
	case err != nil:
		slog.Error("dynamic security preview failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// handleApply serves POST /api/v1/broker/dynsec/apply — it runs the
// reviewed plan against the plugin and verifies that the broker ended up
// in the requested state.
func (d *dynSecAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	if d.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	var request dynSecApplyRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}
	if request.PlanID == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "plan_id is required; preview the change first"})
		return
	}
	result, err := d.svc.ApplyDynSec(r.Context(), actor, request.PlanID, request.Document)
	var guardErr *deploy.DynSecGuardError
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
	case errors.Is(err, deploy.ErrRevisionMissing):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "plan_id is required; preview the change first"})
	case errors.Is(err, storage.ErrDynSecPlanNotFound):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "the plan does not exist; preview the change again"})
	case errors.Is(err, storage.ErrDynSecPlanConsumed):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the plan was already applied"})
	case errors.Is(err, deploy.ErrDynSecBackendConflict):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the security database changed after the preview; review the change again"})
	case errors.As(err, &guardErr):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "the management channel of the dynamic security plugin is open",
			"guard": guardErr.Report,
		})
	case errors.Is(err, deploy.ErrDeployInProgress):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "deploy already in progress"})
	case err != nil:
		slog.Error("dynamic security apply failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// handleAdopt serves POST /api/v1/broker/dynsec/adopt — it prepares the
// migration from the file backend to the dynamic security backend.
//
// The adoption is never implicit: the operator asks for it, MCM shows
// what the migration brings, and the change only happens after the
// operator applied the plan.
func (d *dynSecAPI) handleAdopt(w http.ResponseWriter, r *http.Request) {
	if d.svc == nil {
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
		return
	}
	actor := actorFromRequest(r)
	result, err := d.svc.AdoptDynSec(r.Context(), actor)
	switch {
	case errors.Is(err, deploy.ErrBrokerConfigDisabled):
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "dynamic security management is not configured"})
	case errors.Is(err, deploy.ErrDynSecBackendConflict):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the broker already uses the dynamic security backend, or the broker account of MCM cannot administer it"})
	case err != nil:
		slog.Error("dynamic security adoption failed", "actor", actor, "error", err.Error())
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}
