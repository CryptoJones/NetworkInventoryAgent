// Package ping provides an ICMP echo-sweep for a subnet. It returns the set
// of IPs that respond to ping, letting downstream TCP probes skip silent hosts.
package ping

import (
	"context"
	"net"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// Sweep pings every IP in the given subnet (CIDR notation) concurrently.
// It returns the list of IPs that responded to an ICMP echo request. If ICMP
// is unavailable (no admin rights), it returns nil, nil and the caller should
// skip ping filtering. timeout controls per-host wait; workers sets concurrency.
func Sweep(ctx context.Context, subnet string, timeout time.Duration, workers int) ([]string, error) {
	conn, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		return nil, nil
	}
	defer conn.Close()

	_, ipNet, parseErr := net.ParseCIDR(subnet)
	if parseErr != nil {
		return nil, parseErr
	}

	ips := usableIPs(ipNet)
	if len(ips) == 0 {
		return nil, nil
	}

	if workers <= 0 {
		workers = 128
	}

	return probe(ctx, conn, ips, timeout, workers)
}

func probe(ctx context.Context, conn *icmp.PacketConn, ips []string, timeout time.Duration, workers int) ([]string, error) {
	var (
		mu   sync.Mutex
		live []string
		sem  = make(chan struct{}, workers)
		wg   sync.WaitGroup
	)

	seq := 0
	for _, dst := range ips {
		if ctx.Err() != nil {
			break
		}
		seq++
		msg := icmp.Message{
			Type: ipv4.ICMPTypeEcho,
			Code: 0,
			Body: &icmp.Echo{ID: int(time.Now().Unix()) & 0xFFFF, Seq: seq},
		}
		wb, err := msg.Marshal(nil)
		if err != nil {
			continue
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(dst string, seq int, wb []byte) {
			defer func() { <-sem; wg.Done() }()
			if doPing(ctx, conn, dst, wb, timeout, seq) {
				mu.Lock()
				live = append(live, dst)
				mu.Unlock()
			}
		}(dst, seq, wb)
	}
	wg.Wait()
	return live, nil
}

func doPing(ctx context.Context, conn *icmp.PacketConn, dst string, wb []byte, timeout time.Duration, wantSeq int) bool {
	deadline := time.Now().Add(timeout)
	_ = conn.SetDeadline(deadline)

	_, err := conn.WriteTo(wb, &net.IPAddr{IP: net.ParseIP(dst)})
	if err != nil {
		return false
	}

	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return false
		}
		_ = conn.SetDeadline(deadline)
		rb := make([]byte, 1500)
		n, peer, err := conn.ReadFrom(rb)
		if err != nil {
			return false
		}
		if peer.String() != dst {
			continue
		}
		parsed, err := icmp.ParseMessage(1, rb[:n])
		if err != nil {
			continue
		}
		if parsed.Type == ipv4.ICMPTypeEchoReply {
			if echo, ok := parsed.Body.(*icmp.Echo); ok && echo.Seq == wantSeq {
				return true
			}
		}
	}
	return false
}

// usableIPs mirrors the logic in scanner: skip network/broadcast for
// subnets wider than /30; return all for /31 and /32.
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
	c := make(net.IP, len(ip))
	copy(c, ip)
	return c
}

func incrementIP(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			break
		}
	}
}
