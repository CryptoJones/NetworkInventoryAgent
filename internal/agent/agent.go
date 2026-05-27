// Package agent implements the core scan loop that drives an inventory agent
// instance. It ties together the network scanner, the persistence layer, and
// the health tracker so that the watchdog always has fresh data to compare.
package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/alerts"
	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/metrics"
	"github.com/Ronin48/NetworkInventoryAgent/internal/scanner"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
	"github.com/Ronin48/NetworkInventoryAgent/models"
)

// Agent runs a periodic scan loop and publishes results to a health.Tracker.
type Agent struct {
	name    string
	cfg     config.ScannerConfig
	hosts   store.HostStore
	scanner *scanner.Scanner
	tracker *health.Tracker
	alerts  alerts.Emitter
	now     func() time.Time

	// profiles is the resolved per-subnet config built at New time.
	// Each entry carries its own ScanInterval; nextDue tracks when its
	// next scan is permitted.
	profiles []config.ResolvedProfile
	nextDue  map[string]time.Time

	// trigger is a buffered channel that lets external callers
	// (e.g. POST /scan) force an immediate cycle without waiting for the
	// next ticker firing. Capacity 1 so concurrent triggers coalesce.
	trigger chan struct{}
}

// New creates an Agent. The caller is responsible for starting the health
// server and watchdog; this constructor only wires the scan loop. Pass
// alerts.NoopEmitter() when alerts are unconfigured (the constructor
// substitutes one if alertEmitter is nil to avoid an awkward call-site
// guard at every binding).
func New(
	name string,
	cfg config.ScannerConfig,
	hosts store.HostStore,
	ports store.PortStore,
	scans store.ScanStore,
	tracker *health.Tracker,
	alertEmitter alerts.Emitter,
) (*Agent, error) {
	if alertEmitter == nil {
		alertEmitter = alerts.NoopEmitter()
	}
	profiles, err := cfg.Resolve()
	if err != nil {
		return nil, err
	}
	return &Agent{
		name:  name,
		cfg:   cfg,
		hosts: hosts,
		scanner: scanner.New(scanner.Options{
			Hosts:          hosts,
			Ports:          ports,
			Scans:          scans,
			Timeout:        cfg.Timeout.Duration,
			Workers:        cfg.Workers,
			MaxHosts:       cfg.MaxHosts,
			ProbePorts:     cfg.ProbePorts,
			DeepProbe:      cfg.DeepProbe,
			DeepProbePorts: cfg.DeepProbePorts,
			UDPPorts:       cfg.UDPPorts,
			EnrichARP:      cfg.EnrichARP,
		}),
		tracker:  tracker,
		alerts:   alertEmitter,
		now:      time.Now,
		profiles: profiles,
		nextDue:  make(map[string]time.Time, len(profiles)),
		trigger:  make(chan struct{}, 1),
	}, nil
}

// Trigger requests an out-of-cycle scan. Returns true if the request was
// queued (channel capacity available), false if a trigger is already
// pending — in that case the queued cycle will satisfy the new request too.
// Safe to call from HTTP handlers.
func (a *Agent) Trigger() bool {
	select {
	case a.trigger <- struct{}{}:
		metrics.ScanTriggersTotal.Inc()
		return true
	default:
		return false
	}
}

// Run starts the scan loop. It executes one cycle immediately (every
// profile, regardless of its own interval), then ticks at the shortest
// configured per-profile interval. Each subsequent tick only scans
// profiles whose next-due time has passed. Blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	log := slog.With("agent", a.name)
	log.Info("scan loop started",
		"profiles", len(a.profiles),
		"tick_interval", a.tickInterval(),
	)

	a.runCycle(ctx, log, true /* forceAll */)

	ticker := time.NewTicker(a.tickInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("scan loop stopped")
			return
		case <-ticker.C:
			a.runCycle(ctx, log, false)
		case <-a.trigger:
			log.Info("on-demand scan triggered")
			a.runCycle(ctx, log, true)
		}
	}
}

// tickInterval is the shortest configured profile interval. The main
// loop ticks at this cadence and selects per-tick which profiles are
// actually due. A safety floor of one second prevents pathological
// busy-loops when an operator types "1ns" by accident.
func (a *Agent) tickInterval() time.Duration {
	const floor = time.Second
	min := time.Duration(0)
	for _, p := range a.profiles {
		if p.ScanInterval <= 0 {
			continue
		}
		if min == 0 || p.ScanInterval < min {
			min = p.ScanInterval
		}
	}
	if min < floor {
		min = floor
	}
	return min
}

