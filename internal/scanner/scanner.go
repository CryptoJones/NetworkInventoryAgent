// Package scanner probes a network subnet for live TCP hosts and records
// results in the inventory stores.
package scanner

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/metrics"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// defaultProbePorts is used when Options.ProbePorts is empty.
var defaultProbePorts = []int{22, 80, 443, 8080}

// defaultDeepProbePorts is used when DeepProbe=true and DeepProbePorts is empty.
// It is a deliberately short "top services" list rather than nmap's top-1000;
// scanning a /24 with 1000 ports per live host is rarely what an operator
// wants on a 5-minute cadence.
var defaultDeepProbePorts = []int{
	21, 22, 23, 25, 53, 80, 110, 111, 135, 139, 143, 443, 445, 465, 587,
	993, 995, 1433, 1723, 2049, 3000, 3306, 3389, 5432, 5900, 6379, 8000,
	8080, 8443, 8888, 9090, 9100, 11211, 27017,
}

// Options bundles every Scanner constructor parameter. The struct form keeps
// the call sites readable as the agent grows new probe stages — passing
// twelve positional args at every binding boundary was getting unwieldy.
type Options struct {
	// Hosts and Scans must be non-nil; Ports may be nil for liveness-only mode.
	Hosts store.HostStore
	Ports store.PortStore
	Scans store.ScanStore
	// Timeout is the per-dial budget. Defaults are applied for zero values.
	Timeout time.Duration
	// Workers is the GLOBAL probe concurrency cap (default 50). Set this for
	// total parallelism, not per-subnet — an operator with 20 subnets used to
	// get 20×Workers in-flight dials, which dwarfed the documented setting.
	Workers int
	// MaxHosts is the maximum per-subnet address count (default 65535).
	MaxHosts int
	// ProbePorts is the TCP liveness port list. Empty → defaultProbePorts.
	ProbePorts []int
	// DeepProbe / DeepProbePorts: see config.ScannerConfig docs.
	DeepProbe      bool
	DeepProbePorts []int
	// UDPPorts: see config.ScannerConfig docs. Empty disables UDP probing.
	UDPPorts []int
	// EnrichARP populates MAC + Vendor from /proc/net/arp on Linux.
	EnrichARP bool
}

// Scanner probes subnets and records live hosts.
type Scanner struct {
	hosts          store.HostStore
	ports          store.PortStore
	scans          store.ScanStore
	timeout        time.Duration
	maxHosts       int
	probePorts     []int
	deepProbe      bool
	deepProbePorts []int
	udpPorts       []int
	enrichARP      bool

	// sem caps total concurrent probes across all subnets in a cycle. It is
	// allocated once at construction so an operator with 20 subnets is not
	// silently fanned out to 20×workers in-flight dials.
	sem chan struct{}
}

// New creates a Scanner from the supplied Options. Zero/nil fields fall back
// to sensible defaults.
func New(opts Options) *Scanner {
	if opts.Workers <= 0 {
		opts.Workers = 50
	}
	if opts.MaxHosts <= 0 {
		opts.MaxHosts = 65535
	}
	if len(opts.ProbePorts) == 0 {
		opts.ProbePorts = defaultProbePorts
	}
	deepPorts := opts.DeepProbePorts
	if opts.DeepProbe && len(deepPorts) == 0 {
		deepPorts = defaultDeepProbePorts
	}
	return &Scanner{
		hosts:          opts.Hosts,
		ports:          opts.Ports,
		scans:          opts.Scans,
		timeout:        opts.Timeout,
		maxHosts:       opts.MaxHosts,
		probePorts:     opts.ProbePorts,
		deepProbe:      opts.DeepProbe,
		deepProbePorts: deepPorts,
		udpPorts:       opts.UDPPorts,
		enrichARP:      opts.EnrichARP,
		sem:            make(chan struct{}, opts.Workers),
	}
}

