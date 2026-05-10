package scanner

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsableIPs_Slash24_SkipsNetworkAndBroadcast(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("192.168.1.0/24")
	require.NoError(t, err)

	ips := usableIPs(ipNet)

	assert.Len(t, ips, 254)
	assert.Equal(t, "192.168.1.1", ips[0], "first usable address should be .1, not .0 (network)")
	assert.Equal(t, "192.168.1.254", ips[len(ips)-1], "last usable address should be .254, not .255 (broadcast)")
}

func TestUsableIPs_Slash30_TwoHosts(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("10.0.0.0/30")
	require.NoError(t, err)

	ips := usableIPs(ipNet)

	// /30: .0 = network, .1 and .2 = usable, .3 = broadcast
	assert.Len(t, ips, 2)
	assert.Equal(t, "10.0.0.1", ips[0])
	assert.Equal(t, "10.0.0.2", ips[1])
}

func TestUsableIPs_Slash31_IncludesAll(t *testing.T) {
	// RFC 3021 point-to-point links: no network or broadcast address.
	_, ipNet, err := net.ParseCIDR("10.0.0.0/31")
	require.NoError(t, err)

	ips := usableIPs(ipNet)

	assert.Len(t, ips, 2)
	assert.Equal(t, "10.0.0.0", ips[0])
	assert.Equal(t, "10.0.0.1", ips[1])
}

func TestUsableIPs_Slash32_SingleHost(t *testing.T) {
	_, ipNet, err := net.ParseCIDR("10.0.0.1/32")
	require.NoError(t, err)

	ips := usableIPs(ipNet)

	assert.Len(t, ips, 1)
	assert.Equal(t, "10.0.0.1", ips[0])
}
