package auth

import (
	"testing"
	"time"
)

// The action MFA proof is not an access token. It authorizes one sensitive
// operation, for a short time, on one session, with one live MFA factor.
func TestActionMFAProofIsBoundToSessionAndFactor(t *testing.T) {
	manager := NewTokenManager("test-secret", time.Hour)
	now := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	const userID = int64(42)
	const session = "Bearer session-one"
	const factor = "JBSWY3DPEHPK3PXP"

	proof, expires, err := manager.IssueActionMFA(userID, session, factor, now)
	if err != nil {
		t.Fatalf("issue action MFA: %v", err)
	}
	if !expires.Equal(now.Add(ActionMFATTL)) {
		t.Fatalf("expiry = %s, want %s", expires, now.Add(ActionMFATTL))
	}
	if err := manager.VerifyActionMFA(proof, userID, session, factor, now); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}
	if err := manager.VerifyActionMFA(proof, userID, "Bearer session-two", factor, now); err == nil {
		t.Fatal("proof accepted for a different session of the same account")
	}
	if err := manager.VerifyActionMFA(proof, userID, session, "OTHERSECRET", now); err == nil {
		t.Fatal("proof accepted for a different MFA factor")
	}
	if err := manager.VerifyActionMFA(proof, userID+1, session, factor, now); err == nil {
		t.Fatal("proof accepted for a different user")
	}
	if err := manager.VerifyActionMFA(proof+"x", userID, session, factor, now); err == nil {
		t.Fatal("tampered proof accepted")
	}
	if err := manager.VerifyActionMFA(proof, userID, session, factor, now.Add(ActionMFATTL+time.Second)); err == nil {
		t.Fatal("expired proof accepted")
	}
	if _, err := manager.VerifyAt(proof, now); err == nil {
		t.Fatal("proof accepted as an access token")
	}
}
