package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/fgjcarlos/mcm/internal/listener"
	"github.com/fgjcarlos/mcm/internal/mosquitto/listeners"
	"github.com/fgjcarlos/mcm/internal/storage"
)

// listenerServicer is the listener lifecycle contract used by the HTTP handlers.
type listenerServicer interface {
	List(ctx context.Context) ([]storage.ListenerSpecRow, error)
	Preview(ctx context.Context, desired []listeners.ListenerSpec, opts listener.PreviewOptions) (listener.ListenerPreviewResult, error)
	Apply(ctx context.Context, revisionID string, confirm bool) error
}

type listenerAPI struct {
	svc listenerServicer
}

type listenerPreviewRequest struct {
	Specs   []listeners.ListenerSpec `json:"specs"`
	Confirm bool                     `json:"confirm"`
}

type listenerApplyRequest struct {
	RevisionID string `json:"revision_id"`
	Confirm    bool   `json:"confirm"`
}

func (a *listenerAPI) handleList(w http.ResponseWriter, r *http.Request) {
	specs, err := a.svc.List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if specs == nil {
		specs = []storage.ListenerSpecRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"specs": specs})
}

func (a *listenerAPI) handlePreview(w http.ResponseWriter, r *http.Request) {
	var request listenerPreviewRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	result, err := a.svc.Preview(r.Context(), request.Specs, listener.PreviewOptions{Confirm: request.Confirm})
	if len(result.Issues) > 0 {
		writeJSON(w, http.StatusBadRequest, result)
		return
	}
	if errors.Is(err, listener.ErrListenerComposeUnmapped) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":          "listener ports are not mapped by compose",
			"compose_status": result.ComposeStatus,
		})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (a *listenerAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	var request listenerApplyRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	err := a.svc.Apply(r.Context(), request.RevisionID, request.Confirm)
	switch {
	case errors.Is(err, listener.ErrListenerPreviewNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "listener preview not found"})
	case errors.Is(err, listener.ErrListenerPreviewExpired):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "listener preview expired; request another preview"})
	case errors.Is(err, listener.ErrListenerPreviewConsumed):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "listener preview was already applied"})
	case errors.Is(err, listener.ErrListenerRestartNotConfirmed):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "listener restart confirmation is required"})
	case errors.Is(err, listener.ErrListenerComposeUnmapped):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "listener ports are not mapped by compose"})
	case errors.Is(err, listener.ErrListenerRestartFailed):
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: "broker restart failed; rolled back"})
	case errors.Is(err, listener.ErrListenerRestartConfigMissing):
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "listener restart configuration is missing"})
	case errors.Is(err, listener.ErrListenerApplyInProgress):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "listener apply already in progress"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"revision_id": request.RevisionID, "applied": true})
	}
}
