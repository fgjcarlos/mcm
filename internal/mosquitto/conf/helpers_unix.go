package conf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
)

// bytesReader returns an io.Reader over b. We keep a thin local alias
// instead of importing bytes.NewReader at every callsite; the include
// resolver only needs the read end.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// hashSHA256 returns the hex-encoded SHA-256 of b. Used by the include
// walker so the snapshot carries a stable fingerprint for every file
// pulled in from the include tree.
func hashSHA256(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
