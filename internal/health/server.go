package health

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// Server is a minimal HTTP server that exposes two endpoints:
//
//	GET /health  — 200 OK if the agent is healthy AND has scanned recently,
//	               503 otherwise
//	GET /status  — JSON-encoded Status
//
// When authToken is non-empty both endpoints require
// `Authorization: Bearer <token>`; mismatches return 401 in constant time.
type Server struct {
	addr       string
	tracker    *Tracker
	staleAfter time.Duration
	authToken  string
	srv        *http.Server
}

// NewServer constructs a health server. staleAfter is the maximum age of the
// most recent scan before /health flips to 503; pass 0 to disable the
// freshness check (e.g. for tests). A typical value is 3×ScanInterval.
//
// authToken, when non-empty, gates both endpoints behind a bearer header so
// off-loopback binds don't leak host counts to the network (OWASP A01/A05).
func NewServer(addr string, tracker *Tracker, staleAfter time.Duration, authToken string) *Server {
	s := &Server{addr: addr, tracker: tracker, staleAfter: staleAfter, authToken: authToken}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.requireAuth(s.handleHealth))
	mux.HandleFunc("GET /status", s.requireAuth(s.handleStatus))

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	return s
}

// requireAuth wraps a handler with the bearer-token check. It is a no-op when
// the server was constructed without an authToken so the loopback default
// stays curl-friendly.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authToken == "" {
			next(w, r)
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
		next(w, r)
	}
}

// Addr returns the address the server is actually listening on. Call this
// after Start() to get the resolved port when addr was ":0".
func (s *Server) Addr() string {
	return s.srv.Addr
}

// Start listens and serves in a background goroutine. It returns once the
// listener is accepting connections so callers can safely proceed.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.srv.Addr = ln.Addr().String()
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
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
