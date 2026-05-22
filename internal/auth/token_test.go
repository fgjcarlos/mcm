package auth

import (
	"testing"
	"time"
)

func TestTokenManagerVerifyAtUsesProvidedClock(t *testing.T) {
	issuedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", time.Hour)

	token, _, err := manager.Issue(42, "admin", RoleAdmin, issuedAt)
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	claims, err := manager.VerifyAt(token, issuedAt.Add(30*time.Minute))
	if err != nil {
		t.Fatalf("VerifyAt returned error: %v", err)
	}
	if claims.UserID != 42 {
		t.Fatalf("claims user id = %d, want 42", claims.UserID)
	}
	if claims.Username != "admin" {
		t.Fatalf("claims username = %q, want admin", claims.Username)
	}
}

func TestTokenManagerVerifyAtRejectsExpiredToken(t *testing.T) {
	issuedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", time.Hour)

	token, _, err := manager.Issue(42, "admin", RoleAdmin, issuedAt)
	if err != nil {
		t.Fatalf("Issue returned error: %v", err)
	}

	if _, err := manager.VerifyAt(token, issuedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("VerifyAt returned nil error for expired token")
	}
}
