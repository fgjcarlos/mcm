package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fgjcarlos/mcm/internal/certificate"
	"github.com/fgjcarlos/mcm/internal/mosquitto/certs"
)

// certificateServicer is the certificate lifecycle contract used by the HTTP handlers.
//
// The handlers never touch the private key beyond handing it to the service:
// the uploaded key is write-only material and no response carries it.
type certificateServicer interface {
	CreateRevision(ctx context.Context, actor string, profileName string, description string, input certs.Input, requireCA bool) (certificate.RevisionView, error)
	ListProfiles(ctx context.Context) ([]certificate.ProfileView, error)
	Preview(ctx context.Context, actor string, profileName string, revisionID string) (certificate.PreviewView, error)
	Apply(ctx context.Context, actor string, profileName string, revisionID string) (certificate.ApplyResult, error)
}

type certificateAPI struct {
	svc certificateServicer
}

// certificateRevisionRequest carries an uploaded certificate material.
//
// The private key is accepted once and never returned.
type certificateRevisionRequest struct {
	CACertPEM     string   `json:"ca_cert_pem"`
	ServerCertPEM string   `json:"server_cert_pem"`
	ServerKeyPEM  string   `json:"server_key_pem"`
	ChainPEM      string   `json:"chain_pem"`
	ExpectedNames []string `json:"expected_names"`
	Description   string   `json:"description"`
	RequireCA     bool     `json:"require_ca"`
}

// certificateActionRequest selects the revision of an action.
type certificateActionRequest struct {
	Profile    string `json:"profile"`
	RevisionID string `json:"revision_id"`
}

func (a *certificateAPI) handleList(w http.ResponseWriter, r *http.Request) {
	profiles, err := a.svc.ListProfiles(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if profiles == nil {
		profiles = []certificate.ProfileView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (a *certificateAPI) handleCreateRevision(w http.ResponseWriter, r *http.Request) {
	var request certificateRevisionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	profileName := strings.TrimSpace(r.PathValue("name"))
	input := certs.Input{
		CACertPEM:     request.CACertPEM,
		ServerCertPEM: request.ServerCertPEM,
		ServerKeyPEM:  request.ServerKeyPEM,
		ChainPEM:      request.ChainPEM,
		ExpectedNames: request.ExpectedNames,
	}
	view, err := a.svc.CreateRevision(r.Context(), requestActor(r), profileName, request.Description, input, request.RequireCA)
	if err != nil {
		var validation *certificate.ValidationError
		switch {
		case errors.As(err, &validation):
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"error":    validation.Error(),
				"issues":   validation.Issues,
				"warnings": validation.Warnings,
			})
		case errors.Is(err, certificate.ErrInvalidProfileName):
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid certificate profile name"})
		default:
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		}
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

func (a *certificateAPI) handlePreview(w http.ResponseWriter, r *http.Request) {
	var request certificateActionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	preview, err := a.svc.Preview(r.Context(), requestActor(r), request.Profile, request.RevisionID)
	switch {
	case errors.Is(err, certificate.ErrProfileNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "certificate profile not found"})
	case errors.Is(err, certificate.ErrRevisionNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "certificate revision not found"})
	case errors.Is(err, certificate.ErrInvalidProfileName):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid certificate profile name"})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
	default:
		writeJSON(w, http.StatusOK, preview)
	}
}

func (a *certificateAPI) handleApply(w http.ResponseWriter, r *http.Request) {
	var request certificateActionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
			return
		}
	}

	result, err := a.svc.Apply(r.Context(), requestActor(r), request.Profile, request.RevisionID)
	switch {
	case errors.Is(err, certificate.ErrProfileNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "certificate profile not found"})
	case errors.Is(err, certificate.ErrRevisionNotFound):
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "certificate revision not found"})
	case errors.Is(err, certificate.ErrInvalidProfileName):
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid certificate profile name"})
	case errors.Is(err, certificate.ErrRevisionActive):
		writeJSON(w, http.StatusConflict, errorResponse{Error: "the certificate revision is already active"})
	case err != nil:
		// The activation failed and the previous material was restored.
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"error":  "the broker did not serve the new certificate; the previous material was restored",
			"detail": err.Error(),
			"result": result,
		})
	default:
		writeJSON(w, http.StatusOK, result)
	}
}

// requestActor returns the username of the authenticated caller.
func requestActor(r *http.Request) string {
	if claims, ok := currentUserFromContext(r.Context()); ok && strings.TrimSpace(claims.Username) != "" {
		return claims.Username
	}
	return "anonymous"
}
