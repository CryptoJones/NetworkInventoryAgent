//go:build windows

package scanner

import (
	"encoding/binary"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi          = windows.NewLazySystemDLL("iphlpapi.dll")
	procGetIPNetTable = iphlpapi.NewProc("GetIpNetTable")
)

// mibIPNetRowSize is the packed size of a MIB_IPNETROW: dwIndex(4) +
// dwPhysAddrLen(4) + bPhysAddr[8] + dwAddr(4) + dwType(4). All fields are
// 4-byte aligned, so the row needs no trailing padding and the table's row
// array begins at offset 4 (after dwNumEntries).
const mibIPNetRowSize = 24

// neighbourMAC resolves ip via the Windows ARP table (GetIpNetTable from
// iphlpapi.dll), with no shell invocation — consistent with the Linux/macOS
// implementations. Returns "" if ip has no resolved entry or the lookup
// fails. Runtime-unverified on this build host (compile-checked only);
// degrades safely to "" on any error.
func neighbourMAC(ip string) string {
	target := net.ParseIP(ip).To4()
	if target == nil {
		return ""
	}

	// First call with a nil buffer to learn the required size.
	var size uint32
	r, _, _ := procGetIPNetTable.Call(0, uintptr(unsafe.Pointer(&size)), 0)
	if windows.Errno(r) != windows.ERROR_INSUFFICIENT_BUFFER || size < 4 {
		return ""
	}

	buf := make([]byte, size)
	r, _, _ = procGetIPNetTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0)
	if r != 0 { // anything other than NO_ERROR
		return ""
	}
	return parseIPNetTable(buf, target)
}

// parseIPNetTable scans a MIB_IPNETTABLE byte buffer for target's MAC. Split
// out from the syscall so the (fiddly, offset-sensitive) parsing is unit
// testable without a live Windows host. Returns "" if absent, the entry isn't
// a 6-byte Ethernet MAC, or the MAC is all-zero.
func parseIPNetTable(buf []byte, target net.IP) string {
	if len(buf) < 4 {
		return ""
	}
	n := binary.LittleEndian.Uint32(buf[:4])
	for i := uint32(0); i < n; i++ {
		off := 4 + int(i)*mibIPNetRowSize
		if off+mibIPNetRowSize > len(buf) {
			break
		}
		physLen := binary.LittleEndian.Uint32(buf[off+4 : off+8])
		if physLen != 6 {
			continue // not a standard 6-byte Ethernet MAC
		}
		// dwAddr is the IPv4 address in network byte order (a.b.c.d).
		if !net.IP(buf[off+16 : off+20]).Equal(target) {
			continue
		}
		mac := net.HardwareAddr(append([]byte(nil), buf[off+8:off+14]...))
		s := mac.String()
		if s == "00:00:00:00:00:00" {
			return ""
		}
		return s
	}
	return ""
}
