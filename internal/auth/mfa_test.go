package auth

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TestNewMFAEnrollment verifies that NewMFAEnrollment generates a valid TOTP enrollment.
func TestNewMFAEnrollment(t *testing.T) {
	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}

	if enrollment.Secret == "" {
		t.Error("Secret is empty")
	}

	// Secret should be base32-like (after decoding)
	if !regexp.MustCompile(`^[A-Z2-7]+$`).MatchString(enrollment.Secret) {
		t.Errorf("Secret format unexpected: %q", enrollment.Secret)
	}

	if !strings.Contains(enrollment.OTPAuthURL, "otpauth://totp/") {
		t.Errorf("OTPAuthURL does not contain otpauth://totp/: %q", enrollment.OTPAuthURL)
	}

	if !strings.Contains(enrollment.OTPAuthURL, "testuser") {
		t.Errorf("OTPAuthURL does not contain username: %q", enrollment.OTPAuthURL)
	}
}

// TestVerifyTOTP tests TOTP verification with fixed time.
func TestVerifyTOTP(t *testing.T) {
	// Use a fixed time for deterministic TOTP
	fixedTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	// Generate a secret for testing
	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}
	secret := enrollment.Secret

	// Generate the current code at fixedTime
	currentCode, err := totp.GenerateCodeCustom(secret, fixedTime, totp.ValidateOpts{
		Period:    30,
		Digits:    6,
		Algorithm: 0, // SHA1
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom returned error: %v", err)
	}

	tests := []struct {
		name     string
		code     string
		now      time.Time
		wantPass bool
	}{
		{
			name:     "current step accepts code",
			code:     currentCode,
			now:      fixedTime,
			wantPass: true,
		},
		{
			name:     "previous step (30s ago) accepts code with skew",
			code:     currentCode,
			now:      fixedTime.Add(-30 * time.Second),
			wantPass: true,
		},
		{
			name:     "next step (30s future) accepts code with skew",
			code:     currentCode,
			now:      fixedTime.Add(30 * time.Second),
			wantPass: true,
		},
		{
			name:     "invalid code returns false",
			code:     "000000",
			now:      fixedTime,
			wantPass: false,
		},
		{
			name:     "empty code returns false",
			code:     "",
			now:      fixedTime,
			wantPass: false,
		},
		{
			name:     "empty secret returns false",
			code:     currentCode,
			now:      fixedTime,
			wantPass: false, // will fail due to empty secret
		},
		{
			name:     "code from 60+ seconds ago fails",
			code:     currentCode,
			now:      fixedTime.Add(-65 * time.Second),
			wantPass: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testSecret := secret
			if tt.name == "empty secret returns false" {
				testSecret = ""
			}

			result := VerifyTOTP(testSecret, tt.code, tt.now)
			if result != tt.wantPass {
				t.Errorf("VerifyTOTP returned %v, want %v", result, tt.wantPass)
			}
		})
	}
}

