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
) *Agent {
	if alertEmitter == nil {
		alertEmitter = alerts.NoopEmitter()
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
		tracker: tracker,
		alerts:  alertEmitter,
		now:     time.Now,
		trigger: make(chan struct{}, 1),
	}
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

// Run starts the scan loop. It executes one scan immediately, then repeats on
// cfg.ScanInterval. It blocks until ctx is cancelled.
func (a *Agent) Run(ctx context.Context) {
	log := slog.With("agent", a.name)
	log.Info("scan loop started", "subnets", a.cfg.Subnets, "interval", a.cfg.ScanInterval)

	a.runCycle(ctx, log)

	ticker := time.NewTicker(a.cfg.ScanInterval.Duration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("scan loop stopped")
			return
		case <-ticker.C:
			a.runCycle(ctx, log)
		case <-a.trigger:
			log.Info("on-demand scan triggered")
			a.runCycle(ctx, log)
		}
	}
}

func (a *Agent) runCycle(ctx context.Context, log *slog.Logger) {
	log.Info("scan cycle started", "subnets", len(a.cfg.Subnets))
	started := a.now()

	// Snapshot the pre-cycle host inventory so we can diff it against
	// the post-cycle list and fire HostDiscovered / HostVanished events.
	// Snapshotting before the scan (rather than tracking what the
	// scanner returned) means the diff correctly reflects "ground truth
	// changed", including hosts the operator added or removed
	// externally.
	prevHosts := snapshotByIP(ctx, a.hosts, log)

	cycleHosts := 0
	cycleHealthy := true
	for _, subnet := range a.cfg.Subnets {
		metrics.ScansTotal.Inc()
		n, err := a.scanner.Scan(ctx, subnet)
		if err != nil {
			metrics.ScanErrorsTotal.Inc()
			log.Warn("subnet scan failed", "subnet", subnet, "err", err)
			cycleHealthy = false
			continue
		}
		log.Debug("subnet scanned", "subnet", subnet, "hosts", n)
		cycleHosts += n
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
	interval := a.cfg.ScanInterval.Duration
	if interval > 0 && duration > interval/2 {
		log.Warn("scan cycle nearly exceeded interval",
			"duration", duration.Round(time.Millisecond),
			"interval", interval,
		)
	}
	log.Info("scan cycle complete",
		"cycle_hosts", cycleHosts,
		"total_hosts", total,
		"duration", duration.Round(time.Millisecond),
		"healthy", cycleHealthy,
	)
}

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