// Scan probes every host in subnet (CIDR notation) for open TCP ports and
// records each live host in the inventory. It returns the number of live hosts
// found. The scan record in the DB is updated when the scan finishes.
func (s *Scanner) Scan(ctx context.Context, subnet string) (int, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return 0, fmt.Errorf("parse CIDR %q: %w", subnet, err)
	}

	// Refuse oversized subnets BEFORE allocating the address slice. For
	// IPv6 a /64 holds 2^64 addresses, so even the `len(ips) > maxHosts`
	// check downstream is too late — the slice grows linearly inside
	// usableIPs and exhausts memory long before the check trips.
	ones, bits := ipNet.Mask.Size()
	hostBits := uint(bits - ones)
	if hostBits >= 31 {
		return 0, fmt.Errorf("subnet %q has 2^%d addresses, exceeds limit of %d", subnet, hostBits, s.maxHosts)
	}
	if expected := 1 << hostBits; expected > s.maxHosts {
		return 0, fmt.Errorf("subnet %q has %d addresses, exceeds limit of %d", subnet, expected, s.maxHosts)
	}

	ips := usableIPs(ipNet)
	if len(ips) > s.maxHosts {
		return 0, fmt.Errorf("subnet %q has %d usable addresses, exceeds limit of %d", subnet, len(ips), s.maxHosts)
	}

	startedAt := time.Now().UTC()
	scanID, err := s.scans.Create(ctx, &models.Scan{Subnet: subnet, StartedAt: startedAt})
	if err != nil {
		return 0, fmt.Errorf("create scan record: %w", err)
	}

	var (
		mu    sync.Mutex
		count int
		wg    sync.WaitGroup
	)

	for _, addr := range ips {
		if ctx.Err() != nil {
			break
		}
		s.sem <- struct{}{}
		wg.Add(1)
		go func(addr string) {
			defer func() { <-s.sem; wg.Done() }()
			openPort, ok := s.probe(ctx, addr)
			if !ok {
				metrics.ProbeFailureTotal.Inc()
				return
			}
			metrics.ProbeSuccessTotal.Inc()
			host := &models.Host{
				IPAddress: addr,
				Hostname:  reverseDNS(ctx, addr),
				FirstSeen: startedAt,
				LastSeen:  startedAt,
			}
			if fp := fingerprint(ctx, addr, openPort, s.timeout); fp != "" {
				host.OSFingerprint = fp
			}
			if s.enrichARP {
				if mac, vendor := lookupARP(addr); mac != "" {
					host.MACAddress = mac
					host.Vendor = vendor
				}
			}
			hostID, err := s.hosts.Upsert(ctx, host)
			if err != nil {
				metrics.DBErrorsTotal.Inc()
				slog.Warn("upsert host failed", "ip", addr, "err", err)
				return
			}
			metrics.HostsUpsertedTotal.Inc()
			if s.ports != nil {
				s.upsertPort(ctx, hostID, addr, openPort, models.TCP, models.StateOpen, startedAt)
			}
			if s.deepProbe && s.ports != nil {
				s.deepScan(ctx, hostID, addr, openPort, startedAt)
			}
			if len(s.udpPorts) > 0 && s.ports != nil {
				s.udpScan(ctx, hostID, addr, startedAt)
			}
			mu.Lock()
			count++
			mu.Unlock()
		}(addr)
	}
	wg.Wait()

	finishedAt := time.Now().UTC()
	if err := s.scans.Finish(ctx, scanID, count, finishedAt); err != nil {
		slog.Warn("failed to finish scan record", "scan_id", scanID, "err", err)
	}
	return count, nil
}

// probe dials every configured probe port concurrently and returns the first
// port that answers, or (0, false) if all dials fail within s.timeout.
// Concurrent fan-out collapses worst-case latency from len(probePorts)*timeout
// to ~1*timeout for dead hosts — the original sequential probe could keep a
// /24 sweep running longer than the scan interval.
func (s *Scanner) probe(ctx context.Context, ip string) (int, bool) {
	dialCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	type result struct {
		port int
		ok   bool
	}
	results := make(chan result, len(s.probePorts))
	d := net.Dialer{Timeout: s.timeout}

	for _, port := range s.probePorts {
		go func(port int) {
			conn, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
			if err != nil {
				results <- result{}
				return
			}
			_ = conn.Close()
			results <- result{port: port, ok: true}
		}(port)
	}

	var firstOpen int
	for range s.probePorts {
		r := <-results
		if r.ok && firstOpen == 0 {
			firstOpen = r.port
			cancel() // short-circuit the remaining dials
		}
	}
	if firstOpen == 0 {
		return 0, false
	}
	return firstOpen, true
}

// upsertPort writes one row to the ports store and increments the metrics.
// Pulled out of the hot loop to keep the per-host goroutine readable now
// that we have three port-producing stages (liveness, deep TCP, UDP).
func (s *Scanner) upsertPort(ctx context.Context, hostID int64, ip string, port int, proto models.Protocol, state models.PortState, ts time.Time) {
	if err := s.ports.Upsert(ctx, &models.Port{
		HostID:    hostID,
		Number:    port,
		Protocol:  proto,
		State:     state,
		FirstSeen: ts,
		LastSeen:  ts,
	}); err != nil {
		metrics.DBErrorsTotal.Inc()
		slog.Warn("upsert port failed", "ip", ip, "port", port, "proto", proto, "err", err)
		return
	}
	metrics.PortsUpsertedTotal.Inc()
}

// deepScan dials each port in s.deepProbePorts (skipping the one already
// confirmed by the liveness probe) and persists every successful dial. The
// fan-out shares the global sem so deep probing does not blow past the
// configured Workers budget. Closed/filtered ports are intentionally NOT
// recorded — the ports table is a positive log of what's open, not an
// inverse-index of what isn't.
func (s *Scanner) deepScan(ctx context.Context, hostID int64, ip string, knownOpen int, ts time.Time) {
	d := net.Dialer{Timeout: s.timeout}
	var wg sync.WaitGroup
	for _, port := range s.deepProbePorts {
		if port == knownOpen {
			continue
		}
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(port int) {
			defer func() { <-s.sem; wg.Done() }()
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
			if err != nil {
				return
			}
			_ = conn.Close()
			s.upsertPort(ctx, hostID, ip, port, models.TCP, models.StateOpen, ts)
		}(port)
	}
	wg.Wait()
}

