// Package agent implements the core scan loop that drives an inventory agent
// instance. It ties together the network scanner, the persistence layer, and
// the health tracker so that the watchdog always has fresh data to compare.
package agent

import (
	"context"
	"log/slog"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/scanner"
	"github.com/Ronin48/NetworkInventoryAgent/internal/store"
)

// Agent runs a periodic scan loop and publishes results to a health.Tracker.
type Agent struct {
	name    string
	cfg     config.ScannerConfig
	hosts   store.HostStore
	scanner *scanner.Scanner
	tracker *health.Tracker
	now     func() time.Time

	// trigger is a buffered channel that lets external callers
	// (e.g. POST /scan) force an immediate cycle without waiting for the
	// next ticker firing. Capacity 1 so concurrent triggers coalesce.
	trigger chan struct{}
}

// New creates an Agent. The caller is responsible for starting the health
// server and watchdog; this constructor only wires the scan loop.
func New(
	name string,
	cfg config.ScannerConfig,
	hosts store.HostStore,
	ports store.PortStore,
	scans store.ScanStore,
	tracker *health.Tracker,
) *Agent {
	return &Agent{
		name:  name,
		cfg:   cfg,
		hosts: hosts,
		scanner: scanner.New(
			hosts, ports, scans,
			cfg.Timeout.Duration, cfg.Workers, cfg.MaxHosts,
			cfg.ProbePorts,
		),
		tracker: tracker,
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
	cycleHosts := 0
	cycleHealthy := true
	for _, subnet := range a.cfg.Subnets {
		n, err := a.scanner.Scan(ctx, subnet)
		if err != nil {
			log.Warn("subnet scan failed", "subnet", subnet, "err", err)
			cycleHealthy = false
			continue
		}
		log.Debug("subnet scanned", "subnet", subnet, "hosts", n)
		cycleHosts += n
	}

	if pruned := a.pruneStale(ctx, log, started); pruned > 0 {
		log.Info("pruned stale hosts", "count", pruned)
	}

	// Use the actual DB count so the tracker reflects total accumulated
	// inventory, not just hosts found in this cycle.
	total, err := a.hosts.Count(ctx)
	if err != nil {
		log.Warn("failed to count total hosts", "err", err)
		total = cycleHosts
		cycleHealthy = false
	}
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
