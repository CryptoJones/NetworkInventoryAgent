// Package scanner probes a network subnet for live TCP hosts and records
// results in the inventory stores.
package scanner

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// probePorts are the TCP ports tried in order; the first successful dial
// confirms a host is live.
var probePorts = []string{"22", "80", "443", "8080"}

// Scanner probes subnets and records live hosts.
type Scanner struct {
	hosts   store.HostStore
	scans   store.ScanStore
	timeout time.Duration
}

// New creates a Scanner backed by the supplied stores.
func New(hosts store.HostStore, scans store.ScanStore, timeout time.Duration) *Scanner {
	return &Scanner{hosts: hosts, scans: scans, timeout: timeout}
}

// Scan probes every host in subnet (CIDR notation) for open TCP ports and
// records each live host in the inventory. It returns the number of live hosts
// found. The scan record in the DB is updated when the scan finishes.
func (s *Scanner) Scan(ctx context.Context, subnet string) (int, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return 0, fmt.Errorf("parse CIDR %q: %w", subnet, err)
	}

	startedAt := time.Now().UTC()
	scanID, err := s.scans.Create(ctx, &models.Scan{Subnet: subnet, StartedAt: startedAt})
	if err != nil {
		return 0, fmt.Errorf("create scan record: %w", err)
	}

	count := 0
	for ip := cloneIP(ipNet.IP); ipNet.Contains(ip); incrementIP(ip) {
		if ctx.Err() != nil {
			break
		}
		addr := ip.String()
		if s.probe(ctx, addr) {
			if _, err := s.hosts.Upsert(ctx, &models.Host{
				IPAddress: addr,
				FirstSeen: startedAt,
				LastSeen:  startedAt,
			}); err != nil {
				slog.Warn("upsert host failed", "ip", addr, "err", err)
				continue
			}
			count++
		}
	}

	finishedAt := time.Now().UTC()
	if err := s.scans.Finish(ctx, scanID, count, finishedAt); err != nil {
		slog.Warn("failed to finish scan record", "scan_id", scanID, "err", err)
	}
	return count, nil
}

// probe returns true if any of the standard probe ports are open on ip.
func (s *Scanner) probe(ctx context.Context, ip string) bool {
	d := net.Dialer{Timeout: s.timeout}
	for _, port := range probePorts {
		conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ip, port))
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
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