// TestMatchTOTPStep tests MatchTOTPStep to verify step matching and skew handling.
func TestMatchTOTPStep(t *testing.T) {
	fixedTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}
	secret := enrollment.Secret

	// Generate code for current, prev, and next steps
	currentStep := fixedTime.UTC().Unix() / 30
	prevStep := currentStep - 1
	nextStep := currentStep + 1

	currentCode, _ := totp.GenerateCodeCustom(secret, time.Unix(currentStep*30, 0).UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})
	prevCode, _ := totp.GenerateCodeCustom(secret, time.Unix(prevStep*30, 0).UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})
	nextCode, _ := totp.GenerateCodeCustom(secret, time.Unix(nextStep*30, 0).UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})

	tests := []struct {
		name        string
		secret      string
		code        string
		now         time.Time
		wantStep    int64
		wantOk      bool
		wantNonZero bool
	}{
		{
			name:        "current step code returns currentStep",
			secret:      secret,
			code:        currentCode,
			now:         fixedTime,
			wantStep:    currentStep,
			wantOk:      true,
			wantNonZero: true,
		},
		{
			name:        "next step code returns nextStep",
			secret:      secret,
			code:        nextCode,
			now:         fixedTime,
			wantStep:    nextStep,
			wantOk:      true,
			wantNonZero: true,
		},
		{
			name:        "previous step code returns prevStep",
			secret:      secret,
			code:        prevCode,
			now:         fixedTime,
			wantStep:    prevStep,
			wantOk:      true,
			wantNonZero: true,
		},
		{
			name:        "invalid code returns 0 and false",
			secret:      secret,
			code:        "000000",
			now:         fixedTime,
			wantStep:    0,
			wantOk:      false,
			wantNonZero: false,
		},
		{
			name:        "empty code returns 0 and false",
			secret:      secret,
			code:        "",
			now:         fixedTime,
			wantStep:    0,
			wantOk:      false,
			wantNonZero: false,
		},
		{
			name:        "empty secret returns 0 and false",
			secret:      "",
			code:        currentCode,
			now:         fixedTime,
			wantStep:    0,
			wantOk:      false,
			wantNonZero: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			step, ok := MatchTOTPStep(tt.secret, tt.code, tt.now)
			if ok != tt.wantOk {
				t.Errorf("ok = %v, want %v", ok, tt.wantOk)
			}
			if tt.wantNonZero && step != tt.wantStep {
				t.Errorf("step = %d, want %d", step, tt.wantStep)
			}
			if !tt.wantNonZero && step != 0 {
				t.Errorf("step = %d, want 0", step)
			}
		})
	}
}

// TestGenerateRecoveryCodes verifies recovery code generation format.
func TestGenerateRecoveryCodes(t *testing.T) {
	codes, err := GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes returned error: %v", err)
	}

	if len(codes) != 10 {
		t.Errorf("got %d codes, want 10", len(codes))
	}

	// Check that all codes are unique
	seen := make(map[string]bool)
	for _, code := range codes {
		if seen[code] {
			t.Errorf("duplicate code generated: %q", code)
		}
		seen[code] = true
	}

	// Check format: XXXXX-XXXXX (base32 without padding, uppercase)
	pattern := regexp.MustCompile(`^[A-Z2-7]{4}-[A-Z2-7]{4}$`)
	for i, code := range codes {
		if !pattern.MatchString(code) {
			t.Errorf("code[%d] = %q, does not match format", i, code)
		}
	}
}

// TestGenerateRecoveryCodesRejectsZero verifies that count must be positive.
func TestGenerateRecoveryCodesRejectsZero(t *testing.T) {
	tests := []struct {
		name  string
		count int
	}{
		{name: "count=0", count: 0},
		{name: "count=-1", count: -1},
		{name: "count=-100", count: -100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes, err := GenerateRecoveryCodes(tt.count)
			if err == nil {
				t.Error("expected error for non-positive count, got nil")
			}
			if codes != nil {
				t.Errorf("got %v, want nil", codes)
			}
		})
	}
}

// TestHashAndMatchRecoveryCode verifies hash and match behavior.
func TestHashAndMatchRecoveryCode(t *testing.T) {
	codes, err := GenerateRecoveryCodes(2)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes returned error: %v", err)
	}

	code := codes[0]

	hash, err := HashRecoveryCode(code)
	if err != nil {
		t.Fatalf("HashRecoveryCode returned error: %v", err)
	}

	tests := []struct {
		name      string
		hash      string
		code      string
		wantMatch bool
	}{
		{
			name:      "exact match succeeds",
			hash:      hash,
			code:      code,
			wantMatch: true,
		},
		{
			name:      "different code fails",
			hash:      hash,
			code:      codes[1],
			wantMatch: false,
		},
		{
			name:      "lowercase code matches uppercase hash",
			hash:      hash,
			code:      strings.ToLower(code),
			wantMatch: true,
		},
		{
			name:      "code with whitespace matches after trim",
			hash:      hash,
			code:      "  " + code + "  ",
			wantMatch: true,
		},
		{
			name:      "empty hash returns false",
			hash:      "",
			code:      code,
			wantMatch: false,
		},
		{
			name:      "empty code returns false",
			hash:      hash,
			code:      "",
			wantMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MatchRecoveryCode(tt.hash, tt.code)
			if result != tt.wantMatch {
				t.Errorf("MatchRecoveryCode returned %v, want %v", result, tt.wantMatch)
			}
		})
	}
}

