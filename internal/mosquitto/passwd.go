package mosquitto

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

// DefaultIterations is the default PBKDF2 iteration count for Mosquitto $7$ hashes.
const DefaultIterations = 101

// PasswdEntry represents a single entry in a Mosquitto password file.
type PasswdEntry struct {
	Username string
	Hash     string // "$7$..." portion only, NOT including username
}

// HashPassword returns a Mosquitto-compatible PBKDF2-SHA512 password hash.
// Format: "$7$<iterations>$<base64-salt>$<base64-hash>"
// Uses 12-byte random salt (crypto/rand), 64-byte derived key, base64.StdEncoding (WITH padding).
// Does NOT include the username in the output.
func HashPassword(password string, iterations int) (string, error) {
	salt := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("mosquitto: generate salt: %w", err)
	}

	key := pbkdf2.Key([]byte(password), salt, iterations, 64, sha512.New)

	encodedSalt := base64.StdEncoding.EncodeToString(salt)
	encodedKey := base64.StdEncoding.EncodeToString(key)

	hash := fmt.Sprintf("$7$%d$%s$%s", iterations, encodedSalt, encodedKey)
	return hash, nil
}

// parseHash splits a "$7$<iter>$<salt>$<hash>" string into components.
func parseHash(hash string) (iterations int, salt []byte, key []byte, err error) {
	// Split on "$" — expected: ["", "7", "<iter>", "<salt>", "<hash>"]
	parts := strings.Split(hash, "$")
	if len(parts) != 5 || parts[0] != "" || parts[1] != "7" {
		return 0, nil, nil, fmt.Errorf("mosquitto: invalid hash format")
	}

	iterations, err = strconv.Atoi(parts[2])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("mosquitto: parse iterations: %w", err)
	}

	salt, err = base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("mosquitto: decode salt: %w", err)
	}
	if len(salt) != 12 {
		return 0, nil, nil, fmt.Errorf("mosquitto: salt must be 12 bytes, got %d", len(salt))
	}

	key, err = base64.StdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, nil, nil, fmt.Errorf("mosquitto: decode key: %w", err)
	}
	if len(key) != 64 {
		return 0, nil, nil, fmt.Errorf("mosquitto: key must be 64 bytes, got %d", len(key))
	}

	return iterations, salt, key, nil
}

// VerifyPassword checks whether password matches the Mosquitto $7$ hash.
// Uses constant-time comparison (subtle.ConstantTimeCompare).
func VerifyPassword(hash, password string) (bool, error) {
	iterations, salt, originalKey, err := parseHash(hash)
	if err != nil {
		return false, err
	}

	derived := pbkdf2.Key([]byte(password), salt, iterations, 64, sha512.New)
	if subtle.ConstantTimeCompare(derived, originalKey) == 1 {
		return true, nil
	}
	return false, nil
}

// RenderPasswdFile produces a Mosquitto-compatible password file body.
// Entries sorted by Username, one "username:hash" per line, trailing newline.
// Hash field contains "$7$..." portion. RenderPasswdFile composes "username:hash".
// Empty input returns "".
func RenderPasswdFile(entries []PasswdEntry) string {
	if len(entries) == 0 {
		return ""
	}

	// Copy slice to avoid mutating input.
	sorted := make([]PasswdEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Username < sorted[j].Username
	})

	var b strings.Builder
	for _, e := range sorted {
		b.WriteString(e.Username)
		b.WriteByte(':')
		b.WriteString(e.Hash)
		b.WriteByte('\n')
	}
	return b.String()
}

// ParsePasswdFile is the inverse of RenderPasswdFile: it reads a
// Mosquitto password file and returns the entries keyed by username.
// Empty input and comments return an empty slice. Malformed lines are
// skipped silently — the file is operator-managed and the renderer is
// responsible for re-emitting a clean copy on apply.
func ParsePasswdFile(body string) []PasswdEntry {
	if body == "" {
		return nil
	}
	var entries []PasswdEntry
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 || idx == len(line)-1 {
			continue
		}
		entries = append(entries, PasswdEntry{
			Username: strings.TrimSpace(line[:idx]),
			Hash:     strings.TrimSpace(line[idx+1:]),
		})
	}
	return entries
}
