package admin

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

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
	"formatPct": func(f float64) string {
		return fmt.Sprintf("%.1f%%", f)
	},
}

// pageData is embedded in every page-specific struct so templates can share
// the nav/header bits without each handler duplicating the wiring.
type pageData struct {
	Title     string
	AgentName string
	Healthy   bool
	CSRFToken string
}

type dashboardData struct {
	pageData
	Status      health.Status
	HostCount   int
	RecentScans []*models.Scan
	RecentHosts []*models.Host
	// LoadErrors lists card/section names whose backing query failed during
	// this render. The template surfaces a banner instead of showing stale
	// zeros as if everything were fine.
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

type watchdogData struct {
	pageData
	Peer *health.PeerStatus
}

func (s *Server) basePage(title string) pageData {
	st := s.status()
	return pageData{
		Title:     title,
		AgentName: s.agentName,
		Healthy:   st.Healthy,
		CSRFToken: s.csrfToken,
	}
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("admin render template", "name", name, "err", err)
		http.Error(w, "render error", http.StatusInternalServerError)
	}
}
