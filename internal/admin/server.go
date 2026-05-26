// Package admin provides a browser-based administration console that auto-starts
// alongside the agent. It serves a dark-themed web UI with live scanner reports:
// host inventory, per-host port detail, and full scan history.
package admin

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

//go:embed templates
var templateFS embed.FS

var funcMap = template.FuncMap{
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04:05")
	},
	"formatTimePtr": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return t.UTC().Format("2006-01-02 15:04:05")
	},
	"formatDuration": func(start time.Time, end *time.Time) string {
		if end == nil {
			return "in progress"
		}
		return end.Sub(start).Round(time.Second).String()
	},
	"string": func(v interface{}) string {
		return fmt.Sprintf("%s", v)
	},
}

// Server is the admin web console HTTP server.
type Server struct {
	agentName string
	hosts     store.HostStore
	ports     store.PortStore
	scans     store.ScanStore
	status    func() health.Status
	srv       *http.Server
	tmpl      *template.Template
}

// NewServer constructs an admin Server. It parses the embedded HTML templates
// at construction time so startup errors surface early.
func NewServer(
	addr string,
	agentName string,
	hosts store.HostStore,
	ports store.PortStore,
	scans store.ScanStore,
	status func() health.Status,
) (*Server, error) {
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse admin templates: %w", err)
	}

	s := &Server{
		agentName: agentName,
		hosts:     hosts,
		ports:     ports,
		scans:     scans,
		status:    status,
		tmpl:      tmpl,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /hosts", s.handleHosts)
	mux.HandleFunc("GET /hosts/{ip}", s.handleHostDetail)
	mux.HandleFunc("GET /scans", s.handleScans)

	s.srv = &http.Server{
		Addr:              addr,
		Handler:           withMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s, nil
}

// withMiddleware wraps the mux with two cross-cutting concerns:
//   - per-request access logging (one slog record per response)
//   - baseline security headers (defence-in-depth for the unauthenticated
//     loopback console; non-trivial once operators bind it to 0.0.0.0)
//
// CSP keeps 'unsafe-inline' for styles because the templates embed a single
// <style> block; switching to a nonce is tracked separately.
func withMiddleware(next http.Handler) http.Handler {
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

// --- page data types ---

type pageData struct {
	Title     string
	AgentName string
	Healthy   bool
}

type dashboardData struct {
	pageData
	Status      health.Status
	HostCount   int
	RecentScans []*models.Scan
	RecentHosts []*models.Host
	// LoadErrors is the list of card/section names whose backing query
	// failed during this render. The template uses it to surface a banner
	// instead of showing stale zeros as if everything were fine.
	LoadErrors []string
}

type hostsData struct {
	pageData
	Hosts []*models.Host
}

type hostDetailData struct {
	pageData
	Host  *models.Host
	Ports []*models.Port
}

type scansData struct {
	pageData
	Scans []*models.Scan
}

func (s *Server) basePage(title string) pageData {
	st := s.status()
	return pageData{
		Title:     title,
		AgentName: s.agentName,
		Healthy:   st.Healthy,
	}
}

// --- handlers ---

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	st := s.status()
	data := dashboardData{
		pageData: s.basePage("Dashboard"),
		Status:   st,
	}
	if n, err := s.hosts.Count(r.Context()); err == nil {
		data.HostCount = n
	} else {
		slog.Error("dashboard: count hosts", "err", err)
		data.LoadErrors = append(data.LoadErrors, "host count")
	}
	if scans, err := s.scans.List(r.Context()); err == nil {
		if len(scans) > 10 {
			scans = scans[:10]
		}
		data.RecentScans = scans
	} else {
		slog.Error("dashboard: list scans", "err", err)
		data.LoadErrors = append(data.LoadErrors, "recent scans")
	}
	if hosts, err := s.hosts.List(r.Context()); err == nil {
		if len(hosts) > 10 {
			hosts = hosts[:10]
		}
		data.RecentHosts = hosts
	} else {
		slog.Error("dashboard: list hosts", "err", err)
		data.LoadErrors = append(data.LoadErrors, "host inventory")
	}
	s.render(w, "dashboard", data)
}

func (s *Server) handleHosts(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.hosts.List(r.Context())
	if err != nil {
		slog.Error("admin list hosts", "err", err)
		http.Error(w, "failed to load hosts", http.StatusInternalServerError)
		return
	}
	s.render(w, "hosts", hostsData{
		pageData: s.basePage("Hosts"),
		Hosts:    hosts,
	})
}

func (s *Server) handleHostDetail(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	host, err := s.hosts.GetByIP(r.Context(), ip)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		slog.Error("admin get host", "ip", ip, "err", err)
		http.Error(w, "failed to load host", http.StatusInternalServerError)
		return
	}
	ports, err := s.ports.ListByHost(r.Context(), host.ID)
	if err != nil {
		slog.Error("admin list ports", "host_id", host.ID, "err", err)
	}
	s.render(w, "host", hostDetailData{
		pageData: s.basePage(ip),
		Host:     host,
		Ports:    ports,
	})
}

func (s *Server) handleScans(w http.ResponseWriter, r *http.Request) {
	scans, err := s.scans.List(r.Context())
	if err != nil {
		slog.Error("admin list scans", "err", err)
		http.Error(w, "failed to load scans", http.StatusInternalServerError)
		return
	}
	s.render(w, "scans", scansData{
		pageData: s.basePage("Scans"),
		Scans:    scans,
	})
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("admin render template", "name", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