// TestHashRecoveryCodeNormalizes verifies case-insensitive hashing.
func TestHashRecoveryCodeNormalizes(t *testing.T) {
	upper := "ABCD-EFGH"
	lower := "abcd-efgh"

	hashUpper, err := HashRecoveryCode(upper)
	if err != nil {
		t.Fatalf("HashRecoveryCode(upper) returned error: %v", err)
	}

	hashLower, err := HashRecoveryCode(lower)
	if err != nil {
		t.Fatalf("HashRecoveryCode(lower) returned error: %v", err)
	}

	// Both should match the same plaintext when normalized
	if !MatchRecoveryCode(hashUpper, lower) {
		t.Error("uppercase hash should match lowercase code after normalization")
	}
	if !MatchRecoveryCode(hashLower, upper) {
		t.Error("lowercase hash should match uppercase code after normalization")
	}
}

// TestMatchRecoveryCodeCaseInsensitive verifies case-insensitive matching.
func TestMatchRecoveryCodeCaseInsensitive(t *testing.T) {
	code := "ABCDE-FGHIJ"

	hash, err := HashRecoveryCode(code)
	if err != nil {
		t.Fatalf("HashRecoveryCode returned error: %v", err)
	}

	testCases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "exact match", input: code, want: true},
		{name: "lowercase", input: strings.ToLower(code), want: true},
		{name: "mixed case", input: "AbCdE-FgHiJ", want: true},
		{name: "with spaces", input: "  " + code + "  ", want: true},
		{name: "different code", input: "ZZZZZ-ZZZZZ", want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := MatchRecoveryCode(hash, tc.input)
			if result != tc.want {
				t.Errorf("MatchRecoveryCode returned %v, want %v", result, tc.want)
			}
		})
	}
}

// TestGenerateRecoveryCodesMultipleCalls verifies that each call produces different codes.
func TestGenerateRecoveryCodesMultipleCalls(t *testing.T) {
	codes1, err := GenerateRecoveryCodes(5)
	if err != nil {
		t.Fatalf("first GenerateRecoveryCodes returned error: %v", err)
	}

	codes2, err := GenerateRecoveryCodes(5)
	if err != nil {
		t.Fatalf("second GenerateRecoveryCodes returned error: %v", err)
	}

	// Check that codes from different calls are different (extremely unlikely to collide)
	for _, c1 := range codes1 {
		for _, c2 := range codes2 {
			if c1 == c2 {
				t.Errorf("collision between different calls: %q", c1)
			}
		}
	}
}

// TestVerifyTOTPWithWhitespace verifies that codes with whitespace are handled.
func TestVerifyTOTPWithWhitespace(t *testing.T) {
	fixedTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}
	secret := enrollment.Secret

	currentCode, err := totp.GenerateCodeCustom(secret, fixedTime, totp.ValidateOpts{
		Period:    30,
		Digits:    6,
		Algorithm: 0,
	})
	if err != nil {
		t.Fatalf("GenerateCodeCustom returned error: %v", err)
	}

	// Test with whitespace
	codeWithSpaces := "  " + currentCode + "  "
	result := VerifyTOTP(secret, codeWithSpaces, fixedTime)
	if !result {
		t.Error("VerifyTOTP should accept code with whitespace")
	}
}

// TestMatchTOTPStepWithWhitespace verifies whitespace handling in MatchTOTPStep.
func TestMatchTOTPStepWithWhitespace(t *testing.T) {
	fixedTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}
	secret := enrollment.Secret

	currentStep := fixedTime.UTC().Unix() / 30
	currentCode, _ := totp.GenerateCodeCustom(secret, time.Unix(currentStep*30, 0).UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})

	// Test with whitespace
	codeWithSpaces := "  " + currentCode + "  "
	step, ok := MatchTOTPStep(secret, codeWithSpaces, fixedTime)

	if !ok || step != currentStep {
		t.Errorf("MatchTOTPStep with whitespace failed: ok=%v, step=%d", ok, step)
	}
}

