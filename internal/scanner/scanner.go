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

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// defaultProbePorts is used when ScannerConfig.ProbePorts is empty.
var defaultProbePorts = []int{22, 80, 443, 8080}

// Scanner probes subnets and records live hosts.
type Scanner struct {
	hosts      store.HostStore
	ports      store.PortStore
	scans      store.ScanStore
	timeout    time.Duration
	maxHosts   int
	probePorts []int

	// sem caps total concurrent probes across all subnets in a cycle. It is
	// allocated once at construction so an operator with 20 subnets is not
	// silently fanned out to 20×workers in-flight dials.
	sem chan struct{}
}

// New creates a Scanner backed by the supplied stores.
// workers controls the GLOBAL number of concurrent probes across all subnets
// (default 50). maxHosts limits the number of usable addresses per subnet
// scan (default 65535). probePorts may be nil; if so, the default port list
// is used. ports may be nil; if so, open ports discovered during liveness
// probing are not persisted (liveness-only mode).
func New(
	hosts store.HostStore,
	ports store.PortStore,
	scans store.ScanStore,
	timeout time.Duration,
	workers, maxHosts int,
	probePorts []int,
) *Scanner {
	if workers <= 0 {
		workers = 50
	}
	if maxHosts <= 0 {
		maxHosts = 65535
	}
	if len(probePorts) == 0 {
		probePorts = defaultProbePorts
	}
	return &Scanner{
		hosts:      hosts,
		ports:      ports,
		scans:      scans,
		timeout:    timeout,
		maxHosts:   maxHosts,
		probePorts: probePorts,
		sem:        make(chan struct{}, workers),
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
				return
			}
			host := &models.Host{
				IPAddress: addr,
				Hostname:  reverseDNS(ctx, addr),
				FirstSeen: startedAt,
				LastSeen:  startedAt,
			}
			if fp := fingerprint(ctx, addr, openPort, s.timeout); fp != "" {
				host.OSFingerprint = fp
			}
			hostID, err := s.hosts.Upsert(ctx, host)
			if err != nil {
				slog.Warn("upsert host failed", "ip", addr, "err", err)
				return
			}
			if s.ports != nil {
				if err := s.ports.Upsert(ctx, &models.Port{
					HostID:    hostID,
					Number:    openPort,
					Protocol:  models.TCP,
					State:     models.StateOpen,
					FirstSeen: startedAt,
					LastSeen:  startedAt,
				}); err != nil {
					slog.Warn("upsert port failed", "ip", addr, "port", openPort, "err", err)
				}
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
			conn.Close()
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
	defer conn.Close()
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
	defer resp.Body.Close()
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
