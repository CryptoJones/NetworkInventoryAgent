// Package models defines the core domain types for the network inventory.
// These types are shared across all layers of the application and carry no
// database or HTTP imports.
package models

import "time"

// Protocol represents a network transport protocol.
type Protocol string

const (
	TCP Protocol = "tcp"
	UDP Protocol = "udp"
)

// PortState represents the observed state of a scanned port.
type PortState string

const (
	StateOpen     PortState = "open"
	StateClosed   PortState = "closed"
	StateFiltered PortState = "filtered"
)

// Host is a network-attached device discovered by the scanner.
type Host struct {
	// ID is the database primary key, assigned on first insert.
	ID int64
	// IPAddress is the host's primary IPv4 or IPv6 address.
	IPAddress string
	// MACAddress is the hardware address, if discoverable.
	MACAddress string
	// Hostname is the reverse-DNS name, if resolvable.
	Hostname string
	// OSFingerprint is the operating system identification string.
	OSFingerprint string
	// Vendor is the NIC vendor, if known.
	Vendor string
	// DeviceType classifies the device (e.g. "server", "router", "printer").
	DeviceType string
	// FirstSeen is the time this host was first recorded in the inventory.
	FirstSeen time.Time
	// LastSeen is the time this host was most recently confirmed active.
	LastSeen time.Time
}

// Port is an open TCP or UDP port observed on a Host during a scan.
type Port struct {
	// ID is the database primary key.
	ID int64
	// HostID is the foreign key referencing the parent Host.
	HostID int64
	// Number is the port number (1–65535).
	Number int
	// Protocol is the transport protocol (TCP or UDP).
	Protocol Protocol
	// Service is the name of the service inferred from the port number or banner.
	Service string
	// State is the observed port state.
	State PortState
	// FirstSeen is the time this port was first recorded for this host.
	FirstSeen time.Time
	// LastSeen is the time this port was most recently observed.
	LastSeen time.Time
}

// Scan records a single sweep of a subnet.
type Scan struct {
	// ID is the database primary key.
	ID int64
	// Subnet is the CIDR range that was scanned.
	Subnet string
	// HostsFound is the number of live hosts discovered.
	HostsFound int
	// StartedAt is the time the scan began.
	StartedAt time.Time
	// FinishedAt is the time the scan completed; nil while in progress.
	FinishedAt *time.Time
}
