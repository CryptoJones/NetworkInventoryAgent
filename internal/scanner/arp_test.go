package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupARP_FileMissing(t *testing.T) {
	orig := arpPath
	t.Cleanup(func() { arpPath = orig })
	arpPath = "/nonexistent/proc/net/arp"

	mac, vendor := lookupARP("10.0.0.1")
	assert.Empty(t, mac)
	assert.Empty(t, vendor)
}

func TestLookupARP_ParsesEntry(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "arp")
	require.NoError(t, os.WriteFile(p, []byte(
		"IP address       HW type     Flags       HW address            Mask     Device\n"+
			"10.0.0.5         0x1         0x2         00:0c:29:aa:bb:cc     *        eth0\n"+
			"10.0.0.6         0x1         0x0         00:00:00:00:00:00     *        eth0\n", // incomplete
	), 0o644))

	orig := arpPath
	t.Cleanup(func() { arpPath = orig })
	arpPath = p

	mac, vendor := lookupARP("10.0.0.5")
	assert.Equal(t, "00:0c:29:aa:bb:cc", mac)
	assert.Equal(t, "VMware", vendor)

	// Incomplete entries are skipped.
	mac, vendor = lookupARP("10.0.0.6")
	assert.Empty(t, mac)
	assert.Empty(t, vendor)

	// Unknown IP returns nothing.
	mac, vendor = lookupARP("10.0.0.99")
	assert.Empty(t, mac)
	assert.Empty(t, vendor)
}

func TestOUIVendor_UnknownPrefix(t *testing.T) {
	v := ouiVendor("ff:ff:ff:11:22:33")
	assert.Empty(t, v)
}

func TestOUIVendor_KnownPrefix(t *testing.T) {
	assert.Equal(t, "Cisco", ouiVendor("00:00:0c:11:22:33"))
	// Mixed case input is normalised.
	assert.Equal(t, "Apple", ouiVendor("A4:5E:60:11:22:33"))
}
