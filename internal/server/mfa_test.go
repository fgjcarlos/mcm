package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pquerna/otp/totp"
)

// enrollAndEnableMFA drives /auth/mfa/setup + /auth/mfa/verify against the given
// admin user (already seeded with role admin) and returns the persisted secret plus
// the recovery codes returned by setup. The user ends up with mfa_enabled = true.
func enrollAndEnableMFA(t *testing.T, app *App, password, username string) (secret string, recoveryCodes []string) {
	t.Helper()

	token := loginAs(t, app, username, password)

	setupRec := httptest.NewRecorder()
	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/setup", nil)
	setupReq.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(setupRec, setupReq)
	if setupRec.Code != http.StatusOK {
		t.Fatalf("mfa setup status = %d, body = %s", setupRec.Code, setupRec.Body.String())
	}
	var setupResp mfaSetupResponse
	if err := json.NewDecoder(setupRec.Body).Decode(&setupResp); err != nil {
		t.Fatalf("decode mfa setup: %v", err)
	}
	if setupResp.Secret == "" || setupResp.OTPAuthURL == "" || len(setupResp.RecoveryCodes) != recoveryCodeCount {
		t.Fatalf("setup response missing fields: %#v", setupResp)
	}

	code, err := totp.GenerateCode(setupResp.Secret, app.now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	verifyBody, _ := json.Marshal(mfaVerifyRequest{Code: code})
	verifyRec := httptest.NewRecorder()
	verifyReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/verify", strings.NewReader(string(verifyBody)))
	verifyReq.Header.Set("Authorization", "Bearer "+token)
	verifyReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusNoContent {
		t.Fatalf("mfa verify status = %d, body = %s", verifyRec.Code, verifyRec.Body.String())
	}

	return setupResp.Secret, setupResp.RecoveryCodes
}

func TestMFASetupReturnsSecretAndRecoveryCodes(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	secret, codes := enrollAndEnableMFA(t, app, "secret-password", "admin")

	if secret == "" {
		t.Fatal("expected non-empty mfa secret")
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf("expected %d recovery codes, got %d", recoveryCodeCount, len(codes))
	}

	user, err := store.GetAdminUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("GetAdminUserByUsername: %v", err)
	}
	if !user.MFAEnabled {
		t.Fatal("user.MFAEnabled = false, want true after verify")
	}
	if user.MFASecret != secret {
		t.Fatalf("stored secret mismatch")
	}
}

func TestMFAVerifyRejectsInvalidCode(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	token := loginAs(t, app, "admin", "secret-password")

	// Trigger setup so a pending secret exists.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/setup", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup status = %d", rec.Code)
	}

	body, _ := json.Marshal(mfaVerifyRequest{Code: "000000"})
	verifyRec := httptest.NewRecorder()
	verifyReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/mfa/verify", strings.NewReader(string(body)))
	verifyReq.Header.Set("Authorization", "Bearer "+token)
	verifyReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusUnauthorized {
		t.Fatalf("verify status = %d, want %d", verifyRec.Code, http.StatusUnauthorized)
	}

	user, err := store.GetAdminUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("GetAdminUserByUsername: %v", err)
	}
	if user.MFAEnabled {
		t.Fatal("MFA should still be disabled after bad verify")
	}
}

func TestLoginReturnsMFAChallengeWhenEnabled(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	enrollAndEnableMFA(t, app, "secret-password", "admin")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	req.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp loginResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.MFARequired || resp.MFAChallenge == "" {
		t.Fatalf("expected mfa_required + mfa_challenge, got %#v", resp)
	}
	if resp.Token != "" {
		t.Fatalf("login should not issue an access token until MFA is verified; got token=%s", resp.Token)
	}
}

func TestLoginMFACompletesWithValidTOTP(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	secret, _ := enrollAndEnableMFA(t, app, "secret-password", "admin")

	// First step
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(loginRec, loginReq)
	var firstStep loginResponse
	_ = json.NewDecoder(loginRec.Body).Decode(&firstStep)
	if !firstStep.MFARequired {
		t.Fatalf("expected mfa_required, got %#v", firstStep)
	}

	code, err := totp.GenerateCode(secret, app.now().UTC())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	body, _ := json.Marshal(loginMFARequest{Challenge: firstStep.MFAChallenge, Code: code})
	mfaRec := httptest.NewRecorder()
	mfaReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/mfa", strings.NewReader(string(body)))
	mfaReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(mfaRec, mfaReq)
	if mfaRec.Code != http.StatusOK {
		t.Fatalf("mfa login status = %d, body = %s", mfaRec.Code, mfaRec.Body.String())
	}
	var finalResp loginResponse
	if err := json.NewDecoder(mfaRec.Body).Decode(&finalResp); err != nil {
		t.Fatalf("decode mfa login: %v", err)
	}
	if finalResp.Token == "" {
		t.Fatal("mfa login did not issue an access token")
	}
}

