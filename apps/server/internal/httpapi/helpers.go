package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
)

// shortHash returns the first 12 hex chars of the SHA-256 of s.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// baseName returns the final path element (safe filename).
func baseName(p string) string {
	return path.Base(p)
}
