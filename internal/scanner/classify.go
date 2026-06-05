package scanner

import (
	"strings"
)

// classify returns a coarse device-type label for a host given its OUI
// vendor (from ARP), its OS fingerprint banner, and the TCP + UDP ports
// that came back open during the scan. Returns "" if no rule matches —
// the admin UI hides empty device-type cells rather than guessing.
//
// Ordering matters: more-specific rules fire first. The classifier is
// deliberately conservative — we'd rather say "" than mislabel an
// asset (a wrong "printer" tag on a server is worse than no tag).
func classify(vendor, osfp string, tcp, udp []int) string {
	tcpSet := indexSet(tcp)
	udpSet := indexSet(udp)
	vlow := strings.ToLower(vendor)
	osfpLow := strings.ToLower(osfp)

	// ── Print servers ─────────────────────────────────────────────
	// JetDirect raw-print 9100, IPP 631, LPD 515 — any one of these
	// is a strong signal even on its own.
	if tcpSet[9100] || tcpSet[631] || tcpSet[515] {
		return "printer"
	}

	// ── Network infrastructure ────────────────────────────────────
	// MikroTik RouterOS exposes 8728/8729 (API) and 22+80+443 are the
	// common management surfaces. The vendor check pins it.
	if strings.Contains(vlow, "mikrotik") || tcpSet[8728] || tcpSet[8729] {
		return "router"
	}
	if strings.Contains(vlow, "cisco") && (tcpSet[22] || tcpSet[23] || tcpSet[80] || tcpSet[443]) {
		return "router"
	}

	// ── Hypervisors ───────────────────────────────────────────────
	// ESXi exposes 902 (vsphere SDK) and the host typically has many
	// ports including 22, 80, 443, 5988/5989 (CIM).
	if strings.Contains(vlow, "vmware") && (tcpSet[902] || tcpSet[5988] || tcpSet[5989]) {
		return "hypervisor"
	}
	// Other virtualization stacks are identifiable by NIC OUI alone
	// (QEMU/KVM, VirtualBox, Microsoft Hyper-V) or, for Proxmox VE, its
	// 8006 management port.
	if strings.Contains(vlow, "qemu") || strings.Contains(vlow, "kvm") ||
		strings.Contains(vlow, "virtualbox") || strings.Contains(vlow, "hyper-v") {
		return "hypervisor"
	}
	if tcpSet[8006] {
		return "hypervisor"
	}

	// ── Container / orchestration platforms ───────────────────────
	// Unique control-plane ports. These sit outside the default
	// deep-probe list, so they only fire when an operator probes them —
	// but the labels are precise when the data is present.
	if tcpSet[6443] || tcpSet[2379] || tcpSet[10250] {
		return "kubernetes-node"
	}
	if tcpSet[2375] || tcpSet[2376] {
		return "container-host"
	}

	// ── Database servers ──────────────────────────────────────────
	// Database ports are uniquely strong signals — almost nothing
	// else listens on 3306, 5432, 1433, 27017, 6379.
	switch {
	case tcpSet[3306]:
		return "database (mysql)"
	case tcpSet[5432]:
		return "database (postgres)"
	case tcpSet[1433]:
		return "database (mssql)"
	case tcpSet[27017]:
		return "database (mongodb)"
	case tcpSet[6379]:
		return "database (redis)"
	case tcpSet[11211]:
		return "database (memcached)"
	}

	// ── Storage / NAS ─────────────────────────────────────────────
	// Vendor OUI pins Synology / Western Digital; otherwise NFS (2049)
	// alongside SMB (445) is the NAS signature. Must precede the Windows
	// SMB rule below so a NAS isn't mislabelled windows-host.
	if strings.Contains(vlow, "synology") || strings.Contains(vlow, "western digital") ||
		strings.Contains(vlow, "qnap") {
		return "nas"
	}
	if tcpSet[2049] && tcpSet[445] {
		return "nas"
	}

	// ── Active Directory / Windows DC ─────────────────────────────
	// Kerberos (88) + LDAP (389) is the signature; SMB and DNS
	// usually ride along but aren't required. Must fire BEFORE the
	// generic SMB rule below so a DC isn't mislabelled as a
	// plain windows-host.
	if tcpSet[88] && tcpSet[389] {
		return "windows-dc"
	}

	// ── Windows hosts ─────────────────────────────────────────────
	// SMB (445) is the giveaway. RDP (3389) and WinRM (5985/5986)
	// reinforce. Distinguish server (also serves HTTP) from workstation.
	if tcpSet[445] || tcpSet[5985] || tcpSet[5986] {
		if tcpSet[80] || tcpSet[443] || tcpSet[25] {
			return "windows-server"
		}
		return "windows-host"
	}
	// RDP alone (without SMB) is often a Windows VM with the
	// firewall blocking SMB — still a Windows host.
	if tcpSet[3389] {
		return "windows-host"
	}

	// ── Mail servers ──────────────────────────────────────────────
	// SMTP (25/587/465) + IMAP (143/993) or POP3 (110/995) is the
	// classic combo. SMTP alone is enough on a non-workstation.
	if (tcpSet[25] || tcpSet[465] || tcpSet[587]) &&
		(tcpSet[143] || tcpSet[993] || tcpSet[110] || tcpSet[995]) {
		return "mail-server"
	}

	// ── DNS servers ───────────────────────────────────────────────
	// DNS on TCP/53 OR UDP/53. Many devices answer recursive DNS;
	// require it to NOT also look like a Windows DC (which has 53 +
	// 88 Kerberos + 389 LDAP).
	if (tcpSet[53] || udpSet[53]) && !tcpSet[88] && !tcpSet[389] {
		return "dns-server"
	}

	// ── IoT / embedded ────────────────────────────────────────────
	// Raspberry Pi OUI + SSH-only is the canonical pi-hole / sensor
	// shape. MQTT brokers (1883/8883) are clear IoT signals.
	if tcpSet[1883] || tcpSet[8883] {
		return "iot-broker"
	}
	if strings.Contains(vlow, "raspberry pi") || strings.Contains(vlow, "espressif") {
		return "embedded"
	}

	// ── IP cameras / NVR ──────────────────────────────────────────
	// Vendor OUI pins the major video brands; otherwise RTSP (554) is the
	// unambiguous signal (outside the default deep-probe list, so it fires
	// when an operator probes it).
	if strings.Contains(vlow, "hikvision") || strings.Contains(vlow, "dahua") ||
		strings.Contains(vlow, "axis") {
		return "camera"
	}
	if tcpSet[554] {
		return "camera"
	}

	// ── SSH-banner-driven OS hints ────────────────────────────────
	// fingerprint() records the SSH greeting as the OS fingerprint
	// when port 22 answered. OpenSSH on Linux is the bulk of these.
	if strings.HasPrefix(osfpLow, "ssh-") {
		switch {
		case strings.Contains(osfpLow, "openssh") && !strings.Contains(osfpLow, "windows"):
			return "linux-host"
		case strings.Contains(osfpLow, "openssh_for_windows"):
			return "windows-host"
		}
	}

	// ── Generic web server ────────────────────────────────────────
	// HTTP(S) without any host-shell port (no 22/23/3389/445) is
	// most often an appliance or load balancer.
	if (tcpSet[80] || tcpSet[443] || tcpSet[8080] || tcpSet[8443]) &&
		!tcpSet[22] && !tcpSet[23] && !tcpSet[3389] && !tcpSet[445] {
		return "appliance"
	}

	// ── Generic Linux host (SSH only) ─────────────────────────────
	if tcpSet[22] && len(tcp) <= 3 {
		return "linux-host"
	}

	return ""
}

// indexSet builds a port-membership lookup. Cheap to construct (these
// slices have <50 entries in practice) and much clearer at the call
// site than a Contains helper per check.
func indexSet(ports []int) map[int]bool {
	if len(ports) == 0 {
		return nil
	}
	m := make(map[int]bool, len(ports))
	for _, p := range ports {
		m[p] = true
	}
	return m
}
