package util

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

// HashSecretData returns the hex-encoded SHA-256 digest of a Secret's data, computed over
// each key's raw bytes in sorted key order for a deterministic result.
func HashSecretData(data map[string][]byte) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write(data[k])
	}
	return hex.EncodeToString(h.Sum(nil))
}
