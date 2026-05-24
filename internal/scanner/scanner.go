// Package scanner probes a network subnet for live TCP hosts and records
// results in the inventory stores.
package scanner

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// probePorts are the TCP ports tried in order; the first successful dial
// confirms a host is live.
var probePorts = []int{22, 80, 443, 8080}

// Scanner probes subnets and records live hosts.
type Scanner struct {
	hosts    store.HostStore
	ports    store.PortStore
	scans    store.ScanStore
	timeout  time.Duration
	workers  int
	maxHosts int
}

// New creates a Scanner backed by the supplied stores.
// workers controls the number of concurrent probes per subnet (default 50).
// maxHosts limits the number of usable addresses per subnet scan (default 65535).
// ports may be nil; if so, open ports discovered during liveness probing are
// not persisted (liveness-only mode).
func New(hosts store.HostStore, ports store.PortStore, scans store.ScanStore, timeout time.Duration, workers, maxHosts int) *Scanner {
	if workers <= 0 {
		workers = 50
	}
	if maxHosts <= 0 {
		maxHosts = 65535
	}
	return &Scanner{
		hosts:    hosts,
		ports:    ports,
		scans:    scans,
		timeout:  timeout,
		workers:  workers,
		maxHosts: maxHosts,
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
		sem   = make(chan struct{}, s.workers)
		wg    sync.WaitGroup
	)

	for _, addr := range ips {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(addr string) {
			defer func() { <-sem; wg.Done() }()
			openPort, ok := s.probe(ctx, addr)
			if !ok {
				return
			}
			hostID, err := s.hosts.Upsert(ctx, &models.Host{
				IPAddress: addr,
				FirstSeen: startedAt,
				LastSeen:  startedAt,
			})
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

// probe tries each of the standard probe ports in order and returns the
// first open port, or (0, false) if none answered.
func (s *Scanner) probe(ctx context.Context, ip string) (int, bool) {
	d := net.Dialer{Timeout: s.timeout}
	for _, port := range probePorts {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, strconv.Itoa(port)))
		if err == nil {
			conn.Close()
			return port, true
		}
	}
	return 0, false
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
