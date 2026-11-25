package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

type setupRequest struct {
	Username             string `json:"username"`
	Password             string `json:"password"`
	PasswordConfirmation string `json:"password_confirmation"`
	SetupToken           string `json:"setup_token"`
}

type setupStatusResponse struct {
	Status string `json:"status"`
}

func (a *App) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	count, err := a.store.CountAdminUsers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	status := "required"
	if count > 0 {
		status = "complete"
	} else if !a.setupTokenConfigured {
		status = "token_not_configured"
	}
	writeJSON(w, http.StatusOK, setupStatusResponse{Status: status})
}

func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !a.setupTokenConfigured {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "setup token is not configured"})
		return
	}
	var req setupRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}

	digest := sha256.Sum256([]byte(req.SetupToken))
	if subtle.ConstantTimeCompare(digest[:], a.setupTokenDigest[:]) != 1 {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: "invalid setup token"})
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" || req.Password == "" || req.PasswordConfirmation == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "username, password, and password_confirmation are required"})
		return
	}
	if req.Password != req.PasswordConfirmation {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "passwords do not match"})
		return
	}
	if err := validateSetupPassword(username, req.Password); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error()})
		return
	}
	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	now := a.now().UTC()
	var session loginResponse
	user, err := a.store.CreateFirstAdminUser(r.Context(), storage.CreateAdminUserParams{
		Username: username, PasswordHash: passwordHash, Role: string(auth.RoleAdmin),
	}, func(user storage.AdminUser) error {
		role, err := auth.ParseRole(user.Role, auth.RoleAdmin)
		if err != nil {
			return err
		}
		token, expiresAt, err := a.tokens.Issue(user.ID, user.Username, role, now)
		if err != nil {
			return err
		}
		session = loginResponse{Token: token, ExpiresAt: expiresAt, User: toAdminUserResponse(user)}
		return nil
	})
	if errors.Is(err, storage.ErrAdminSetupComplete) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: "setup is complete; please log in"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	session.User = toAdminUserResponse(user)
	writeJSON(w, http.StatusCreated, session)
}

func validateSetupPassword(username, password string) error {
	trimmed := strings.TrimSpace(password)
	if len(trimmed) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	switch strings.ToLower(trimmed) {
	case "admin", "password", "changeme", "12345678", "change-this-admin-password", "replace-this-secret-with-at-least-32-characters":
		return errors.New("password is too common; choose a stronger password")
	}
	if strings.EqualFold(trimmed, strings.TrimSpace(username)) {
		return errors.New("password must not equal the username")
	}
	return nil
}
