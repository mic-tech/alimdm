package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"path"
)

// shortHash returns the first 12 hex chars of the SHA-256 of s.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}

// randomID returns 12 hex chars of randomness, for device ids that cannot be
// derived from anything the client reported. Never derive such an id from the
// enrolment token: it is shared across the fleet and would collide.
func randomID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not recoverable here, but an enrolment that
		// errors out is better than one that silently reuses another device's id.
		panic("randomID: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// baseName returns the final path element (safe filename).
func baseName(p string) string {
	return path.Base(p)
}
