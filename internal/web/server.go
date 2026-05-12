// Package web provides an HTTP server that exposes the network inventory as
// both a dark-themed HTML dashboard and a JSON API.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

//go:embed templates
var templatesFS embed.FS

// Server is a minimal HTTP server for the inventory dashboard and JSON API.
type Server struct {
	addr    string
	hosts   store.HostStore
	ports   store.PortStore
	scans   store.ScanStore
	tracker *health.Tracker
	srv     *http.Server
	tmpl    *template.Template
}

// NewServer creates a web dashboard server.
func NewServer(addr string, hosts store.HostStore, ports store.PortStore, scans store.ScanStore, tracker *health.Tracker) *Server {
	s := &Server{
		addr:    addr,
		hosts:   hosts,
		ports:   ports,
		scans:   scans,
		tracker: tracker,
	}

	t, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		slog.Error("failed to parse web templates", "err", err)
	}
	s.tmpl = t

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleDashboard)
	mux.HandleFunc("GET /api/hosts", s.handleAPIHosts)
	mux.HandleFunc("GET /api/hosts/{id}", s.handleAPIHostByID)
	mux.HandleFunc("GET /api/scans", s.handleAPIScans)
	mux.HandleFunc("GET /api/status", s.handleAPIStatus)

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s
}

// Start begins listening in a background goroutine.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	s.srv.Addr = ln.Addr().String()
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			slog.Error("web server error", "err", err)
		}
	}()
	slog.Info("web dashboard started", "addr", s.srv.Addr)
	return nil
}

// Addr returns the address the server is listening on.
func (s *Server) Addr() string {
	return s.srv.Addr
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// --- HTML Dashboard ---

type dashboardData struct {
	AgentName   string
	Status      health.Status
	Hosts       []*models.Host
	RecentScans []*models.Scan
	TotalPorts  int
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	data := dashboardData{
		AgentName: s.tracker.Get().Name,
		Status:    s.tracker.Get(),
	}

	hosts, err := s.hosts.List(ctx)
	if err == nil {
		data.Hosts = hosts
		for _, h := range hosts {
			ports, pErr := s.ports.ListByHost(ctx, h.ID)
			if pErr == nil {
				data.TotalPorts += len(ports)
			}
		}
	}

	scans, err := s.scans.List(ctx)
	if err == nil {
		if len(scans) > 20 {
			scans = scans[:20]
		}
		data.RecentScans = scans
	}

	if s.tmpl == nil {
		http.Error(w, "templates not loaded", http.StatusInternalServerError)
		return
	}
	if err := s.tmpl.ExecuteTemplate(w, "dashboard.html", data); err != nil {
		slog.Error("render dashboard template", "err", err)
		http.Error(w, "template render error", http.StatusInternalServerError)
	}
}

// --- JSON API ---

func (s *Server) handleAPIHosts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hosts, err := s.hosts.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, hosts)
}

func (s *Server) handleAPIHostByID(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid host id", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	host, err := s.hosts.GetByIP(ctx, idStr)
	if err != nil {
		// Fallback: search by numeric ID
		hosts, _ := s.hosts.List(ctx)
		for _, h := range hosts {
			if h.ID == id {
				host = h
				break
			}
		}
		if host == nil {
			http.Error(w, "host not found", http.StatusNotFound)
			return
		}
	}

	ports, err := s.ports.ListByHost(ctx, host.ID)
	if err != nil {
		ports = nil
	}

	resp := map[string]any{
		"host":  host,
		"ports": ports,
	}
	writeJSON(w, resp)
}

func (s *Server) handleAPIScans(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	scans, err := s.scans.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, scans)
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.tracker.Get())
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("encode JSON response", "err", err)
	}
}
