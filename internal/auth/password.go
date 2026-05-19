package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argon2Memory      = 64 * 1024
	argon2Iterations  = 3
	argon2Parallelism = 2
	argon2SaltLength  = 16
	argon2KeyLength   = 32
)

// HashPassword hashes a password with Argon2id and returns a PHC-formatted string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argon2SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, argon2Iterations, argon2Memory, argon2Parallelism, argon2KeyLength)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		argon2Memory,
		argon2Iterations,
		argon2Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword checks whether password matches the stored Argon2id hash.
func VerifyPassword(encodedHash string, password string) (bool, error) {
	params, salt, expectedHash, err := parseHash(encodedHash)
	if err != nil {
		return false, err
	}

	actualHash := argon2.IDKey([]byte(password), salt, params.iterations, params.memory, params.parallelism, uint32(len(expectedHash)))
	return subtle.ConstantTimeCompare(actualHash, expectedHash) == 1, nil
}

type hashParams struct {
	memory      uint32
	iterations  uint32
	parallelism uint8
}

func parseHash(encodedHash string) (hashParams, []byte, []byte, error) {
	parts := strings.Split(encodedHash, "$")
	if len(parts) != 6 {
		return hashParams{}, nil, nil, fmt.Errorf("invalid password hash format")
	}
	if parts[1] != "argon2id" {
		return hashParams{}, nil, nil, fmt.Errorf("unsupported password hash algorithm %q", parts[1])
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("parse password hash version: %w", err)
	}
	if version != argon2.Version {
		return hashParams{}, nil, nil, fmt.Errorf("unsupported password hash version %d", version)
	}

	params := hashParams{}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &params.memory, &params.iterations, &params.parallelism); err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("parse password hash parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("decode password hash salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return hashParams{}, nil, nil, fmt.Errorf("decode password hash: %w", err)
	}

	return params, salt, hash, nil
}