// runCycle scans every profile whose nextDue has passed (or all of them
// when forceAll is true — used for the initial cycle and on-demand
// triggers).
func (a *Agent) runCycle(ctx context.Context, log *slog.Logger, forceAll bool) {
	started := a.now()

	// Snapshot the pre-cycle host inventory so we can diff it against
	// the post-cycle list and fire HostDiscovered / HostVanished events.
	prevHosts := snapshotByIP(ctx, a.hosts, log)

	// Select due profiles. With no profiles configured (zero-config
	// "watchdog only" deployment), due is empty but housekeeping below
	// — prune, diff, tracker updates — still runs.
	due := make([]config.ResolvedProfile, 0, len(a.profiles))
	for _, p := range a.profiles {
		if forceAll || !started.Before(a.nextDue[p.Subnet]) {
			due = append(due, p)
		}
	}

	if len(due) > 0 {
		log.Info("scan cycle started", "due_profiles", len(due), "total_profiles", len(a.profiles))
	}

	cycleHosts := 0
	cycleHealthy := true
	for _, p := range due {
		metrics.ScansTotal.Inc()
		n, err := a.scanner.Scan(ctx, p.Subnet, scanner.SubnetOptions{
			Timeout:        p.Timeout,
			ProbePorts:     p.ProbePorts,
			DeepProbe:      boolPtr(p.DeepProbe),
			DeepProbePorts: p.DeepProbePorts,
			UDPPorts:       p.UDPPorts,
			EnrichARP:      boolPtr(p.EnrichARP),
		})
		if err != nil {
			metrics.ScanErrorsTotal.Inc()
			log.Warn("subnet scan failed", "subnet", p.Subnet, "err", err)
			cycleHealthy = false
			continue
		}
		log.Debug("subnet scanned", "subnet", p.Subnet, "hosts", n, "interval", p.ScanInterval)
		cycleHosts += n
		// Schedule the next due time. Computed off the start of THIS
		// cycle (not now) so a slow scan doesn't drift the cadence.
		a.nextDue[p.Subnet] = started.Add(p.ScanInterval)
	}

	if pruned := a.pruneStale(ctx, log, started); pruned > 0 {
		metrics.HostsPrunedTotal.Add(int64(pruned))
		log.Info("pruned stale hosts", "count", pruned)
	}

	// Diff and fire events. Only meaningful when the cycle didn't
	// itself fail mid-way — declaring hosts "vanished" because of a
	// transient DB error would be alert spam.
	if cycleHealthy {
		a.emitChangeEvents(ctx, log, prevHosts, started)
	}

	// Use the actual DB count so the tracker reflects total accumulated
	// inventory, not just hosts found in this cycle.
	total, err := a.hosts.Count(ctx)
	if err != nil {
		metrics.DBErrorsTotal.Inc()
		log.Warn("failed to count total hosts", "err", err)
		total = cycleHosts
		cycleHealthy = false
	}
	metrics.HostCount.Set(int64(total))
	a.tracker.SetHostCount(total)
	a.tracker.RecordScan()
	a.tracker.SetHealthy(cycleHealthy)

	duration := a.now().Sub(started)
	// Warning threshold: half the tick interval. Slower than that and
	// the loop is at risk of dropped firings.
	interval := a.tickInterval()
	if interval > 0 && duration > interval/2 {
		log.Warn("scan cycle nearly exceeded tick interval",
			"duration", duration.Round(time.Millisecond),
			"tick_interval", interval,
		)
	}
	log.Info("scan cycle complete",
		"cycle_hosts", cycleHosts,
		"total_hosts", total,
		"duration", duration.Round(time.Millisecond),
		"healthy", cycleHealthy,
	)
}

// boolPtr is a small inline helper for passing a value bool to a *bool
// option field. Pointer-bools let a profile distinguish "explicit false"
// from "inherit default"; from the resolved-profile side we know which
// way the bool was already resolved, so we always pass a non-nil pointer.
func boolPtr(b bool) *bool { return &b }

// pruneStale deletes hosts whose last_seen is older than the configured
// HostTTL. Returns the number of hosts pruned. Disabled when HostTTL is 0
// (the default), so existing deployments don't lose history silently.
func (a *Agent) pruneStale(ctx context.Context, log *slog.Logger, now time.Time) int {
	ttl := a.cfg.HostTTL.Duration
	if ttl <= 0 {
		return 0
	}
	cutoff := now.Add(-ttl)
	hosts, err := a.hosts.List(ctx)
	if err != nil {
		log.Warn("prune: list hosts failed", "err", err)
		return 0
	}
	pruned := 0
	for _, h := range hosts {
		if h.LastSeen.Before(cutoff) {
			if err := a.hosts.Delete(ctx, h.ID); err != nil {
				log.Warn("prune: delete host failed", "id", h.ID, "ip", h.IPAddress, "err", err)
				continue
			}
			pruned++
		}
	}
	return pruned
}

// snapshotByIP lists the current host inventory keyed by IP. Used pre-
// cycle so the change-detection diff has a stable view to compare
// against. A List failure logs and returns nil — the diff will then
// produce no events (better than misleading ones based on a partial set).
func snapshotByIP(ctx context.Context, hs store.HostStore, log *slog.Logger) map[string]*models.Host {
	hosts, err := hs.List(ctx)
	if err != nil {
		log.Warn("change-detect: pre-cycle snapshot failed", "err", err)
		return nil
	}
	out := make(map[string]*models.Host, len(hosts))
	for _, h := range hosts {
		out[h.IPAddress] = h
	}
	return out
}

// emitChangeEvents compares the post-cycle host set with the pre-cycle
// snapshot and fires one alert event per change. Discovered events use
// the post-cycle enrichment; vanished events use the pre-cycle row
// (since the post-cycle one is gone).
func (a *Agent) emitChangeEvents(ctx context.Context, log *slog.Logger, prev map[string]*models.Host, cycleStart time.Time) {
	curr := snapshotByIP(ctx, a.hosts, log)
	if curr == nil {
		return
	}
	for ip, h := range curr {
		if _, was := prev[ip]; !was {
			a.alerts.Emit(ctx, alerts.Event{
				Type:       alerts.HostDiscovered,
				IP:         ip,
				Hostname:   h.Hostname,
				MACAddress: h.MACAddress,
				Vendor:     h.Vendor,
				DeviceType: h.DeviceType,
				Time:       cycleStart,
				Agent:      a.name,
			})
		}
	}
	for ip, h := range prev {
		if _, still := curr[ip]; !still {
			a.alerts.Emit(ctx, alerts.Event{
				Type:       alerts.HostVanished,
				IP:         ip,
				Hostname:   h.Hostname,
				MACAddress: h.MACAddress,
				Vendor:     h.Vendor,
				DeviceType: h.DeviceType,
				Time:       cycleStart,
				Agent:      a.name,
			})
		}
	}
}
