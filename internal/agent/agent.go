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
	scanner *scanner.Scanner
	tracker *health.Tracker
}

// New creates an Agent. The caller is responsible for starting the health
// server and watchdog; this constructor only wires the scan loop.
func New(
	name string,
	cfg config.ScannerConfig,
	hosts store.HostStore,
	scans store.ScanStore,
	tracker *health.Tracker,
) *Agent {
	return &Agent{
		name:    name,
		cfg:     cfg,
		scanner: scanner.New(hosts, scans, cfg.Timeout.Duration),
		tracker: tracker,
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
		}
	}
}

func (a *Agent) runCycle(ctx context.Context, log *slog.Logger) {
	log.Info("scan cycle started", "subnets", len(a.cfg.Subnets))
	total := 0
	for _, subnet := range a.cfg.Subnets {
		n, err := a.scanner.Scan(ctx, subnet)
		if err != nil {
			log.Warn("subnet scan failed", "subnet", subnet, "err", err)
			continue
		}
		log.Debug("subnet scanned", "subnet", subnet, "hosts", n)
		total += n
	}
	a.tracker.SetHostCount(total)
	a.tracker.RecordScan()
	log.Info("scan cycle complete", "total_hosts", total)
}
