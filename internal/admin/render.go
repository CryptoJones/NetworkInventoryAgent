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
	Pager pager
}

type hostDetailData struct {
	pageData
	Host  *models.Host
	Ports []*models.Port
}

type scansData struct {
	pageData
	Scans []*models.Scan
	Pager pager
}

// pager carries pagination state for list templates. PagePath is the route
// the prev/next links target (e.g. "/hosts"). From/To are 1-based indices of
// the rows shown (both 0 when the page is empty).
type pager struct {
	PagePath   string
	Total      int
	Limit      int
	Offset     int
	From       int
	To         int
	HasPrev    bool
	HasNext    bool
	PrevOffset int
	NextOffset int
}

// newPager computes display + link state for a window [offset, offset+limit)
// over a list of `total` items.
func newPager(path string, total, limit, offset int) pager {
	p := pager{PagePath: path, Total: total, Limit: limit, Offset: offset}
	if offset < total {
		p.From = offset + 1
		end := offset + limit
		if end > total {
			end = total
		}
		p.To = end
	}
	if offset > 0 {
		p.HasPrev = true
		if p.PrevOffset = offset - limit; p.PrevOffset < 0 {
			p.PrevOffset = 0
		}
	}
	if offset+limit < total {
		p.HasNext = true
		p.NextOffset = offset + limit
	}
	return p
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