func TestLoginMFAAcceptsRecoveryCodeOnce(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	_, codes := enrollAndEnableMFA(t, app, "secret-password", "admin")

	tryRecoveryLogin := func(recoveryCode string) int {
		loginRec := httptest.NewRecorder()
		loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
		loginReq.Header.Set("Content-Type", "application/json")
		app.Handler().ServeHTTP(loginRec, loginReq)
		var firstStep loginResponse
		_ = json.NewDecoder(loginRec.Body).Decode(&firstStep)

		body, _ := json.Marshal(loginMFARequest{Challenge: firstStep.MFAChallenge, Code: recoveryCode})
		mfaRec := httptest.NewRecorder()
		mfaReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/mfa", strings.NewReader(string(body)))
		mfaReq.Header.Set("Content-Type", "application/json")
		app.Handler().ServeHTTP(mfaRec, mfaReq)
		return mfaRec.Code
	}

	if status := tryRecoveryLogin(codes[0]); status != http.StatusOK {
		t.Fatalf("first recovery use status = %d, want 200", status)
	}
	if status := tryRecoveryLogin(codes[0]); status != http.StatusUnauthorized {
		t.Fatalf("second recovery use status = %d, want 401 (single-use)", status)
	}
	if status := tryRecoveryLogin(codes[1]); status != http.StatusOK {
		t.Fatalf("second distinct recovery code status = %d, want 200", status)
	}
}

func TestMFADisableRequiresPassword(t *testing.T) {
	app, store := newTestApp(t)
	t.Cleanup(func() { _ = store.Close() })

	seedAdminUser(t, store, "admin", "secret-password", false)
	enrollAndEnableMFA(t, app, "secret-password", "admin")

	// Need a fresh access token after enrollment.
	loginRec := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"secret-password"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(loginRec, loginReq)
	var firstStep loginResponse
	_ = json.NewDecoder(loginRec.Body).Decode(&firstStep)
	stored, _ := store.GetAdminUserByUsername(context.Background(), "admin")
	code, _ := totp.GenerateCode(stored.MFASecret, app.now().UTC())
	stepBody, _ := json.Marshal(loginMFARequest{Challenge: firstStep.MFAChallenge, Code: code})
	mfaRec := httptest.NewRecorder()
	mfaReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login/mfa", strings.NewReader(string(stepBody)))
	mfaReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(mfaRec, mfaReq)
	var loggedIn loginResponse
	_ = json.NewDecoder(mfaRec.Body).Decode(&loggedIn)

	wrong, _ := json.Marshal(mfaDisableRequest{Password: "nope"})
	wrongRec := httptest.NewRecorder()
	wrongReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/mfa", strings.NewReader(string(wrong)))
	wrongReq.Header.Set("Authorization", "Bearer "+loggedIn.Token)
	wrongReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(wrongRec, wrongReq)
	if wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password disable status = %d, want 401", wrongRec.Code)
	}

	good, _ := json.Marshal(mfaDisableRequest{Password: "secret-password"})
	goodRec := httptest.NewRecorder()
	goodReq := httptest.NewRequest(http.MethodDelete, "/api/v1/auth/mfa", strings.NewReader(string(good)))
	goodReq.Header.Set("Authorization", "Bearer "+loggedIn.Token)
	goodReq.Header.Set("Content-Type", "application/json")
	app.Handler().ServeHTTP(goodRec, goodReq)
	if goodRec.Code != http.StatusNoContent {
		t.Fatalf("correct password disable status = %d, body = %s", goodRec.Code, goodRec.Body.String())
	}

	finalUser, _ := store.GetAdminUserByUsername(context.Background(), "admin")
	if finalUser.MFAEnabled {
		t.Fatal("MFA still enabled after disable")
	}
	if finalUser.MFASecret != "" {
		t.Fatal("MFA secret not cleared after disable")
	}
}
