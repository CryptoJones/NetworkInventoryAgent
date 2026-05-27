package admin

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// Query parameter contract for GET /api/v1/hosts:
//
//	vendor       — exact-match filter on Host.Vendor
//	device_type  — exact-match filter on Host.DeviceType
//	hostname     — case-insensitive substring match on Host.Hostname
//	subnet       — CIDR; host IP must be inside it
//	port         — integer; host must have this TCP port open (Service "open")
//	limit        — page size, default 100, capped at 1000
//	offset       — zero-based offset, default 0
//
// All filters AND together. Missing parameters mean "no filter on that
// dimension". Pagination is applied AFTER filtering so `total` always
// reflects the matching set, not the post-paginated slice.
const (
	defaultAPILimit = 100
	maxAPILimit     = 1000
)

// hostsResponse is the JSON envelope for GET /api/v1/hosts. The shape is
// intentionally stable so v1 consumers don't break — additive changes
// only (new optional fields), never renamed or removed fields.
type hostsResponse struct {
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Hosts  []*models.Host `json:"hosts"`
}

// hostDetailResponse bundles a host with its ports so a single API call
// reproduces the admin /hosts/{ip} page's data.
type hostDetailResponse struct {
	Host  *models.Host   `json:"host"`
	Ports []*models.Port `json:"ports"`
}

// apiError sets standard headers and writes a JSON error body. The status
// codes follow the standard REST conventions: 400 for bad input, 404 for
// unknown resource, 500 for backend failure.
func apiError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (s *Server) handleAPIHosts(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter, err := parseHostFilter(q)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, offset, err := parsePagination(q)
	if err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}

	all, err := s.hosts.List(r.Context())
	if err != nil {
		slog.Error("api list hosts", "err", err)
		apiError(w, http.StatusInternalServerError, "list hosts failed")
		return
	}

	// Port filter requires a join with the ports table. We snapshot the
	// host→ports relation up-front when the filter is set, then a tight
	// in-Go pass produces the final set. For unfiltered queries we skip
	// the port lookup entirely.
	var hostsWithPort map[int64]bool
	if filter.port != 0 {
		hostsWithPort = make(map[int64]bool)
		for _, h := range all {
			ports, perr := s.ports.ListByHost(r.Context(), h.ID)
			if perr != nil {
				continue
			}
			for _, p := range ports {
				if p.Number == filter.port && p.State == models.StateOpen {
					hostsWithPort[h.ID] = true
					break
				}
			}
		}
	}

	matched := make([]*models.Host, 0, len(all))
	for _, h := range all {
		if filter.matches(h, hostsWithPort) {
			matched = append(matched, h)
		}
	}

	total := len(matched)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(hostsResponse{
		Total:  total,
		Limit:  limit,
		Offset: offset,
		Hosts:  matched[start:end],
	}); err != nil {
		slog.Error("api encode hosts", "err", err)
	}
}

func (s *Server) handleAPIHostDetail(w http.ResponseWriter, r *http.Request) {
	ip := r.PathValue("ip")
	host, err := s.hosts.GetByIP(r.Context(), ip)
	if errors.Is(err, store.ErrNotFound) {
		apiError(w, http.StatusNotFound, "host not found")
		return
	}
	if err != nil {
		slog.Error("api get host", "ip", ip, "err", err)
		apiError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	ports, err := s.ports.ListByHost(r.Context(), host.ID)
	if err != nil {
		// Surface the host even when port lookup fails — partial info
		// beats a 500 when the operator just wants to confirm the host
		// exists.
		slog.Warn("api list ports", "host_id", host.ID, "err", err)
		ports = nil
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(hostDetailResponse{
		Host:  host,
		Ports: ports,
	}); err != nil {
		slog.Error("api encode host detail", "err", err)
	}
}

// hostFilter is the parsed query string. matches() applies the AND of
// every populated field.
type hostFilter struct {
	vendor     string
	deviceType string
	hostname   string     // lowercase, for case-insensitive substring
	subnet     *net.IPNet // nil = no subnet filter
	port       int        // 0 = no port filter
}

func parseHostFilter(q url.Values) (hostFilter, error) {
	f := hostFilter{
		vendor:     q.Get("vendor"),
		deviceType: q.Get("device_type"),
		hostname:   strings.ToLower(q.Get("hostname")),
	}
	if cidr := q.Get("subnet"); cidr != "" {
		_, net, err := net.ParseCIDR(cidr)
		if err != nil {
			return f, errors.New("invalid subnet CIDR: " + cidr)
		}
		f.subnet = net
	}
	if portStr := q.Get("port"); portStr != "" {
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			return f, errors.New("invalid port (must be 1..65535)")
		}
		f.port = port
	}
	return f, nil
}

func parsePagination(q url.Values) (limit, offset int, err error) {
	limit = defaultAPILimit
	if v := q.Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 {
			return 0, 0, errors.New("invalid limit (must be >= 1)")
		}
		if n > maxAPILimit {
			n = maxAPILimit
		}
		limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 0 {
			return 0, 0, errors.New("invalid offset (must be >= 0)")
		}
		offset = n
	}
	return limit, offset, nil
}

func (f hostFilter) matches(h *models.Host, hostsWithPort map[int64]bool) bool {
	if f.vendor != "" && h.Vendor != f.vendor {
		return false
	}
	if f.deviceType != "" && h.DeviceType != f.deviceType {
		return false
	}
	if f.hostname != "" && !strings.Contains(strings.ToLower(h.Hostname), f.hostname) {
		return false
	}
	if f.subnet != nil {
		ip := net.ParseIP(h.IPAddress)
		if ip == nil || !f.subnet.Contains(ip) {
			return false
		}
	}
	if f.port != 0 && !hostsWithPort[h.ID] {
		return false
	}
	return true
}
