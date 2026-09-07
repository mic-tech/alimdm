package httpapi

// Response headers that close off whole classes of attack, set by the
// application rather than by the proxy.
//
// The bundled Caddyfile could carry these, and did carry two of them — but the
// README documents swapping in nginx, and every deployment that does gets
// whatever that operator remembered. A header the app sets is one that cannot
// be lost in a proxy migration or a certbot rewrite.

import "net/http"

// withSecurityHeaders wraps the whole router.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()

		// The console holds a session token and can act on every tablet in the
		// fleet. Nothing should ever frame it: a page that can is a page that
		// can trick an operator into clicking Unenroll.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")

		// Stops a browser second-guessing a Content-Type — an uploaded file
		// served as text should never be sniffed into script.
		h.Set("X-Content-Type-Options", "nosniff")

		// A device id or file name in a path should not travel to third-party
		// sites in a Referer.
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		// Only over TLS. Browsers ignore HSTS received over plain HTTP, but the
		// header is meaningless there and the local-LAN test setups in the docs
		// run without it, so do not claim what is not true. The proxy terminates
		// TLS, so the request reaching us is plain: X-Forwarded-Proto is how it
		// says what the client actually used.
		if r.Header.Get("X-Forwarded-Proto") == "https" || r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}
