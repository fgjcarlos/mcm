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

// TestIssueMFAChallenge verifies MFA challenge token creation.
func TestIssueMFAChallenge(t *testing.T) {
	issuedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", time.Hour)

	tests := []struct {
		name      string
		userID    int64
		username  string
		ttl       time.Duration
		wantError bool
	}{
		{
			name:      "valid challenge",
			userID:    123,
			username:  "alice",
			ttl:       5 * time.Minute,
			wantError: false,
		},
		{
			name:      "different user",
			userID:    456,
			username:  "bob",
			ttl:       5 * time.Minute,
			wantError: false,
		},
		{
			name:      "empty username",
			userID:    789,
			username:  "",
			ttl:       5 * time.Minute,
			wantError: false, // token is issued, but VerifyMFAChallenge will reject empty username
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := manager.IssueMFAChallenge(tt.userID, tt.username, tt.ttl, issuedAt)
			if (err != nil) != tt.wantError {
				t.Fatalf("IssueMFAChallenge error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil && token == "" {
				t.Fatal("IssueMFAChallenge returned empty token")
			}
		})
	}
}

// TestVerifyMFAChallenge verifies MFA challenge token validation.
func TestVerifyMFAChallenge(t *testing.T) {
	issuedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", 10*time.Minute)

	// Create a valid challenge
	validToken, err := manager.IssueMFAChallenge(42, "alice", 10*time.Minute, issuedAt)
	if err != nil {
		t.Fatalf("IssueMFAChallenge returned error: %v", err)
	}

	tests := []struct {
		name      string
		token     string
		now       time.Time
		wantID    int64
		wantUser  string
		wantError bool
	}{
		{
			name:      "valid challenge",
			token:     validToken,
			now:       issuedAt.Add(5 * time.Minute),
			wantID:    42,
			wantUser:  "alice",
			wantError: false,
		},
		{
			name:      "challenge at expiry",
			token:     validToken,
			now:       issuedAt.Add(10 * time.Minute),
			wantID:    42,
			wantUser:  "alice",
			wantError: true, // expired
		},
		{
			name:      "challenge after expiry",
			token:     validToken,
			now:       issuedAt.Add(15 * time.Minute),
			wantID:    0,
			wantUser:  "",
			wantError: true,
		},
		{
			name:      "invalid token signature",
			token:     "invalid.token.string",
			now:       issuedAt,
			wantID:    0,
			wantUser:  "",
			wantError: true,
		},
		{
			name:      "empty token",
			token:     "",
			now:       issuedAt,
			wantID:    0,
			wantUser:  "",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			userID, username, err := manager.VerifyMFAChallenge(tt.token, tt.now)
			if (err != nil) != tt.wantError {
				t.Fatalf("VerifyMFAChallenge error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil {
				if userID != tt.wantID {
					t.Errorf("userID = %d, want %d", userID, tt.wantID)
				}
				if username != tt.wantUser {
					t.Errorf("username = %q, want %q", username, tt.wantUser)
				}
			}
		})
	}
}

// TestTokenManagerVerify verifies JWT token validation without explicit clock.
func TestTokenManagerVerify(t *testing.T) {
	now := time.Now()
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", time.Hour)

	tests := []struct {
		name       string
		buildToken func() string
		wantError  bool
		wantID     int64
		wantUser   string
		wantRole   Role
	}{
		{
			name: "valid token",
			buildToken: func() string {
				token, _, err := manager.Issue(99, "testuser", RoleOperator, now)
				if err != nil {
					t.Fatalf("Issue returned error: %v", err)
				}
				return token
			},
			wantError: false,
			wantID:    99,
			wantUser:  "testuser",
			wantRole:  RoleOperator,
		},
		{
			name: "invalid token",
			buildToken: func() string {
				return "invalid.token.string"
			},
			wantError: true,
		},
		{
			name: "empty token",
			buildToken: func() string {
				return ""
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := tt.buildToken()
			claims, err := manager.Verify(token)
			if (err != nil) != tt.wantError {
				t.Fatalf("Verify error = %v, wantError %v", err, tt.wantError)
			}
			if err == nil {
				if claims.UserID != tt.wantID {
					t.Errorf("UserID = %d, want %d", claims.UserID, tt.wantID)
				}
				if claims.Username != tt.wantUser {
					t.Errorf("Username = %q, want %q", claims.Username, tt.wantUser)
				}
				if claims.Role != tt.wantRole {
					t.Errorf("Role = %v, want %v", claims.Role, tt.wantRole)
				}
			}
		})
	}
}

// TestVerifyRejectsMFAChallengeToken verifies that Verify rejects MFA challenge tokens.
func TestVerifyRejectsMFAChallengeToken(t *testing.T) {
	issuedAt := time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)
	manager := NewTokenManager("0123456789abcdef0123456789abcdef", time.Hour)

	// Create a challenge token (which has purpose="mfa_challenge")
	challengeToken, err := manager.IssueMFAChallenge(42, "alice", 5*time.Minute, issuedAt)
	if err != nil {
		t.Fatalf("IssueMFAChallenge returned error: %v", err)
	}

	// Verify should reject challenge tokens
	if _, err := manager.VerifyAt(challengeToken, issuedAt); err == nil {
		t.Fatal("Verify accepted MFA challenge token, should reject")
	}
}
