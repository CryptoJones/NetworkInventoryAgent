//go:build darwin

package scanner

import (
	"net"

	"golang.org/x/net/route"
)

// Indices into route.RouteMessage.Addrs (the RTAX_* slots). The route package
// does not export these constants; the order is part of the stable routing
// socket ABI.
const (
	rtaxDst     = 0
	rtaxGateway = 1
)

// neighbourMAC resolves ip to a link-layer address from the macOS routing
// table, where the kernel keeps the ARP/NDP cache. It dumps the routing
// information base via a routing socket (golang.org/x/net/route) — no shell
// invocation and no /proc dependency, preserving the project's "no external
// processes" posture. Returns "" if ip has no resolved neighbour entry.
func neighbourMAC(ip string) string {
	target := net.ParseIP(ip)
	if target == nil {
		return ""
	}
	rib, err := route.FetchRIB(0 /* AF_UNSPEC */, route.RIBTypeRoute, 0)
	if err != nil {
		return ""
	}
	msgs, err := route.ParseRIB(route.RIBTypeRoute, rib)
	if err != nil {
		return ""
	}
	for _, m := range msgs {
		rm, ok := m.(*route.RouteMessage)
		if !ok || len(rm.Addrs) <= rtaxGateway {
			continue
		}
		dst := routeAddrIP(rm.Addrs[rtaxDst])
		if dst == nil || !dst.Equal(target) {
			continue
		}
		la, ok := rm.Addrs[rtaxGateway].(*route.LinkAddr)
		if !ok || len(la.Addr) != 6 {
			continue
		}
		return net.HardwareAddr(la.Addr).String()
	}
	return ""
}

// routeAddrIP extracts a net.IP from a routing-socket address, or nil if it
// isn't an IPv4/IPv6 address (e.g. a link or netmask slot).
func routeAddrIP(a route.Addr) net.IP {
	switch v := a.(type) {
	case *route.Inet4Addr:
		return net.IPv4(v.IP[0], v.IP[1], v.IP[2], v.IP[3])
	case *route.Inet6Addr:
		ip := make(net.IP, net.IPv6len)
		copy(ip, v.IP[:])
		return ip
	default:
		return nil
	}
}
