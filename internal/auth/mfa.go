package auth

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

// MFAEnrollment holds the data needed to bootstrap a new TOTP enrollment. The Secret
// is the unencoded shared secret kept on the server; the OTPAuthURL is what the
// authenticator app consumes (usually rendered as a QR code on the client).
type MFAEnrollment struct {
	Secret     string
	OTPAuthURL string
}

// NewMFAEnrollment generates a fresh TOTP secret for the given username/issuer pair.
func NewMFAEnrollment(issuer, username string) (MFAEnrollment, error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: username,
	})
	if err != nil {
		return MFAEnrollment{}, fmt.Errorf("generate totp secret: %w", err)
	}
	return MFAEnrollment{Secret: key.Secret(), OTPAuthURL: key.URL()}, nil
}

// VerifyTOTP checks code against secret at the given instant. Allows a one-step
// skew so a code that was valid 30s ago still works for slow networks.
func VerifyTOTP(secret, code string, now time.Time) bool {
	_, ok := MatchTOTPStep(secret, code, now)
	return ok
}

// MatchTOTPStep returns the accepted TOTP time-step counter for code. The step is
// persisted by callers to reject replay of a previously accepted TOTP window.
func MatchTOTPStep(secret, code string, now time.Time) (int64, bool) {
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(code) == "" {
		return 0, false
	}
	const period = int64(30)
	currentStep := now.UTC().Unix() / period
	for _, step := range []int64{currentStep, currentStep + 1, currentStep - 1} {
		if step < 0 {
			continue
		}
		candidate, err := totp.GenerateCodeCustom(secret, time.Unix(step*period, 0).UTC(), totp.ValidateOpts{
			Period:    uint(period),
			Digits:    6,
			Algorithm: 0, // SHA1, matches the default Google Authenticator profile.
		})
		if err == nil && candidate == strings.TrimSpace(code) {
			return step, true
		}
	}
	return 0, false
}

// GenerateRecoveryCodes returns count random recovery codes in groups of four hex
// characters separated by a dash (e.g. "A1B2-C3D4"). The plaintext codes are
// shown once to the operator; their bcrypt hashes are what the server persists.
func GenerateRecoveryCodes(count int) ([]string, error) {
	if count <= 0 {
		return nil, fmt.Errorf("count must be positive")
	}
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		var b [5]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("read random recovery code: %w", err)
		}
		// Base32 without padding yields 8 chars from 5 bytes — perfect for splitting in two groups of four.
		raw := strings.TrimRight(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), "=")
		codes = append(codes, raw[:4]+"-"+raw[4:])
	}
	return codes, nil
}

// HashRecoveryCode returns a bcrypt hash for a recovery code so the plaintext never lands on disk.
func HashRecoveryCode(code string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(strings.TrimSpace(strings.ToUpper(code))), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash recovery code: %w", err)
	}
	return string(hash), nil
}

// MatchRecoveryCode returns true when the operator-provided code matches the stored hash.
func MatchRecoveryCode(hash, code string) bool {
	if hash == "" || strings.TrimSpace(code) == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(strings.TrimSpace(strings.ToUpper(code)))) == nil
}
