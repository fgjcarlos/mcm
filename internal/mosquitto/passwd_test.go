package mosquitto

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestDefaultIterations(t *testing.T) {
	t.Parallel()
	if DefaultIterations != 101 {
		t.Fatalf("DefaultIterations = %d, want 101", DefaultIterations)
	}
}

func TestHashPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		password   string
		iterations int
	}{
		{
			name:       "output starts with $7$101$ for default iterations",
			password:   "secret",
			iterations: DefaultIterations,
		},
		{
			name:       "empty password is hashed",
			password:   "",
			iterations: DefaultIterations,
		},
		{
			name:       "custom iteration count",
			password:   "hunter2",
			iterations: 200,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hash, err := HashPassword(tc.password, tc.iterations)
			if err != nil {
				t.Fatalf("HashPassword() error = %v", err)
			}

			// Must start with $7$
			if !strings.HasPrefix(hash, "$7$") {
				t.Fatal("HashPassword() returned a value without the expected format prefix")
			}

			// Split and verify structure: ["", "7", "<iter>", "<salt>", "<key>"]
			parts := strings.Split(hash, "$")
			if len(parts) != 5 {
				t.Fatalf("HashPassword() has %d parts, want 5", len(parts))
			}
			if parts[1] != "7" {
				t.Fatalf("HashPassword() scheme = %q, want \"7\"", parts[1])
			}

			// Salt must decode to exactly 12 bytes.
			saltBytes, err := base64.StdEncoding.DecodeString(parts[3])
			if err != nil {
				t.Fatalf("salt decode error: %v", err)
			}
			if len(saltBytes) != 12 {
				t.Fatalf("salt length = %d, want 12", len(saltBytes))
			}

			// Key must decode to exactly 64 bytes.
			keyBytes, err := base64.StdEncoding.DecodeString(parts[4])
			if err != nil {
				t.Fatalf("key decode error: %v", err)
			}
			if len(keyBytes) != 64 {
				t.Fatalf("key length = %d, want 64", len(keyBytes))
			}
		})
	}

	// Two calls with same password produce different outputs (random salt).
	t.Run("random salt produces unique hashes", func(t *testing.T) {
		t.Parallel()
		h1, err := HashPassword("same-password", DefaultIterations)
		if err != nil {
			t.Fatalf("first HashPassword() error = %v", err)
		}
		h2, err := HashPassword("same-password", DefaultIterations)
		if err != nil {
			t.Fatalf("second HashPassword() error = %v", err)
		}
		if h1 == h2 {
			t.Fatal("HashPassword() returned identical hashes for two separate calls — salt must be random")
		}
	})
}

func TestVerifyPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		password string
		check    string
		wantOK   bool
		wantErr  bool
	}{
		{
			name:     "correct password verifies successfully",
			password: "secret",
			check:    "secret",
			wantOK:   true,
			wantErr:  false,
		},
		{
			name:     "wrong password does not verify",
			password: "secret",
			check:    "wrong",
			wantOK:   false,
			wantErr:  false,
		},
		{
			name:     "empty password hashed and verified correctly",
			password: "",
			check:    "",
			wantOK:   true,
			wantErr:  false,
		},
		{
			name:     "empty password does not match non-empty",
			password: "",
			check:    "notempty",
			wantOK:   false,
			wantErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hash, err := HashPassword(tc.password, DefaultIterations)
			if err != nil {
				t.Fatalf("HashPassword() error = %v", err)
			}

			ok, err := VerifyPassword(hash, tc.check)
			if tc.wantErr && err == nil {
				t.Fatal("VerifyPassword() expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("VerifyPassword() unexpected error: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("VerifyPassword() = %v, want %v", ok, tc.wantOK)
			}
		})
	}

	t.Run("malformed hash returns error", func(t *testing.T) {
		t.Parallel()

		malformed := []string{
			"notahash",
			"$6$101$abc$def",  // wrong scheme
			"$7$notanint$x$y", // non-numeric iterations
			"$7$101$!!!$!!!",  // invalid base64
			"",
		}

		for _, h := range malformed {
			ok, err := VerifyPassword(h, "password")
			if err == nil {
				t.Errorf("VerifyPassword(%q) expected error, got ok=%v err=nil", h, ok)
			}
			if ok {
				t.Errorf("VerifyPassword(%q) returned ok=true, want false on error", h)
			}
		}
	})
}

