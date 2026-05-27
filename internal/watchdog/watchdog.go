// Package watchdog implements the mutual sanity-check loop between two agent
// instances. Each agent runs one Watchdog pointed at the other agent's health
// server. The watchdog performs three checks on every tick:
//
//  1. Liveness   — is the peer responding at all?
//  2. Freshness  — has the peer completed a scan recently?
//  3. Consistency — does the peer's host count agree with ours (within drift threshold)?
//
// Failures are logged as warnings until MaxFailures consecutive checks fail, at
// which point an error is logged and the peer is declared DOWN. The watchdog
// never kills or restarts the peer process; that responsibility belongs to an
// external supervisor (systemd, Docker, Kubernetes).
package watchdog

import (
	"context"
	"log/slog"
	"math"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/metrics"
)

// Config holds the parameters for a Watchdog instance.
type Config struct {
	// Name is the name of THIS agent (used in log fields).
	Name string
	// PeerAddr is the base URL of the peer's health server.
	PeerAddr string
	// PeerToken is the bearer token sent on every probe. Empty when the
	// peer is on loopback and does not require auth.
	PeerToken string
	// Interval is how often to run the sanity checks.
	Interval time.Duration
	// ScanInterval is the expected gap between the peer's scans.
	// A peer whose last scan is older than 2×ScanInterval is considered stale.
	ScanInterval time.Duration
	// MaxHostDriftPct is the maximum tolerable percentage difference between
	// local and peer host counts before a consistency warning is raised.
	MaxHostDriftPct float64
	// MaxFailures is the number of consecutive liveness failures before the
	// peer is declared DOWN.
	MaxFailures int
}

// Watchdog continuously sanity-checks a peer agent.
type Watchdog struct {
	cfg         Config
	client      *health.Client
	localStatus func() health.Status
	publish     func(health.PeerStatus)
	failures    int
}

// New creates a Watchdog. localStatus is a callback that returns this agent's
// current Status so the watchdog can compare inventories. publish, if non-nil,
// is called after every tick with the watchdog's latest view of the peer —
// the standard wiring is tracker.SetPeer.
func New(cfg Config, localStatus func() health.Status, publish func(health.PeerStatus)) *Watchdog {
	if publish == nil {
		publish = func(health.PeerStatus) {}
	}
	return &Watchdog{
		cfg:         cfg,
		client:      health.NewAuthedClient(cfg.PeerAddr, cfg.PeerToken),
		localStatus: localStatus,
		publish:     publish,
	}
}

// Run starts the watchdog loop and blocks until ctx is cancelled.
func (w *Watchdog) Run(ctx context.Context) {
	log := slog.With("agent", w.cfg.Name, "peer", w.cfg.PeerAddr)
	log.Info("watchdog started")

	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("watchdog stopped")
			return
		case <-ticker.C:
			w.check(ctx, log)
		}
	}
}

func (w *Watchdog) check(ctx context.Context, log *slog.Logger) {
	metrics.WatchdogChecksTotal.Inc()
	ps := health.PeerStatus{
		Addr:          w.cfg.PeerAddr,
		LastCheckedAt: time.Now().UTC(),
	}
	defer func() {
		ps.ConsecutiveFailures = w.failures
		if ps.Reachable {
			metrics.PeerUp.Set(1)
		} else {
			metrics.PeerUp.Set(0)
		}
		w.publish(ps)
	}()

	// 1. Liveness
	if err := w.client.Ping(ctx); err != nil {
		w.failures++
		metrics.WatchdogFailuresTotal.Inc()
		ps.LastError = err.Error()
		if w.failures == w.cfg.MaxFailures {
			metrics.WatchdogPeerDownTotal.Inc()
		}
		if w.failures >= w.cfg.MaxFailures {
			log.Error("peer is DOWN",
				"consecutive_failures", w.failures,
				"err", err,
			)
		} else {
			log.Warn("peer health check failed",
				"failure", w.failures,
				"max", w.cfg.MaxFailures,
				"err", err,
			)
		}
		return
	}
	w.failures = 0
	ps.Reachable = true

	// 2 & 3. Fetch full status for freshness and consistency checks.
	peer, err := w.client.FetchStatus(ctx)
	if err != nil {
		log.Warn("could not fetch peer status", "err", err)
		ps.LastError = err.Error()
		return
	}
	ps.PeerHostCount = peer.HostCount
	ps.PeerScanCount = peer.ScanCount

	ps.Stale = w.checkFreshness(peer, log)
	ps.DriftPct = w.checkConsistency(peer, log)
}

// checkFreshness warns if the peer hasn't completed a scan recently and
// returns true when it considers the peer stale.
func (w *Watchdog) checkFreshness(peer health.Status, log *slog.Logger) bool {
	if peer.LastScanAt == nil {
		log.Warn("peer has never completed a scan")
		return true
	}
	age := time.Since(*peer.LastScanAt)
	staleThreshold := 2 * w.cfg.ScanInterval
	if age > staleThreshold {
		log.Warn("peer scan is stale",
			"last_scan_age", age.Round(time.Second),
			"threshold", staleThreshold,
		)
		return true
	}
	return false
}

// checkConsistency warns if the peer's host count diverges too far from ours
// and returns the computed drift percentage.
func (w *Watchdog) checkConsistency(peer health.Status, log *slog.Logger) float64 {
	local := w.localStatus()

	// Skip the check if either side has not yet completed a scan.
	if local.ScanCount == 0 || peer.ScanCount == 0 {
		return 0
	}

	mine := float64(local.HostCount)
	theirs := float64(peer.HostCount)

	// Avoid division by zero when both counts are 0.
	if mine == 0 && theirs == 0 {
		return 0
	}

	larger := math.Max(mine, theirs)
	driftPct := math.Abs(mine-theirs) / larger * 100

	if driftPct > w.cfg.MaxHostDriftPct {
		log.Warn("inventory consistency check failed — host counts diverge",
			"local_hosts", local.HostCount,
			"peer_hosts", peer.HostCount,
			"drift_pct", math.Round(driftPct*10)/10,
			"threshold_pct", w.cfg.MaxHostDriftPct,
		)
	} else {
		log.Debug("peer inventory consistent",
			"local_hosts", local.HostCount,
			"peer_hosts", peer.HostCount,
			"drift_pct", math.Round(driftPct*10)/10,
		)
	}
	return math.Round(driftPct*10) / 10
}
