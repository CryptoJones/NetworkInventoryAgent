// Package metrics is a tiny dependency-free Prometheus exposer.
//
// The agent only needs a handful of monotonic counters and gauges; pulling in
// prometheus/client_golang and its transitive deps is more than the use case
// justifies. This package keeps a process-global Registry whose Handler emits
// the standard Prometheus text exposition format on GET /metrics.
//
// All metric values are taken at the moment of scrape — no separate collection
// goroutine — so callers just increment/set as side effects of their normal
// work. The Registry is safe for concurrent use.
package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Counter is a monotonically increasing integer. Use Inc / Add.
type Counter struct {
	v atomic.Int64
}

// Inc adds one to the counter.
func (c *Counter) Inc() { c.v.Add(1) }

// Add adds n to the counter. n is expected to be non-negative; negative values
// are ignored so we don't accidentally produce a non-monotonic series.
func (c *Counter) Add(n int64) {
	if n <= 0 {
		return
	}
	c.v.Add(n)
}

// Get returns the current value. Used by the exposer; rarely needed elsewhere.
func (c *Counter) Get() int64 { return c.v.Load() }

// Gauge is an integer value that can go up or down.
type Gauge struct {
	v atomic.Int64
}

// Set replaces the gauge value.
func (g *Gauge) Set(n int64) { g.v.Store(n) }

// Inc / Dec adjust the gauge.
func (g *Gauge) Inc() { g.v.Add(1) }
func (g *Gauge) Dec() { g.v.Add(-1) }

// Get returns the current value.
func (g *Gauge) Get() int64 { return g.v.Load() }

// Registry holds the metrics exposed by an agent. There is one process-global
// Registry (see Default) but tests can construct their own.
type Registry struct {
	mu       sync.RWMutex
	counters map[string]*counterEntry
	gauges   map[string]*gaugeEntry
}

type counterEntry struct {
	help string
	c    *Counter
}

type gaugeEntry struct {
	help string
	g    *Gauge
}

// NewRegistry returns an empty Registry. The Handler it produces is safe to
// mount on any *http.ServeMux.
func NewRegistry() *Registry {
	return &Registry{
		counters: map[string]*counterEntry{},
		gauges:   map[string]*gaugeEntry{},
	}
}

// Counter registers (or returns the existing) Counter under name. help is the
// human-readable description emitted as the # HELP comment.
func (r *Registry) Counter(name, help string) *Counter {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.counters[name]; ok {
		return e.c
	}
	c := &Counter{}
	r.counters[name] = &counterEntry{help: help, c: c}
	return c
}

// Gauge registers (or returns the existing) Gauge under name.
func (r *Registry) Gauge(name, help string) *Gauge {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.gauges[name]; ok {
		return e.g
	}
	g := &Gauge{}
	r.gauges[name] = &gaugeEntry{help: help, g: g}
	return g
}

// Handler returns an http.Handler that writes the Prometheus text exposition
// format on every request. It is goroutine-safe.
func (r *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.writeText(w)
	})
}

func (r *Registry) writeText(w http.ResponseWriter) {
	r.mu.RLock()
	names := make([]string, 0, len(r.counters)+len(r.gauges))
	for n := range r.counters {
		names = append(names, n)
	}
	for n := range r.gauges {
		names = append(names, n)
	}
	r.mu.RUnlock()
	sort.Strings(names)

	var sb strings.Builder
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, n := range names {
		if c, ok := r.counters[n]; ok {
			fmt.Fprintf(&sb, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", n, c.help, n, n, c.c.Get())
			continue
		}
		if g, ok := r.gauges[n]; ok {
			fmt.Fprintf(&sb, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", n, g.help, n, n, g.g.Get())
		}
	}
	_, _ = w.Write([]byte(sb.String()))
}

// Default is the process-global Registry. Application code uses the package
// helpers below (ScansTotal, ProbeSuccessTotal, ...) which read/write Default.
var Default = NewRegistry()

// The agent's exposed metrics. Defined eagerly so the package's import order
// determines all of them and a fresh Registry is consistent before the first
// scrape.
var (
	ScansTotal            = Default.Counter("inventory_scans_total", "Subnet scans completed (success or failure)")
	ScanErrorsTotal       = Default.Counter("inventory_scan_errors_total", "Subnet scans that returned an error")
	HostsUpsertedTotal    = Default.Counter("inventory_hosts_upserted_total", "Host upsert calls issued by the scanner")
	PortsUpsertedTotal    = Default.Counter("inventory_ports_upserted_total", "Port upsert calls issued by the scanner")
	ProbeSuccessTotal     = Default.Counter("inventory_probe_success_total", "TCP probes that found an open port")
	ProbeFailureTotal     = Default.Counter("inventory_probe_failure_total", "TCP probes that found no open port")
	UDPProbeSuccessTotal  = Default.Counter("inventory_udp_probe_success_total", "UDP probes that received a response")
	UDPProbeFailureTotal  = Default.Counter("inventory_udp_probe_failure_total", "UDP probes that got a definitive closed (ICMP unreachable) response")
	DBErrorsTotal         = Default.Counter("inventory_db_errors_total", "Database operations that returned an error")
	WatchdogChecksTotal   = Default.Counter("inventory_watchdog_checks_total", "Watchdog ticks executed")
	WatchdogFailuresTotal = Default.Counter("inventory_watchdog_failures_total", "Watchdog ticks where the peer was unreachable")
	WatchdogPeerDownTotal = Default.Counter("inventory_watchdog_peer_down_total", "Times the peer has been declared DOWN")
	HostsPrunedTotal      = Default.Counter("inventory_hosts_pruned_total", "Hosts deleted by the staleness pruner")
	ScansPrunedTotal      = Default.Counter("inventory_scans_pruned_total", "Scan-history rows deleted by the retention pruner")
	ScanTriggersTotal     = Default.Counter("inventory_scan_triggers_total", "On-demand scans accepted via POST /scan")

	HostCount = Default.Gauge("inventory_host_count", "Current number of hosts in the inventory")
	PeerUp    = Default.Gauge("inventory_peer_up", "1 if the watchdog peer is reachable, 0 otherwise; -1 if no watchdog configured")
)

// ResetAll zeroes every metric. Tests only — production code should never
// reset counters because Prometheus expects monotonicity across restarts.
func ResetAll() {
	Default.mu.Lock()
	defer Default.mu.Unlock()
	for _, e := range Default.counters {
		e.c.v.Store(0)
	}
	for _, e := range Default.gauges {
		e.g.v.Store(0)
	}
}
