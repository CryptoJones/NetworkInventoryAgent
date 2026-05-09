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
	Name       string     `json:"name"`
	Healthy    bool       `json:"healthy"`
	StartedAt  time.Time  `json:"started_at"`
	LastScanAt *time.Time `json:"last_scan_at,omitempty"`
	HostCount  int        `json:"host_count"`
	ScanCount  int        `json:"scan_count"`
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