func TestRenderPasswdFileRejectsUnrepresentableEntries(t *testing.T) {
	t.Parallel()
	_, err := RenderPasswdFile([]PasswdEntry{{Username: "alice:admin", Hash: "$7$redacted"}})
	if err == nil || !strings.Contains(err.Error(), "username") {
		t.Fatalf("RenderPasswdFile() error = %v, want username field error", err)
	}
}

func renderPasswdForTest(t *testing.T, entries []PasswdEntry) string {
	t.Helper()
	body, err := RenderPasswdFile(entries)
	if err != nil {
		t.Fatalf("RenderPasswdFile(): %v", err)
	}
	return body
}

func TestRenderPasswdFile(t *testing.T) {
	t.Parallel()

	// Build a real hash for stable test entries.
	hashAlice, err := HashPassword("alice-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword for alice: %v", err)
	}
	hashBob, err := HashPassword("bob-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword for bob: %v", err)
	}
	hashZebra, err := HashPassword("zebra-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword for zebra: %v", err)
	}

	tests := []struct {
		name    string
		entries []PasswdEntry
		check   func(t *testing.T, got string)
	}{
		{
			name:    "empty input returns empty string",
			entries: []PasswdEntry{},
			check: func(t *testing.T, got string) {
				t.Helper()
				if got != "" {
					t.Fatalf("RenderPasswdFile([]) = %q, want \"\"", got)
				}
			},
		},
		{
			name:    "single entry has trailing newline and correct format",
			entries: []PasswdEntry{{Username: "alice", Hash: hashAlice}},
			check: func(t *testing.T, got string) {
				t.Helper()
				want := "alice:" + hashAlice + "\n"
				if got != want {
					t.Fatal("RenderPasswdFile() output did not match the expected structure")
				}
			},
		},
		{
			name: "multiple entries sorted by username",
			entries: []PasswdEntry{
				{Username: "zebra", Hash: hashZebra},
				{Username: "alice", Hash: hashAlice},
				{Username: "bob", Hash: hashBob},
			},
			check: func(t *testing.T, got string) {
				t.Helper()
				want := "alice:" + hashAlice + "\nbob:" + hashBob + "\nzebra:" + hashZebra + "\n"
				if got != want {
					t.Fatal("RenderPasswdFile() output did not match the expected structure")
				}
			},
		},
		{
			name: "each line format is username:$7$...",
			entries: []PasswdEntry{
				{Username: "alice", Hash: hashAlice},
				{Username: "bob", Hash: hashBob},
			},
			check: func(t *testing.T, got string) {
				t.Helper()
				lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
				if len(lines) != 2 {
					t.Fatalf("expected 2 lines, got %d: %q", len(lines), got)
				}
				for _, line := range lines {
					if !strings.Contains(line, ":") {
						t.Fatalf("line %q missing colon separator", line)
					}
					colonIdx := strings.Index(line, ":")
					hash := line[colonIdx+1:]
					if !strings.HasPrefix(hash, "$7$") {
						t.Fatal("hash field does not have the expected scheme")
					}
				}
				// Verify trailing newline.
				if !strings.HasSuffix(got, "\n") {
					t.Fatal("RenderPasswdFile() output does not end with newline")
				}
			},
		},
		{
			name: "does not mutate input slice order",
			entries: []PasswdEntry{
				{Username: "zebra", Hash: hashZebra},
				{Username: "alice", Hash: hashAlice},
			},
			check: func(t *testing.T, got string) {
				t.Helper()
				// The first entry in input should still be zebra after the call.
				// We verify this via the output being sorted (alice before zebra).
				if !strings.HasPrefix(got, "alice:") {
					t.Fatal("RenderPasswdFile() output was not sorted by username")
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Keep original order to verify no mutation.
			original := make([]PasswdEntry, len(tc.entries))
			copy(original, tc.entries)

			got := renderPasswdForTest(t, tc.entries)
			tc.check(t, got)

			// Verify input slice was not mutated.
			for i, e := range tc.entries {
				if e.Username != original[i].Username {
					t.Fatalf("RenderPasswdFile() mutated input[%d]: got %q, want %q", i, e.Username, original[i].Username)
				}
			}
		})
	}
}

func TestRenderPasswdFilePreservesUnicodeNormalizationForms(t *testing.T) {
	t.Parallel()
	entries := []PasswdEntry{{Username: "é", Hash: "hash-one"}, {Username: "e\u0301", Hash: "hash-two"}}
	body := renderPasswdForTest(t, entries)
	parsed, err := ParsePasswdFile(body)
	if err != nil {
		t.Fatalf("ParsePasswdFile(): %v", err)
	}
	if len(parsed) != 2 || parsed[0].Username == parsed[1].Username {
		t.Fatalf("passwd render did not preserve byte-distinct Unicode usernames; entries=%d", len(parsed))
	}
}

