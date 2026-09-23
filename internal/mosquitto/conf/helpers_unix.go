package conf

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"syscall"
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

// inodeOf returns the (device, inode) tuple for path as a single uint64
// hash suitable for cycle-detection. We collapse device+ino because Go's
// map keys need a single comparable value. The exact value is opaque;
// equality across two calls for the same path is what matters.
func inodeOf(path string) (uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Non-unix syscall (e.g. Windows). Fall back to a content hash
		// of the absolute path so the cycle detector still has a
		// stable key per directory.
		sum := sha256.Sum256([]byte(path))
		return uint64(sum[0])<<56 | uint64(sum[1])<<48 | uint64(sum[2])<<40 | uint64(sum[3])<<32 |
			uint64(sum[4])<<24 | uint64(sum[5])<<16 | uint64(sum[6])<<8 | uint64(sum[7]), nil
	}
	return uint64(st.Dev)<<32 ^ uint64(st.Ino), nil
}