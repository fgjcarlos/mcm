package server

import (
	"net/http"
)

// withSecurityHeaders wraps next so that every response carries a baseline set
// of security response headers regardless of TLS configuration. Headers are set
// before ServeHTTP is called so that inner handlers can override them if needed,
// although in practice they should not.
//
// Headers applied:
//   - X-Content-Type-Options: nosniff
//   - X-Frame-Options: DENY
//   - Referrer-Policy: no-referrer
//   - Content-Security-Policy: default-src 'self'; script-src 'self'; frame-ancestors 'none'
//
// The CSP is safe for the embedded SPA: all assets are external same-origin
// (/assets/*.js, /assets/*.css, /favicon.svg) and there are no inline scripts or styles.
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// withCORS wraps next with a strict CORS policy. Only origins explicitly listed
// in allowedOrigins receive Access-Control-* response headers. Arbitrary origins
// are never reflected and the wildcard "*" is never used (credentials require an
// exact origin match).
//
// Behaviour:
//
//   - No Origin header → pass through unchanged (same-origin / non-browser request).
//   - Origin header present, origin NOT in list → pass through with no CORS headers;
//     the browser will block the cross-origin access.
//   - Origin header present, origin in list → set Access-Control-Allow-Origin,
//     Vary: Origin, and Access-Control-Allow-Credentials: true, then call next.
//   - CORS preflight (OPTIONS + Access-Control-Request-Method), origin in list →
//     add preflight headers and return 204 No Content WITHOUT calling next.
//   - CORS preflight, origin NOT in list → pass through with no CORS headers (204
//     from the outer handler or whatever next returns; browser blocks it).
//
// allowedOrigins is converted to a set at middleware construction time for O(1)
// per-request lookup.
func withCORS(next http.Handler, allowedOrigins []string) http.Handler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// No Origin header: not a CORS request — pass straight through.
		if origin == "" {
			next.ServeHTTP(w, r)
			return
		}

		// Vary: Origin must be set whenever an Origin header is present (allowed
		// or not) so that caches key on the origin and never serve a CORS-allowed
		// response to an origin that should be blocked.
		w.Header().Add("Vary", "Origin")

		_, isAllowed := allowed[origin]

		if !isAllowed {
			// Origin not in allowed list: pass through without CORS headers so
			// the browser enforces same-origin policy.
			next.ServeHTTP(w, r)
			return
		}

		// Origin is allowed — set the standard CORS headers for all responses.
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		// Handle preflight (OPTIONS + Access-Control-Request-Method).
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Expose X-Request-ID so browser dashboards can read the request
		// identifier set by withRequestLogging on cross-origin simple requests.
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")

		next.ServeHTTP(w, r)
	})
}
