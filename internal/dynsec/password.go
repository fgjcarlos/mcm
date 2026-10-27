package dynsec

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
)

// PasswordIterations is the number of iterations the plugin uses when it
// hashes a password.
const PasswordIterations = 101

// SaltLength is the length of the salt the plugin uses.
//
// The salt of the plugin is 12 bytes long, the same length the broker
// uses for the password file. A database that carries another length is
// refused by the plugin.
const SaltLength = 12

// HashPassword hashes a password the way the plugin does.
//
// The plugin derives the key with PBKDF2 over HMAC-SHA512, using a random
// salt and 101 iterations. Both the salt and the key are base64 encoded
// in the database of the plugin, which never holds a cleartext password.
//
// The pair is the one the plugin itself writes, so a database MCM writes
// is read by the plugin without a conversion.
func HashPassword(password string) (hash, salt string, err error) {
	raw := make([]byte, SaltLength)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate salt: %w", err)
	}
	key, err := pbkdf2.Key(sha512.New, password, raw, PasswordIterations, 64)
	if err != nil {
		return "", "", fmt.Errorf("hash password: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), base64.StdEncoding.EncodeToString(raw), nil
}