func TestRenderPasswdFileRejectsDuplicateUsernameWithoutLeakingHash(t *testing.T) {
	t.Parallel()
	_, err := RenderPasswdFile([]PasswdEntry{{Username: "alice", Hash: "hash-secret-one"}, {Username: "alice", Hash: "hash-secret-two"}})
	if err == nil || !strings.Contains(err.Error(), "username duplicates") {
		t.Fatalf("RenderPasswdFile() error = %v, want duplicate username field error", err)
	}
	if strings.Contains(err.Error(), "hash-secret") {
		t.Fatal("renderer error leaked password hash material")
	}
}

func TestParsePasswdFileRejectsDuplicateUsernamesAndSeparators(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"no-colon\n", "alice:hash-one\nalice:hash-two\n", "alice:hash:extra\n", " alice:hash\n"} {
		if _, err := ParsePasswdFile(body); err == nil {
			t.Error("ParsePasswdFile() accepted a collision or unrepresentable field")
		}
	}
}

func TestParsePasswdFile(t *testing.T) {
	t.Parallel()

	hashAlice, err := HashPassword("alice-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword for alice: %v", err)
	}
	hashBob, err := HashPassword("bob-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword for bob: %v", err)
	}

	tests := []struct {
		name    string
		body    string
		want    []PasswdEntry
		wantErr bool
	}{
		{
			name: "empty",
			body: "",
			want: nil,
		},
		{
			name: "only whitespace",
			body: "\n\n  \n",
			want: nil,
		},
		{
			name: "single entry",
			body: "alice:" + hashAlice + "\n",
			want: []PasswdEntry{{Username: "alice", Hash: hashAlice}},
		},
		{
			name: "two entries with comments",
			body: "# header\nalice:" + hashAlice + "\n# inline\nbob:" + hashBob + "\n",
			want: []PasswdEntry{
				{Username: "alice", Hash: hashAlice},
				{Username: "bob", Hash: hashBob},
			},
		},
		{
			name:    "malformed lines rejected",
			body:    "no-colon\n:missing-user\nmissing-hash:\nvalid:" + hashAlice,
			wantErr: true,
		},
		{
			name: "preserves insertion order",
			body: "bob:" + hashBob + "\nalice:" + hashAlice + "\n",
			want: []PasswdEntry{
				{Username: "bob", Hash: hashBob},
				{Username: "alice", Hash: hashAlice},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParsePasswdFile(tc.body)
			if tc.wantErr {
				if err == nil {
					t.Fatal("ParsePasswdFile() error = nil, want rejection")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePasswdFile(): %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("ParsePasswdFile() entry count = %d, want %d", len(got), len(tc.want))
			}
			for i, e := range got {
				if e.Username != tc.want[i].Username || e.Hash != tc.want[i].Hash {
					t.Errorf("ParsePasswdFile()[%d] did not preserve the expected fields", i)
				}
			}
		})
	}
}

// TestParseAndRenderPasswdFileRoundTrip documents that the two functions
// are exact inverses for non-malformed input — the deploy service uses
// this to preserve the broker service user's hash across applies instead
// of re-hashing on every render (which would defeat has_changes
// idempotency, issue #275 review).
func TestParseAndRenderPasswdFileRoundTrip(t *testing.T) {
	t.Parallel()
	hashAlice, err := HashPassword("alice-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	hashBob, err := HashPassword("bob-pass", DefaultIterations)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	original := []PasswdEntry{
		{Username: "bob", Hash: hashBob},
		{Username: "alice", Hash: hashAlice},
	}
	body := renderPasswdForTest(t, original)
	parsed, err := ParsePasswdFile(body)
	if err != nil {
		t.Fatalf("ParsePasswdFile(): %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("round-trip len = %d, want 2", len(parsed))
	}
	// RenderPasswdFile sorts by username; check we got them back in the right order.
	if parsed[0].Username != "alice" || parsed[1].Username != "bob" {
		t.Errorf("round-trip order: got %v %v, want alice bob", parsed[0].Username, parsed[1].Username)
	}
	for i, e := range parsed {
		switch e.Username {
		case "alice":
			if e.Hash != hashAlice {
				t.Errorf("alice hash drifted across round-trip")
			}
		case "bob":
			if e.Hash != hashBob {
				t.Errorf("bob hash drifted across round-trip")
			}
		}
		_ = i
	}
}
