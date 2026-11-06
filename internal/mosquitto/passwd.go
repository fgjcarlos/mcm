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
	"unicode"
	"unicode/utf8"

	"github.com/fgjcarlos/mcm/internal/acl"
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
// Entries are sorted by Username, one "username:hash" per line, with a trailing
// newline. It rejects entries that cannot be represented and exact username
// duplicates. NFC and NFD spellings are kept byte-distinct, not normalized.
// Empty input returns an empty body.
func RenderPasswdFile(entries []PasswdEntry) (string, error) {
	if len(entries) > 100000 {
		return "", fmt.Errorf("entries must contain at most 100000 records")
	}
	seen := make(map[string]struct{}, len(entries))
	sizeEstimate := 0
	for i, entry := range entries {
		if problems := acl.ValidateUsernameField(entry.Username, "username"); len(problems) > 0 {
			return "", fmt.Errorf("entry[%d]: %s", i, strings.Join(problems, "; "))
		}
		if _, duplicate := seen[entry.Username]; duplicate {
			return "", fmt.Errorf("entry[%d]: username duplicates another passwd entry", i)
		}
		seen[entry.Username] = struct{}{}
		if err := validatePasswdHash(entry.Hash); err != nil {
			return "", fmt.Errorf("entry[%d]: hash %w", i, err)
		}
		sizeEstimate += len(entry.Username) + len(entry.Hash) + 2
		if sizeEstimate > maxMosquittoFileBytes {
			return "", fmt.Errorf("rendered passwd file exceeds %d bytes", maxMosquittoFileBytes)
		}
	}
	if len(entries) == 0 {
		return "", nil
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
	return b.String(), nil
}

// ParsePasswdFile reads a Mosquitto password file and returns its entries in
// source order. Empty input and comments return an empty slice. A malformed
// record or duplicate username rejects the whole file so a later write cannot
// silently discard or collide with an imported entry.
func ParsePasswdFile(body string) ([]PasswdEntry, error) {
	if len(body) > maxMosquittoFileBytes {
		return nil, fmt.Errorf("passwd file exceeds %d bytes", maxMosquittoFileBytes)
	}
	if body == "" {
		return nil, nil
	}
	if strings.Count(body, "\n") > 100000 {
		return nil, fmt.Errorf("passwd file exceeds 100000 lines")
	}
	var entries []PasswdEntry
	seen := make(map[string]struct{})
	for lineNumber, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		idx := strings.IndexByte(line, ':')
		if idx <= 0 || idx == len(line)-1 {
			return nil, fmt.Errorf("line %d: expected username:hash fields", lineNumber+1)
		}
		username, hash := line[:idx], line[idx+1:]
		if problems := acl.ValidateUsernameField(username, "username"); len(problems) > 0 {
			return nil, fmt.Errorf("line %d: %s", lineNumber+1, strings.Join(problems, "; "))
		}
		if err := validatePasswdHash(hash); err != nil {
			return nil, fmt.Errorf("line %d: hash %w", lineNumber+1, err)
		}
		if _, duplicate := seen[username]; duplicate {
			return nil, fmt.Errorf("line %d: username duplicates another passwd entry", lineNumber+1)
		}
		seen[username] = struct{}{}
		entries = append(entries, PasswdEntry{Username: username, Hash: hash})
		if len(entries) > 100000 {
			return nil, fmt.Errorf("passwd file exceeds 100000 entries")
		}
	}
	return entries, nil
}

func validatePasswdHash(hash string) error {
	if !utf8.ValidString(hash) {
		return fmt.Errorf("must be valid UTF-8")
	}
	if hash == "" {
		return fmt.Errorf("is required")
	}
	if len(hash) > acl.MaxFieldBytes {
		return fmt.Errorf("must be at most %d bytes", acl.MaxFieldBytes)
	}
	for _, r := range hash {
		if r == ':' || unicode.IsControl(r) || unicode.IsSpace(r) {
			return fmt.Errorf("must not contain separators, whitespace, or control characters")
		}
	}
	return nil
}