// udpScan tries each UDP port in s.udpPorts. Best-effort semantics:
//   - any bytes read back → Open
//   - connection-refused (Linux surfaces ICMP port-unreachable this way) → Closed
//   - anything else (no reply within timeout) → not recorded, since the
//     ambiguous case would otherwise dominate the ports table.
func (s *Scanner) udpScan(ctx context.Context, hostID int64, ip string, ts time.Time) {
	var wg sync.WaitGroup
	for _, port := range s.udpPorts {
		select {
		case s.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		wg.Add(1)
		go func(port int) {
			defer func() { <-s.sem; wg.Done() }()
			state, ok := probeUDP(ctx, ip, port, s.timeout)
			if !ok {
				return
			}
			if state == models.StateOpen {
				metrics.UDPProbeSuccessTotal.Inc()
			}
			s.upsertPort(ctx, hostID, ip, port, models.UDP, state, ts)
		}(port)
	}
	wg.Wait()
}

// probeUDP returns (state, true) when the port's state is determinable, and
// (_, false) when the probe is ambiguous (no reply, no ICMP).
func probeUDP(ctx context.Context, ip string, port int, timeout time.Duration) (models.PortState, bool) {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "udp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		// A connect-time error on UDP is unusual but treat it as filtered.
		return "", false
	}
	defer func() { _ = conn.Close() }()
	// Send a single zero byte. Many services respond to anything (DNS
	// returns FORMERR, NTP rejects); for the rest we still learn the
	// closed-vs-filtered distinction from the read error.
	_ = conn.SetWriteDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte{0}); err != nil {
		return "", false
	}
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 32)
	n, err := conn.Read(buf)
	if err == nil && n > 0 {
		return models.StateOpen, true
	}
	// ECONNREFUSED → kernel saw an ICMP port-unreachable, port is closed.
	if err != nil && strings.Contains(err.Error(), "refused") {
		return models.StateClosed, true
	}
	return "", false
}

// reverseDNS does a best-effort PTR lookup with a tight timeout. Returns an
// empty string if anything fails — Hostname stays absent in the inventory
// rather than being populated with a misleading value.
func reverseDNS(ctx context.Context, ip string) string {
	rctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(rctx, ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

// fingerprint is a best-effort banner grab for the port that answered the
// liveness probe. For SSH (22) we read the first line of the protocol
// greeting; for HTTP (80, 8080) we send a minimal HEAD request and capture
// the Server header. Anything else, or any error, returns "" so the field
// stays absent rather than misleadingly populated.
func fingerprint(ctx context.Context, ip string, port int, timeout time.Duration) string {
	switch port {
	case 22:
		return sshBanner(ctx, ip, port, timeout)
	case 80, 8080:
		return httpServerHeader(ctx, ip, port, timeout, "http")
	case 443:
		// TLS handshake would be needed for 443; skip rather than dial
		// twice. A future deep-probe pass can do this properly.
		return ""
	default:
		return ""
	}
}

func sshBanner(ctx context.Context, ip string, port int, timeout time.Duration) string {
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return ""
	}
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "SSH-") {
		return ""
	}
	return line
}

func httpServerHeader(ctx context.Context, ip string, port int, timeout time.Duration, scheme string) string {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodHead,
		fmt.Sprintf("%s://%s/", scheme, net.JoinHostPort(ip, strconv.Itoa(port))), nil)
	if err != nil {
		return ""
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if s := resp.Header.Get("Server"); s != "" {
		return "HTTP: " + s
	}
	return ""
}

// usableIPs returns the set of host addresses in ipNet, skipping the network
// and broadcast addresses for IPv4 subnets with a prefix length of /30 or shorter.
// /31 (RFC 3021, point-to-point) and /32 (host route) addresses are returned as-is.
func usableIPs(ipNet *net.IPNet) []string {
	ones, bits := ipNet.Mask.Size()
	skipEnds := bits == 32 && ones <= 30

	var ips []string
	for ip := cloneIP(ipNet.IP); ipNet.Contains(ip); incrementIP(ip) {
		ips = append(ips, ip.String())
	}
	if skipEnds && len(ips) >= 2 {
		ips = ips[1 : len(ips)-1]
	}
	return ips
}

func cloneIP(ip net.IP) net.IP {
	clone := make(net.IP, len(ip))
	copy(clone, ip)
	return clone
}

// incrementIP advances ip by one, in-place.
func incrementIP(ip net.IP) {
	if len(ip) == 4 {
		v := binary.BigEndian.Uint32(ip)
		binary.BigEndian.PutUint32(ip, v+1)
		return
	}
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
