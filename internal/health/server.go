package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server is a minimal HTTP server that exposes two endpoints:
//
//	GET /health  — 200 OK if the agent is healthy, 503 otherwise
//	GET /status  — JSON-encoded Status
type Server struct {
	addr    string
	tracker *Tracker
	srv     *http.Server
}

func NewServer(addr string, tracker *Tracker) *Server {
	s := &Server{addr: addr, tracker: tracker}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	return s
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
	if s.tracker.Get().Healthy {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusServiceUnavailable)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.tracker.Get()) //nolint:errcheck
}
