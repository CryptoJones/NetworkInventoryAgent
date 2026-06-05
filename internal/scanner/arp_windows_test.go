//go:build windows

package scanner

import (
	"encoding/binary"
	"net"
	"testing"
)

// buildIPNetTable assembles a synthetic MIB_IPNETTABLE buffer from rows of
// (ip, mac) so parseIPNetTable can be tested without a live Windows host.
func buildIPNetTable(rows []struct {
	ip  string
	mac []byte
}) []byte {
	buf := make([]byte, 4+len(rows)*mibIPNetRowSize)
	binary.LittleEndian.PutUint32(buf[:4], uint32(len(rows)))
	for i, r := range rows {
		off := 4 + i*mibIPNetRowSize
		binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(len(r.mac))) // dwPhysAddrLen
		copy(buf[off+8:off+16], r.mac)                                      // bPhysAddr[8]
		ip := net.ParseIP(r.ip).To4()
		copy(buf[off+16:off+20], ip) // dwAddr (network byte order a.b.c.d)
	}
	return buf
}

func TestParseIPNetTable(t *testing.T) {
	rows := []struct {
		ip  string
		mac []byte
	}{
		{"10.0.0.5", []byte{0x00, 0x0c, 0x29, 0xaa, 0xbb, 0xcc}},
		{"10.0.0.6", []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}}, // zero MAC
		{"10.0.0.7", []byte{0xde, 0xad, 0xbe}},                   // wrong length (3)
	}
	buf := buildIPNetTable(rows)

	if got := parseIPNetTable(buf, net.ParseIP("10.0.0.5")); got != "00:0c:29:aa:bb:cc" {
		t.Errorf("match: got %q", got)
	}
	if got := parseIPNetTable(buf, net.ParseIP("10.0.0.6")); got != "" {
		t.Errorf("zero MAC should be empty, got %q", got)
	}
	if got := parseIPNetTable(buf, net.ParseIP("10.0.0.7")); got != "" {
		t.Errorf("non-6-byte MAC should be empty, got %q", got)
	}
	if got := parseIPNetTable(buf, net.ParseIP("10.0.0.99")); got != "" {
		t.Errorf("absent IP should be empty, got %q", got)
	}
	if got := parseIPNetTable([]byte{0x00}, net.ParseIP("10.0.0.5")); got != "" {
		t.Errorf("truncated buffer should be empty, got %q", got)
	}
}
