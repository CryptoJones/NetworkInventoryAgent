package admin

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"time"
)

// middleware wraps the mux with three cross-cutting concerns:
//   - per-request access logging (one slog record per response)
//   - baseline security headers (defence-in-depth for the loopback console;
//     non-trivial once operators bind it to 0.0.0.0)
//   - CSRF protection on state-changing methods (POST/PUT/PATCH/DELETE)
//
// CSP keeps 'unsafe-inline' for styles because the templates embed a single
// <style> block; switching to a nonce is tracked separately.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "interest-cohort=()")
		h.Set("Content-Security-Policy",
			"default-src 'none'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self'; "+
				"base-uri 'none'; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'")

		if !s.checkCSRF(r) {
			http.Error(w, "csrf token mismatch", http.StatusForbidden)
			return
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(rec, r)
		slog.Info("admin request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(started).Round(time.Millisecond),
			"remote", r.RemoteAddr,
		)
	})
}

// checkCSRF enforces the token on state-changing methods. Safe methods
// (GET/HEAD/OPTIONS) are always allowed — token is propagated to forms by
// the page handlers so the user-facing flow needs no JS.
//
// The token is compared in constant time. Accepting it from either header
// or form body lets HTMX, fetch(), and plain <form> all work.
func (s *Server) checkCSRF(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	got := r.Header.Get("X-CSRF-Token")
	if got == "" {
		// Form bodies are parsed lazily; only call ParseForm when we have
		// to so handlers can still read the body themselves.
		_ = r.ParseForm()
		got = r.PostFormValue("csrf")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.csrfToken)) == 1
}

// statusRecorder lets the access-log middleware capture the response status
// that handlers chose. Defaults to 200 because http.ResponseWriter only
// records WriteHeader when an explicit status is set.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
