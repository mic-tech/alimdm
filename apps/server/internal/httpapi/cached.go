package httpapi

// Answering "nothing has changed" cheaply.
//
// The console polls. Several cards refresh on their own timers, and most of
// what comes back is identical to what came back a few seconds earlier: an app
// inventory of 314 packages was 71KB every five seconds, a file library of 236
// files 83KB every fifteen, for data that changes perhaps once a day.
//
// The fix is not fewer requests — polling is what keeps the console simple and
// self-healing — it is a cheaper answer to the ones that find nothing new. An
// ETag lets the browser ask "still this?" and be told "yes" in a header, with
// no body at all. Nothing in the console changes: fetch revalidates and serves
// its cached copy transparently.
//
// Cache-Control is no-cache rather than no-store, which reads backwards and is
// the point: no-store forbids keeping a copy, while no-cache keeps one and
// requires revalidation before every use. That is exactly the behaviour wanted
// — never stale, never resent unchanged.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// writeJSONCached sends v as JSON with an ETag, or 304 if the caller already
// has that exact body.
func writeJSONCached(w http.ResponseWriter, r *http.Request, v any) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		writeErr(w, http.StatusInternalServerError, "could not encode the response")
		return
	}
	sum := sha256.Sum256(buf.Bytes())
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`

	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/json")

	// A browser sends back exactly what it was given, but a proxy in between may
	// have made it a weak validator, and a client may offer several.
	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Write(buf.Bytes())
}

// matchesETag reports whether a comma-separated If-None-Match names this tag.
func matchesETag(header, etag string) bool {
	if header == "" {
		return false
	}
	for _, candidate := range splitAndTrim(header) {
		if candidate == "*" || candidate == etag {
			return true
		}
		// A weak validator is written W/"...". Weakness is irrelevant here:
		// these bodies are compared byte for byte either way.
		if len(candidate) > 2 && candidate[:2] == "W/" && candidate[2:] == etag {
			return true
		}
	}
	return false
}

func splitAndTrim(s string) []string {
	out := []string{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			part := s[start:i]
			for len(part) > 0 && (part[0] == ' ' || part[0] == '\t') {
				part = part[1:]
			}
			for len(part) > 0 && (part[len(part)-1] == ' ' || part[len(part)-1] == '\t') {
				part = part[:len(part)-1]
			}
			if part != "" {
				out = append(out, part)
			}
			start = i + 1
		}
	}
	return out
}
