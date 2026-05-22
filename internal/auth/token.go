package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// UserClaims contains authenticated user identity.
type UserClaims struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Role     Role   `json:"role"`
}

// TokenManager issues and verifies JWTs.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
}

// NewTokenManager creates a JWT manager using an HMAC secret and token TTL.
func NewTokenManager(secret string, ttl time.Duration) *TokenManager {
	return &TokenManager{
		secret: []byte(secret),
		ttl:    ttl,
	}
}

// Issue creates a signed JWT for the user.
func (m *TokenManager) Issue(userID int64, username string, role Role, now time.Time) (string, time.Time, error) {
	expiresAt := now.Add(m.ttl)
	claims := jwt.MapClaims{
		"sub":      fmt.Sprintf("%d", userID),
		"username": username,
		"role":     string(role),
		"iat":      now.Unix(),
		"exp":      expiresAt.Unix(),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("sign token: %w", err)
	}

	return signed, expiresAt, nil
}

// IssueMFAChallenge signs a short-lived JWT identifying the user for the second
// login step. The "purpose" claim is mandatory so a challenge token can never be
// presented as a regular access token via Verify.
func (m *TokenManager) IssueMFAChallenge(userID int64, username string, ttl time.Duration, now time.Time) (string, error) {
	expiresAt := now.Add(ttl)
	claims := jwt.MapClaims{
		"sub":      fmt.Sprintf("%d", userID),
		"username": username,
		"purpose":  "mfa_challenge",
		"iat":      now.Unix(),
		"exp":      expiresAt.Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(m.secret)
	if err != nil {
		return "", fmt.Errorf("sign mfa challenge: %w", err)
	}
	return signed, nil
}

// VerifyMFAChallenge validates a challenge token and returns the user identity.
// Regular access tokens (which carry no purpose) are rejected.
func (m *TokenManager) VerifyMFAChallenge(tokenString string, now time.Time) (int64, string, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		return 0, "", fmt.Errorf("parse mfa challenge: %w", err)
	}
	if !token.Valid {
		return 0, "", fmt.Errorf("invalid mfa challenge")
	}
	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, "", fmt.Errorf("invalid mfa challenge claims")
	}
	purpose, _ := mapClaims["purpose"].(string)
	if purpose != "mfa_challenge" {
		return 0, "", fmt.Errorf("token is not an mfa challenge")
	}
	sub, err := mapClaims.GetSubject()
	if err != nil {
		return 0, "", fmt.Errorf("read mfa challenge subject: %w", err)
	}
	var userID int64
	if _, err := fmt.Sscanf(sub, "%d", &userID); err != nil {
		return 0, "", fmt.Errorf("parse mfa challenge subject: %w", err)
	}
	username, _ := mapClaims["username"].(string)
	if username == "" {
		return 0, "", fmt.Errorf("mfa challenge username claim is required")
	}
	return userID, username, nil
}

// Verify validates a signed JWT and returns the user identity claims.
func (m *TokenManager) Verify(tokenString string) (UserClaims, error) {
	return m.VerifyAt(tokenString, time.Now())
}

// VerifyAt validates a signed JWT using the provided clock instant.
func (m *TokenManager) VerifyAt(tokenString string, now time.Time) (UserClaims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		return UserClaims{}, fmt.Errorf("parse token: %w", err)
	}
	if !token.Valid {
		return UserClaims{}, fmt.Errorf("invalid token")
	}

	mapClaims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return UserClaims{}, fmt.Errorf("invalid token claims")
	}

	sub, err := mapClaims.GetSubject()
	if err != nil {
		return UserClaims{}, fmt.Errorf("read token subject: %w", err)
	}

	var userID int64
	if _, err := fmt.Sscanf(sub, "%d", &userID); err != nil {
		return UserClaims{}, fmt.Errorf("parse token subject: %w", err)
	}

	username, _ := mapClaims["username"].(string)
	if username == "" {
		return UserClaims{}, fmt.Errorf("token username claim is required")
	}

	if purpose, _ := mapClaims["purpose"].(string); purpose != "" {
		return UserClaims{}, fmt.Errorf("token has unexpected purpose %q", purpose)
	}

	roleString, _ := mapClaims["role"].(string)
	role, err := ParseRole(roleString, RoleAdmin)
	if err != nil {
		return UserClaims{}, fmt.Errorf("parse token role claim: %w", err)
	}

	return UserClaims{
		UserID:   userID,
		Username: username,
		Role:     role,
	}, nil
}