// TestMatchTOTPStepNegativeStepSkipped verifies that negative steps are skipped.
func TestMatchTOTPStepNegativeStepSkipped(t *testing.T) {
	// Use a time near epoch where currentStep - 1 would be negative
	veryEarlyTime := time.Unix(10, 0).UTC() // Epoch + 10 seconds

	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}
	secret := enrollment.Secret

	// Get the current step at this early time
	currentStep := veryEarlyTime.Unix() / 30
	if currentStep > 0 {
		// If currentStep is positive, we need an even earlier time
		veryEarlyTime = time.Unix(5, 0).UTC()
		currentStep = veryEarlyTime.Unix() / 30
	}

	// Generate code for a valid step in the past
	validStep := currentStep + 2 // Use +2 to ensure we don't hit negative
	code, _ := totp.GenerateCodeCustom(secret, time.Unix(validStep*30, 0).UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})

	// Now match at the very early time - the prev step would be negative
	step, ok := MatchTOTPStep(secret, code, veryEarlyTime)
	// This should either fail (because the code is from too far in future) or succeed
	// The important thing is it doesn't crash trying to check negative steps

	// Assert that the function completes without panic and returns a valid result
	// (either success with a non-negative step, or failure with step=0 and ok=false)
	if ok {
		if step < 0 {
			t.Errorf("MatchTOTPStep returned negative step=%d, should not happen", step)
		}
	} else {
		// Failure is expected for a code from too far in the future
		if step != 0 {
			t.Errorf("MatchTOTPStep failed but returned step=%d (expected 0 on failure)", step)
		}
	}
}

// TestHashRecoveryCodeWithLeadingTrailingSpaces verifies trimming.
func TestHashRecoveryCodeWithLeadingTrailingSpaces(t *testing.T) {
	code := "ABCD-EFGH"
	codeWithSpaces := "   " + code + "   "

	hash, err := HashRecoveryCode(codeWithSpaces)
	if err != nil {
		t.Fatalf("HashRecoveryCode returned error: %v", err)
	}

	// Both exact and spaced versions should match
	if !MatchRecoveryCode(hash, code) {
		t.Error("hash should match exact code")
	}
	if !MatchRecoveryCode(hash, codeWithSpaces) {
		t.Error("hash should match spaced code")
	}
}

// TestMatchTOTPStepReplay verifies replay behavior: the same code produces the same step counter.
// This test demonstrates that MatchTOTPStep returns the matched step for replay detection.
// The CALLER is responsible for persisting the step and rejecting reused steps —
// MatchTOTPStep itself does not reject replay; it returns the step for the caller to decide.
func TestMatchTOTPStepReplay(t *testing.T) {
	fixedTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	enrollment, err := NewMFAEnrollment("TestIssuer", "testuser")
	if err != nil {
		t.Fatalf("NewMFAEnrollment returned error: %v", err)
	}
	secret := enrollment.Secret

	// Generate a code for the current step
	currentStep := fixedTime.UTC().Unix() / 30
	code, _ := totp.GenerateCodeCustom(secret, time.Unix(currentStep*30, 0).UTC(), totp.ValidateOpts{
		Period: 30, Digits: 6, Algorithm: 0,
	})

	// First call: MatchTOTPStep succeeds and returns the step
	step1, ok1 := MatchTOTPStep(secret, code, fixedTime)
	if !ok1 {
		t.Fatalf("first MatchTOTPStep failed, ok=%v", ok1)
	}
	if step1 != currentStep {
		t.Errorf("first step = %d, want %d", step1, currentStep)
	}

	// Second call: MatchTOTPStep with the same code returns the same step
	step2, ok2 := MatchTOTPStep(secret, code, fixedTime)
	if !ok2 {
		t.Fatalf("second MatchTOTPStep failed, ok=%v", ok2)
	}
	if step2 != currentStep {
		t.Errorf("second step = %d, want %d", step2, currentStep)
	}

	// Both calls return the same step counter
	if step1 != step2 {
		t.Errorf("step changed across calls: %d vs %d", step1, step2)
	}

	// The caller must persist step1 and reject step2 if step2 == step1 (replay check).
	// This test demonstrates that MatchTOTPStep returns consistent steps for the same code,
	// allowing the caller to implement replay detection by comparing persisted steps.
	if step1 > 0 {
		t.Logf("Replay detection note: caller received step=%d twice. Caller should reject the second use of this step.", step1)
	}
}
