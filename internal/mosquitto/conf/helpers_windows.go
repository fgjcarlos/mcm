//go:build !unix

package conf

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
)

// inodeOf returns a stable per-path key for the include cycle detector on
// non-unix platforms. We hash the absolute path so two calls for the
// same directory yield the same key without depending on syscall.Stat_t.
func inodeOf(path string) (uint64, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	sum := sha256.Sum256([]byte(path))
	return binary.LittleEndian.Uint64(sum[:8]), nil
}