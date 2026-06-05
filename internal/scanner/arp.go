package scanner

import (
	"bufio"
	"os"
	"strings"
)

// arpPath is the kernel-exported ARP table on Linux. On other platforms it
// won't exist and the lookup is a silent no-op — that's intentional, we'd
// rather give nothing than misleading data.
//
// The file is a stable, undocumented-but-widely-used kernel interface that
// has held its format since at least 2.4. Each line after the header looks
// like:
//
//	IP address       HW type     Flags       HW address            Mask     Device
//	192.168.1.1      0x1         0x2         aa:bb:cc:dd:ee:ff     *        eth0
//
// We re-read it on every lookup rather than caching because (a) the file is
// small and lives in tmpfs, and (b) caching would force us to think about
// invalidation when a host's MAC changes.
var arpPath = "/proc/net/arp"

// lookupARP returns the MAC address and OUI vendor for ip if it is in the
// kernel's neighbour cache. Returns ("", "") for any of:
//   - non-Linux platforms (file missing)
//   - IP not in the table (host on a different subnet, or has not communicated recently)
//   - MAC has an incomplete flag (0x0)
func lookupARP(ip string) (string, string) {
	f, err := os.Open(arpPath)
	if err != nil {
		return "", ""
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	scanner.Scan() // header
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		// Flags column 2; 0x0 means "incomplete" — the kernel saw the address
		// but never resolved it. Skip those rows.
		if fields[0] != ip || fields[2] == "0x0" {
			continue
		}
		mac := strings.ToLower(fields[3])
		if mac == "" || mac == "00:00:00:00:00:00" {
			return "", ""
		}
		return mac, ouiVendor(mac)
	}
	return "", ""
}

// ouiVendor returns the vendor name for a MAC address based on its first
// three octets (the OUI). The embedded table covers the most common vendors
// seen on enterprise networks; an unknown OUI returns "" rather than a
// placeholder so the admin UI can hide the Vendor column when it's empty.
//
// To extend: drop more entries into the map below. The IEEE publishes the
// full assignment list at https://standards-oui.ieee.org/oui/oui.csv but
// shipping all ~30k entries would bloat the binary for marginal value.
func ouiVendor(mac string) string {
	if len(mac) < 8 {
		return ""
	}
	prefix := strings.ToLower(mac[:8])
	prefix = strings.ReplaceAll(prefix, "-", ":")
	if v, ok := ouiTable[prefix]; ok {
		return v
	}
	return ""
}

// ouiTable maps the lowercase "aa:bb:cc" OUI prefix to a vendor name. Kept
// short on purpose; see ouiVendor for the rationale.
var ouiTable = map[string]string{
	"00:00:0c": "Cisco",
	"00:1b:0d": "Cisco",
	"00:1b:54": "Cisco",
	"00:1e:13": "Cisco",
	"00:50:56": "VMware",
	"00:0c:29": "VMware",
	"00:1c:14": "VMware",
	"00:05:69": "VMware",
	"08:00:27": "VirtualBox",
	"52:54:00": "QEMU/KVM",
	"00:15:5d": "Microsoft Hyper-V",
	"00:03:ff": "Microsoft",
	"00:0d:3a": "Microsoft",
	"00:17:fa": "Microsoft",
	"00:25:ae": "Microsoft",
	"00:1d:d8": "Microsoft",
	"00:25:9c": "Cisco-Linksys",
	"00:0d:88": "D-Link",
	"00:24:01": "D-Link",
	"00:1f:1f": "Edimax",
	"00:14:bf": "Cisco-Linksys",
	"00:11:50": "Belkin",
	"00:1a:70": "Cisco-Linksys",
	"00:14:6c": "Netgear",
	"00:09:5b": "Netgear",
	"00:14:22": "Dell",
	"00:1c:23": "Dell",
	"00:24:e8": "Dell",
	"00:14:5e": "IBM",
	"00:21:5e": "HP",
	"00:1f:29": "HP",
	"00:23:7d": "HP",
	"00:25:b3": "HP",
	"3c:d9:2b": "HP",
	"d8:9d:67": "HP",
	"00:1c:bf": "Intel",
	"00:1f:3b": "Intel",
	"00:21:6a": "Intel",
	"00:24:d6": "Intel",
	"00:26:c6": "Intel",
	"00:1c:c0": "Intel",
	"00:13:02": "Intel",
	"00:11:24": "Apple",
	"00:14:51": "Apple",
	"00:16:cb": "Apple",
	"00:17:f2": "Apple",
	"00:19:e3": "Apple",
	"00:1b:63": "Apple",
	"00:1c:b3": "Apple",
	"00:1e:c2": "Apple",
	"00:1f:5b": "Apple",
	"00:1f:f3": "Apple",
	"00:21:e9": "Apple",
	"00:22:41": "Apple",
	"00:23:12": "Apple",
	"00:23:32": "Apple",
	"00:23:6c": "Apple",
	"00:23:df": "Apple",
	"00:25:00": "Apple",
	"00:25:4b": "Apple",
	"00:25:bc": "Apple",
	"00:26:08": "Apple",
	"00:26:4a": "Apple",
	"00:26:b0": "Apple",
	"00:26:bb": "Apple",
	"a4:5e:60": "Apple",
	"f4:5c:89": "Apple",
	"b8:27:eb": "Raspberry Pi Foundation",
	"dc:a6:32": "Raspberry Pi Foundation",
	"e4:5f:01": "Raspberry Pi Foundation",
	"2c:cf:67": "Raspberry Pi Foundation",
	"00:90:a9": "Western Digital",
	"00:14:ee": "Western Digital",
	"00:90:f5": "CLEVO",
	"00:0e:c6": "ASIX",
	"00:0c:42": "Routerboard.com (MikroTik)",
	"4c:5e:0c": "Routerboard.com (MikroTik)",
	"e4:8d:8c": "Routerboard.com (MikroTik)",
	"00:90:0b": "Lanner",
	"00:11:32": "Synology",
	"00:0e:7f": "HP Networking",
	"00:1f:33": "Netgear",
	"00:1e:2a": "Netgear",
	"40:b8:9a": "Netgear",
	"08:00:20": "Sun/Oracle",
	"00:03:ba": "Sun/Oracle",
	"00:14:4f": "Sun/Oracle",
	"00:1b:24": "Sun/Oracle",
	// IP-camera / NVR vendors (all prefixes verified against the IEEE
	// registry via maclookup.app). Feed the "camera" classifier rule.
	"44:19:b6": "Hikvision",
	"c0:56:e3": "Hikvision",
	"bc:ad:28": "Hikvision",
	"3c:ef:8c": "Dahua",
	"e0:50:8b": "Dahua",
	"00:40:8c": "Axis",
	"ac:cc:8e": "Axis",
	// QNAP NAS — feeds the "nas" classifier rule. (Note: 00:08:9b is
	// ICP Electronics, NOT QNAP — deliberately excluded.)
	"24:5e:be": "QNAP",
	// Ubiquiti network gear (APs / switches / gateways / cameras).
	"fc:ec:da": "Ubiquiti",
	"78:8a:20": "Ubiquiti",
	"dc:9f:db": "Ubiquiti",
	// Espressif (ESP8266/ESP32) — the silicon behind most DIY IoT;
	// feeds the "embedded" classifier rule.
	"24:0a:c4": "Espressif",
	"30:ae:a4": "Espressif",
	"18:fe:34": "Espressif",
}
