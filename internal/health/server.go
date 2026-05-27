package health

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/metrics"
	"github.com/Ronin48/NetworkInventoryAgent/internal/tracing"
)

// Server is a minimal HTTP server that exposes three endpoints:
//
//	GET /health   — 200 OK if the agent is healthy AND has scanned recently,
//	                503 otherwise
//	GET /status   — JSON-encoded Status
//	GET /metrics  — Prometheus text exposition format (also gated by authToken)
//
// When authToken is non-empty all endpoints require
// `Authorization: Bearer <token>`; mismatches return 401 in constant time.
// When TLSConfig is non-nil the listener accepts only HTTPS; mTLS is enabled
// if TLSConfig.ClientCAs is set.
type Server struct {
	addr       string
	tracker    *Tracker
	staleAfter time.Duration
	authToken  string
	tlsConfig  *tls.Config
	srv        *http.Server
}

// ServerOptions configures NewServer. All fields are optional; the zero
// value produces a loopback-friendly plain-HTTP server with no auth and no
// freshness check (matching test defaults).
type ServerOptions struct {
	// StaleAfter is the maximum age of the most recent scan before /health
	// flips to 503. Zero disables the freshness check.
	StaleAfter time.Duration
	// AuthToken gates every endpoint behind `Authorization: Bearer <token>`.
	AuthToken string
	// TLSConfig switches the listener to HTTPS. mTLS is enabled if
	// TLSConfig.ClientCAs is non-nil (the listener requires a client
	// certificate signed by one of those CAs).
	TLSConfig *tls.Config
}

// NewServer constructs a health server with the supplied options.
// The legacy positional signature (addr, tracker, staleAfter, authToken)
// is preserved as NewServerLegacy for tests and external callers; new code
// should use NewServer.
func NewServer(addr string, tracker *Tracker, opts ServerOptions) *Server {
	s := &Server{
		addr:       addr,
		tracker:    tracker,
		staleAfter: opts.StaleAfter,
		authToken:  opts.AuthToken,
		tlsConfig:  opts.TLSConfig,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.requireAuth(s.handleHealth))
	mux.HandleFunc("GET /status", s.requireAuth(s.handleStatus))
	mux.Handle("GET /metrics", s.requireAuthHandler(metrics.Default.Handler()))

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           tracing.HTTPMiddleware("health", mux),
		TLSConfig:         opts.TLSConfig,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	return s
}

// NewServerLegacy preserves the pre-26.08 positional signature so existing
// callers (tests, third-party) compile unchanged. New code should call
// NewServer with a ServerOptions struct.
func NewServerLegacy(addr string, tracker *Tracker, staleAfter time.Duration, authToken string) *Server {
	return NewServer(addr, tracker, ServerOptions{
		StaleAfter: staleAfter,
		AuthToken:  authToken,
	})
}

// requireAuth wraps a handler with the bearer-token check. It is a no-op when
// the server was constructed without an authToken so the loopback default
// stays curl-friendly.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuthHandler(next).ServeHTTP
}

// requireAuthHandler is the http.Handler-typed variant so we can wrap the
// metrics handler (which is constructed as a Handler, not a HandlerFunc).
func (s *Server) requireAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		got, ok := strings.CutPrefix(auth, "Bearer ")
		// Constant-time compare so partial-match timing can't be used to
		// bisect the token.
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(s.authToken)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="inventory-agent"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Addr returns the address the server is actually listening on. Call this
// after Start() to get the resolved port when addr was ":0".
func (s *Server) Addr() string {
	return s.srv.Addr
}

// Start listens and serves in a background goroutine. It returns once the
// listener is accepting connections so callers can safely proceed. When the
// server was constructed with a TLSConfig the listener serves HTTPS via
// ServeTLS; the certificates are taken from TLSConfig.Certificates so no
// file paths are required at Start time.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.srv.Addr = ln.Addr().String()
	go func() {
		var err error
		if s.tlsConfig != nil {
			err = s.srv.ServeTLS(ln, "", "")
		} else {
			err = s.srv.Serve(ln)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health server error", "err", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	st := s.tracker.Get()
	if !st.Healthy {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// Stale-scan freshness check: a wedged scan loop will keep Healthy=true
	// because nothing flips it, but LastScanAt will fall behind. Refuse 200
	// once the gap exceeds staleAfter so orchestrators can restart us.
	if s.staleAfter > 0 && st.LastScanAt != nil &&
		time.Since(*st.LastScanAt) > s.staleAfter {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.tracker.Get()); err != nil {
		slog.Error("encode status response", "err", err)
	}
}
