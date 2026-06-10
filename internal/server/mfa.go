package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

const (
	mfaIssuer        = "MCM"
	recoveryCodeCount = 10
)

type mfaSetupResponse struct {
	OTPAuthURL    string   `json:"otpauth_url"`
	Secret        string   `json:"secret"`
	RecoveryCodes []string `json:"recovery_codes"`
}

type mfaVerifyRequest struct {
	Code string `json:"code"`
}

type mfaDisableRequest struct {
	Password string `json:"password"`
}

// handleMFASetup generates a fresh secret and recovery codes for the current user.
// The plaintext recovery codes are returned exactly once; only the hashes are stored.
// The new secret stays "pending" (not marked enabled) until verify lands a matching code.
func (a *App) handleMFASetup(w http.ResponseWriter, r *http.Request) {
	claims, ok := currentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	user, err := a.store.GetAdminUserByID(r.Context(), claims.UserID)
	if errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	enrollment, err := auth.NewMFAEnrollment(mfaIssuer, user.Username)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	codes, err := auth.GenerateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	hashes := make([]string, 0, len(codes))
	for _, code := range codes {
		hash, err := auth.HashRecoveryCode(code)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		hashes = append(hashes, hash)
	}

	// Persist the pending secret (enabled=false) and rotate the recovery codes atomically.
	if err := a.store.SetAdminUserMFA(r.Context(), user.ID, enrollment.Secret, false); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := a.store.ReplaceRecoveryCodes(r.Context(), user.ID, hashes); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordAuditFromRequest(r, "mfa.setup", "admin_user", strconv.FormatInt(user.ID, 10), "pending", map[string]any{"username": user.Username})

	writeJSON(w, http.StatusOK, mfaSetupResponse{
		OTPAuthURL:    enrollment.OTPAuthURL,
		Secret:        enrollment.Secret,
		RecoveryCodes: codes,
	})
}

// handleMFAVerify completes enrollment by validating a TOTP code generated from the
// pending secret. On success the user's mfa_enabled flag flips to true and future
// logins require either a TOTP code or one of the recovery codes returned by setup.
func (a *App) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	claims, ok := currentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	var req mfaVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "code is required"})
		return
	}

	user, err := a.store.GetAdminUserByID(r.Context(), claims.UserID)
	if errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if strings.TrimSpace(user.MFASecret) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "mfa setup is required before verification"})
		return
	}
	if !auth.VerifyTOTP(user.MFASecret, code, a.now().UTC()) {
		a.recordAuditFromRequest(r, "mfa.setup", "admin_user", strconv.FormatInt(user.ID, 10), "failure", map[string]any{"username": user.Username, "reason": "invalid_code"})
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid mfa code"})
		return
	}

	if err := a.store.SetAdminUserMFA(r.Context(), user.ID, user.MFASecret, true); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordAuditFromRequest(r, "mfa.setup", "admin_user", strconv.FormatInt(user.ID, 10), "success", map[string]any{"username": user.Username})
	a.recordSecurityChange(r, "mfa_enabled", "mfa_setup_complete", user.Username)

	w.WriteHeader(http.StatusNoContent)
}

// handleMFADisable removes the MFA secret and recovery codes after the operator
// re-authenticates with their password (the same protection used on sensitive
// account changes elsewhere in the industry).
func (a *App) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	claims, ok := currentUserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}

	var req mfaDisableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, http.StatusRequestEntityTooLarge, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "invalid request body"})
		return
	}
	if strings.TrimSpace(req.Password) == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "password is required"})
		return
	}

	user, err := a.store.GetAdminUserByID(r.Context(), claims.UserID)
	if errors.Is(err, storage.ErrUserNotFound) {
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "authentication required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	match, err := auth.VerifyPassword(user.PasswordHash, req.Password)
	if err != nil || !match {
		a.recordAuditFromRequest(r, "mfa.disable", "admin_user", strconv.FormatInt(user.ID, 10), "failure", map[string]any{"username": user.Username, "reason": "invalid_password"})
		a.recordSecurityFailure(r, "mfa_disable_failed", "invalid_password", user.Username)
		writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "invalid credentials"})
		return
	}

	if err := a.store.SetAdminUserMFA(r.Context(), user.ID, "", false); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}
	if err := a.store.DeleteRecoveryCodes(r.Context(), user.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
		return
	}

	a.recordAuditFromRequest(r, "mfa.disable", "admin_user", strconv.FormatInt(user.ID, 10), "success", map[string]any{"username": user.Username})
	a.recordSecurityChange(r, "mfa_disabled", "operator_request", user.Username)

	w.WriteHeader(http.StatusNoContent)
}
