// Package admin provides a browser-based administration console that auto-starts
// alongside the agent. It serves a dark-themed web UI with live scanner reports:
// host inventory, per-host port detail, full scan history, watchdog peer view,
// CSV/JSON export, and an on-demand scan trigger.
package admin

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
)

//go:embed templates
var templateFS embed.FS

// Trigger is the optional hook used by the POST /scan endpoint to ask the
// agent loop for an out-of-cycle cycle. Returning false means a trigger is
// already pending. Pass a nil Trigger to omit the endpoint.
type Trigger func() bool

// Server is the admin web console HTTP server.
type Server struct {
	agentName string
	hosts     store.HostStore
	ports     store.PortStore
	scans     store.ScanStore
	status    func() health.Status
	trigger   Trigger
	csrfToken string
	srv       *http.Server
	tmpl      *template.Template
}

// NewServer constructs an admin Server. It parses the embedded HTML templates
// at construction time so startup errors surface early. trigger may be nil,
// in which case the POST /scan endpoint returns 501.
func NewServer(
	addr string,
	agentName string,
	hosts store.HostStore,
	ports store.PortStore,
	scans store.ScanStore,
	status func() health.Status,
	trigger Trigger,
) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse admin templates: %w", err)
	}

	csrf, err := randomToken()
	if err != nil {
		return nil, fmt.Errorf("generate csrf token: %w", err)
	}

	s := &Server{
		agentName: agentName,
		hosts:     hosts,
		ports:     ports,
		scans:     scans,
		status:    status,
		trigger:   trigger,
		csrfToken: csrf,
		tmpl:      tmpl,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /hosts", s.handleHosts)
	mux.HandleFunc("GET /hosts/{ip}", s.handleHostDetail)
	mux.HandleFunc("GET /scans", s.handleScans)
	mux.HandleFunc("GET /watchdog", s.handleWatchdog)
	mux.HandleFunc("GET /export.json", s.handleExportJSON)
	mux.HandleFunc("GET /export.csv", s.handleExportCSV)
	mux.HandleFunc("POST /scan", s.handleScanTrigger)

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           s.middleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s, nil
}

// Start begins accepting connections in a background goroutine and returns
// once the listener is ready. Call Addr() after Start() for the resolved port.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.srv.Addr = ln.Addr().String()
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("admin server error", "err", err)
		}
	}()
	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string { return s.srv.Addr }

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// randomToken returns 32 random hex chars (128 bits of entropy). Used for
// the per-process CSRF token. Regenerated on every restart so a leaked token
// has bounded lifetime.
func randomToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
