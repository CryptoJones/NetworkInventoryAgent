package admin

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

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
	limit, offset, err := parsePagination(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.render(w, "hosts", hostsData{
		pageData: s.basePage("Hosts"),
		Hosts:    pageSlice(hosts, offset, limit),
		Pager:    newPager("/hosts", len(hosts), limit, offset),
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
	limit, offset, err := parsePagination(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.render(w, "scans", scansData{
		pageData: s.basePage("Scans"),
		Scans:    pageSlice(scans, offset, limit),
		Pager:    newPager("/scans", len(scans), limit, offset),
	})
}

func (s *Server) handleWatchdog(w http.ResponseWriter, _ *http.Request) {
	st := s.status()
	s.render(w, "watchdog", watchdogData{
		pageData: s.basePage("Watchdog"),
		Peer:     st.Peer,
	})
}

// handleScanTrigger fires the agent's out-of-cycle scan. Returns 204 on
// success, 503 when a trigger is already pending, 501 when the agent was
// constructed without a trigger hook.
func (s *Server) handleScanTrigger(w http.ResponseWriter, _ *http.Request) {
	if s.trigger == nil {
		http.Error(w, "scan trigger not wired", http.StatusNotImplemented)
		return
	}
	if !s.trigger() {
		http.Error(w, "scan already pending", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleExportJSON returns the full host + ports inventory as JSON. Useful
// for batch ingestion and offline analysis without taking the agent down to
// copy the SQLite file.
func (s *Server) handleExportJSON(w http.ResponseWriter, r *http.Request) {
	type portOut struct {
		Number   int    `json:"number"`
		Protocol string `json:"protocol"`
		State    string `json:"state"`
		Service  string `json:"service,omitempty"`
	}
	type hostOut struct {
		IPAddress     string    `json:"ip_address"`
		Hostname      string    `json:"hostname,omitempty"`
		MACAddress    string    `json:"mac_address,omitempty"`
		Vendor        string    `json:"vendor,omitempty"`
		OSFingerprint string    `json:"os_fingerprint,omitempty"`
		Ports         []portOut `json:"ports,omitempty"`
	}

	hosts, err := s.hosts.List(r.Context())
	if err != nil {
		slog.Error("export: list hosts", "err", err)
		http.Error(w, "list hosts failed", http.StatusInternalServerError)
		return
	}

	out := make([]hostOut, 0, len(hosts))
	for _, h := range hosts {
		hostRecord := hostOut{
			IPAddress:     h.IPAddress,
			Hostname:      h.Hostname,
			MACAddress:    h.MACAddress,
			Vendor:        h.Vendor,
			OSFingerprint: h.OSFingerprint,
		}
		ports, err := s.ports.ListByHost(r.Context(), h.ID)
		if err != nil {
			slog.Warn("export: list ports", "host_id", h.ID, "err", err)
		}
		for _, p := range ports {
			hostRecord.Ports = append(hostRecord.Ports, portOut{
				Number:   p.Number,
				Protocol: string(p.Protocol),
				State:    string(p.State),
				Service:  p.Service,
			})
		}
		out = append(out, hostRecord)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="inventory.json"`)
	if err := json.NewEncoder(w).Encode(out); err != nil {
		slog.Error("export: encode json", "err", err)
	}
}

// handleExportCSV returns one row per host in a flat shape suitable for a
// spreadsheet. Multi-port hosts get a comma-joined Ports column rather than
// duplicated rows so the file stays one-row-per-asset.
func (s *Server) handleExportCSV(w http.ResponseWriter, r *http.Request) {
	hosts, err := s.hosts.List(r.Context())
	if err != nil {
		slog.Error("export: list hosts", "err", err)
		http.Error(w, "list hosts failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="inventory.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{"ip_address", "hostname", "mac_address", "vendor", "os_fingerprint", "first_seen", "last_seen", "ports"})

	for _, h := range hosts {
		ports, err := s.ports.ListByHost(r.Context(), h.ID)
		if err != nil {
			slog.Warn("export csv: list ports", "host_id", h.ID, "err", err)
		}
		_ = cw.Write([]string{
			h.IPAddress,
			h.Hostname,
			h.MACAddress,
			h.Vendor,
			h.OSFingerprint,
			h.FirstSeen.UTC().Format("2006-01-02T15:04:05Z"),
			h.LastSeen.UTC().Format("2006-01-02T15:04:05Z"),
			joinPorts(ports),
		})
	}
}

func joinPorts(ports []*models.Port) string {
	if len(ports) == 0 {
		return ""
	}
	out := ""
	for i, p := range ports {
		if i > 0 {
			out += ","
		}
		out += strconv.Itoa(p.Number) + "/" + string(p.Protocol)
	}
	return out
}
