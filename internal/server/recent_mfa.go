package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/fgjcarlos/mcm/internal/auth"
	"github.com/fgjcarlos/mcm/internal/storage"
)

func (a *App) handleMFAStepUp(w http.ResponseWriter, r *http.Request) {
	claims, _ := currentUserFromContext(r.Context())
	if a.enforceLoginLockout(w, r, claims.Username) {
		return
	}
	var req mfaVerifyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if isMaxBytesError(err) {
			writeJSON(w, 413, errorResponse{Error: "request body too large"})
			return
		}
		writeJSON(w, 400, errorResponse{Error: "invalid request body"})
		return
	}
	user, err := a.loadCurrentUser(r.Context(), claims)
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	if !user.MFAEnabled || user.MFASecret == "" {
		a.denyRecentMFA(w, r)
		return
	}
	step, ok := auth.MatchTOTPStep(user.MFASecret, strings.TrimSpace(req.Code), a.now().UTC())
	if ok {
		err = a.store.ConsumeTOTPTimeStep(r.Context(), user.ID, step)
		if err != nil && !errors.Is(err, storage.ErrTOTPCodeReused) {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		ok = err == nil
	}
	if !ok {
		a.recordLoginAttempt(r, user.Username, false)
		a.recordAuditFromRequest(r, "mfa.step_up", "admin_user", "", "failure", nil)
		writeJSON(w, 401, errorResponse{Error: "invalid or reused mfa code"})
		return
	}
	proof, expires, err := a.tokens.IssueActionMFA(user.ID, r.Header.Get("Authorization"), user.MFASecret, a.now().UTC())
	if err != nil {
		writeJSON(w, 500, errorResponse{Error: "internal server error"})
		return
	}
	a.resetFailedLoginAttempts(r, user.Username)
	a.recordAuditFromRequest(r, "mfa.step_up", "admin_user", "", "success", nil)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"proof": proof, "expires_at": expires})
}

// requireRecentMFA is nested inside requireRole, so a proof cannot elevate
// privileges. It rechecks live enrollment instead of trusting JWT role/MFA data.
func (a *App) requireRecentMFA(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := currentUserFromContext(r.Context())
		if !ok {
			writeJSON(w, 401, errorResponse{Error: "authentication required"})
			return
		}
		user, err := a.loadCurrentUser(r.Context(), claims)
		if err != nil {
			writeJSON(w, 500, errorResponse{Error: "internal server error"})
			return
		}
		if !user.MFAEnabled || user.MFASecret == "" || a.tokens.VerifyActionMFA(r.Header.Get("X-MCM-MFA"), user.ID, r.Header.Get("Authorization"), user.MFASecret, a.now().UTC()) != nil {
			a.denyRecentMFA(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *App) denyRecentMFA(w http.ResponseWriter, r *http.Request) {
	a.recordAuditFromRequest(r, "mfa.required", "operation", r.URL.Path, "failure", nil)
	writeJSON(w, 403, struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}{"recent MFA verification required", "mfa_required"})
}
