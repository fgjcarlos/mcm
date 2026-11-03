package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const ActionMFATTL = 5 * time.Minute

func factorBinding(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// IssueActionMFA creates a short-lived proof, not an access token. It is bound
// to the authenticated session and current factor; rotating/disabling MFA
// invalidates the proof even while the access token remains valid.
func (m *TokenManager) IssueActionMFA(userID int64, session, secret string, now time.Time) (string, time.Time, error) {
	expires := now.Add(ActionMFATTL)
	claims := jwt.MapClaims{"sub": fmt.Sprint(userID), "purpose": "action_mfa", "session": factorBinding(session), "factor": factorBinding(secret), "iat": now.Unix(), "exp": expires.Unix()}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
	return signed, expires, err
}

func (m *TokenManager) VerifyActionMFA(proof string, userID int64, session, secret string, now time.Time) error {
	token, err := jwt.Parse(proof, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return m.secret, nil
	}, jwt.WithTimeFunc(func() time.Time { return now }), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return fmt.Errorf("invalid action MFA proof")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["purpose"] != "action_mfa" || claims["sub"] != fmt.Sprint(userID) || claims["session"] != factorBinding(session) || claims["factor"] != factorBinding(secret) {
		return fmt.Errorf("MFA proof binding mismatch")
	}
	return nil
}
