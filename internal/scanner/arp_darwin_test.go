//go:build darwin

package scanner

import (
	"net"
	"testing"

	"golang.org/x/net/route"
)

// firstNeighbour returns one (ip, mac) pair from the live routing table that
// has a resolved link-layer gateway, or ("","") if none exist.
func firstNeighbour(t *testing.T) (string, string) {
	t.Helper()
	rib, err := route.FetchRIB(0, route.RIBTypeRoute, 0)
	if err != nil {
		t.Skipf("FetchRIB: %v", err)
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		t.Skipf("ParseRIB: %v", err)
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || len(rm.Addrs) <= rtaxGateway {
			continue
		}
		dst := routeAddrIP(rm.Addrs[rtaxDst])
		la, ok := rm.Addrs[rtaxGateway].(*route.LinkAddr)
		if dst == nil || dst.To4() == nil || !ok || len(la.Addr) != 6 {
			continue
		}
		return dst.String(), net.HardwareAddr(la.Addr).String()
	}
	return "", ""
}

func TestNeighbourMAC_Darwin(t *testing.T) {
	ip, mac := firstNeighbour(t)
	if ip == "" {
		t.Skip("no IPv4 ARP/neighbour entries in the routing table")
	}
	got := neighbourMAC(ip)
	if got != mac {
		t.Errorf("neighbourMAC(%s) = %q, want %q (from routing table)", ip, got, mac)
	}
}

func TestNeighbourMAC_NoMatch(t *testing.T) {
	// An address that won't have a neighbour entry must yield "" (no panic).
	if got := neighbourMAC("203.0.113.255"); got != "" {
		t.Errorf("expected empty for non-neighbour IP, got %q", got)
	}
	// Malformed input is handled.
	if got := neighbourMAC("not-an-ip"); got != "" {
		t.Errorf("expected empty for bad IP, got %q", got)
	}
}
