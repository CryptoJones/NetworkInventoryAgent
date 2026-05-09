package models

import "time"

type Host struct {
	ID            int64
	IPAddress     string
	MACAddress    string
	Hostname      string
	OSFingerprint string
	Vendor        string
	DeviceType    string
	FirstSeen     time.Time
	LastSeen      time.Time
}

type Port struct {
	ID        int64
	HostID    int64
	Port      int
	Protocol  string // "tcp" | "udp"
	Service   string
	State     string // "open" | "closed" | "filtered"
	FirstSeen time.Time
	LastSeen  time.Time
}

type Scan struct {
	ID         int64
	Subnet     string
	HostsFound int
	StartedAt  time.Time
	FinishedAt *time.Time // nil while the scan is still running
}
