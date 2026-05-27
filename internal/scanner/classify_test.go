package scanner

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name   string
		vendor string
		osfp   string
		tcp    []int
		udp    []int
		want   string
	}{
		{
			name: "printer JetDirect 9100",
			tcp:  []int{9100},
			want: "printer",
		},
		{
			name: "printer IPP 631 alone",
			tcp:  []int{631},
			want: "printer",
		},
		{
			name:   "mikrotik router by vendor",
			vendor: "Routerboard.com (MikroTik)",
			tcp:    []int{22, 80, 443},
			want:   "router",
		},
		{
			name: "mikrotik router by API port even without vendor",
			tcp:  []int{8728},
			want: "router",
		},
		{
			name:   "cisco router",
			vendor: "Cisco",
			tcp:    []int{22, 80, 443},
			want:   "router",
		},
		{
			name:   "esxi hypervisor",
			vendor: "VMware",
			tcp:    []int{22, 80, 443, 902, 5989},
			want:   "hypervisor",
		},
		{
			name: "mysql database",
			tcp:  []int{22, 3306},
			want: "database (mysql)",
		},
		{
			name: "postgres database",
			tcp:  []int{5432, 22},
			want: "database (postgres)",
		},
		{
			name: "mssql database",
			tcp:  []int{1433},
			want: "database (mssql)",
		},
		{
			name: "mongodb",
			tcp:  []int{27017},
			want: "database (mongodb)",
		},
		{
			name: "redis",
			tcp:  []int{6379},
			want: "database (redis)",
		},
		{
			name: "windows host with SMB only",
			tcp:  []int{445, 135, 139},
			want: "windows-host",
		},
		{
			name: "windows server (SMB+IIS)",
			tcp:  []int{445, 80, 443, 3389},
			want: "windows-server",
		},
		{
			name: "windows host with RDP only",
			tcp:  []int{3389},
			want: "windows-host",
		},
		{
			name: "mail server (smtp+imap)",
			tcp:  []int{25, 143, 993, 587},
			want: "mail-server",
		},
		{
			name: "windows-dc with SMB still wins over generic windows",
			tcp:  []int{53, 88, 389, 445},
			want: "windows-dc",
		},
		{
			name: "active directory without SMB",
			tcp:  []int{53, 88, 389, 636},
			want: "windows-dc",
		},
		{
			name: "dns server (tcp)",
			tcp:  []int{53, 22},
			want: "dns-server",
		},
		{
			name: "dns server (udp)",
			udp:  []int{53},
			want: "dns-server",
		},
		{
			name: "mqtt broker",
			tcp:  []int{1883, 22},
			want: "iot-broker",
		},
		{
			name:   "raspberry pi by vendor + ssh",
			vendor: "Raspberry Pi Foundation",
			tcp:    []int{22},
			want:   "embedded",
		},
		{
			name: "linux host (openssh banner + ssh only)",
			osfp: "SSH-2.0-OpenSSH_9.6p1 Ubuntu-3ubuntu13.5",
			tcp:  []int{22},
			want: "linux-host",
		},
		{
			name: "appliance (https only, no shell port)",
			tcp:  []int{443},
			want: "appliance",
		},
		{
			name: "appliance (http+https only)",
			tcp:  []int{80, 443},
			want: "appliance",
		},
		{
			name: "ssh-only no banner falls through to linux-host",
			tcp:  []int{22},
			want: "linux-host",
		},
		{
			name: "no match returns empty",
			tcp:  []int{4242},
			want: "",
		},
		{
			name: "empty everything returns empty",
			want: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classify(c.vendor, c.osfp, c.tcp, c.udp)
			if got != c.want {
				t.Errorf("classify(vendor=%q, osfp=%q, tcp=%v, udp=%v) = %q; want %q",
					c.vendor, c.osfp, c.tcp, c.udp, got, c.want)
			}
		})
	}
}
