// Package health provides the shared status type, an HTTP server that exposes
// it, and a client for fetching it from a peer agent.
package health

import (
	"sync"
	"time"
)

// Status is the snapshot each agent publishes about itself. Both the local
// agent and remote peers are represented with this type so comparison logic
// stays uniform.
type Status struct {
	Name       string      `json:"name"`
	Healthy    bool        `json:"healthy"`
	StartedAt  time.Time   `json:"started_at"`
	LastScanAt *time.Time  `json:"last_scan_at,omitempty"`
	HostCount  int         `json:"host_count"`
	ScanCount  int         `json:"scan_count"`
	Peer       *PeerStatus `json:"peer,omitempty"`
}

// PeerStatus is the watchdog's view of the other agent. It is written by the
// watchdog after each tick and read by the /status endpoint, the admin
// console, and any external monitor.
type PeerStatus struct {
	// Addr is the peer's health-server base URL (e.g. http://neuromancer:8081).
	Addr string `json:"addr"`
	// Reachable is true iff the most recent liveness probe succeeded.
	Reachable bool `json:"reachable"`
	// ConsecutiveFailures is the count of back-to-back probe failures; resets to 0 on success.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// LastCheckedAt is the time of the most recent watchdog tick.
	LastCheckedAt time.Time `json:"last_checked_at"`
	// LastError is the most recent probe error, if any.
	LastError string `json:"last_error,omitempty"`
	// PeerHostCount and PeerScanCount mirror the last status fetched from the peer.
	PeerHostCount int `json:"peer_host_count"`
	PeerScanCount int `json:"peer_scan_count"`
	// DriftPct is the inventory drift between this agent and the peer.
	DriftPct float64 `json:"drift_pct"`
	// Stale is true iff the peer's most recent scan is older than the freshness threshold.
	Stale bool `json:"stale"`
}

// Tracker is a concurrency-safe holder for a Status. The agent loop writes to
// it; the health server and watchdog read from it.
type Tracker struct {
	mu     sync.RWMutex
	status Status
}

func NewTracker(name string) *Tracker {
	return &Tracker{
		status: Status{
			Name:      name,
			Healthy:   true,
			StartedAt: time.Now().UTC(),
		},
	}
}

func (t *Tracker) Get() Status {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.status
}

func (t *Tracker) SetHostCount(n int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.HostCount = n
}

func (t *Tracker) RecordScan() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now().UTC()
	t.status.LastScanAt = &now
	t.status.ScanCount++
}

func (t *Tracker) SetHealthy(ok bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.status.Healthy = ok
}

// SetPeer publishes the watchdog's latest view of the peer agent. It is
// stored on the Status so /status, the admin /watchdog page, and external
// monitors can all read the same authoritative snapshot.
func (t *Tracker) SetPeer(p PeerStatus) {
	t.mu.Lock()
	defer t.mu.Unlock()
	clone := p
	t.status.Peer = &clone
}
