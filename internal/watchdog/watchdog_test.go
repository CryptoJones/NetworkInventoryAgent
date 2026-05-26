package watchdog_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/watchdog"
	"github.com/stretchr/testify/require"
)

func startPeer(t *testing.T, tracker *health.Tracker) string {
	t.Helper()
	srv := health.NewServer(":0", tracker, 0)
	require.NoError(t, srv.Start())
	t.Cleanup(func() { srv.Shutdown(context.Background()) }) //nolint:errcheck

	_, port, err := net.SplitHostPort(srv.Addr())
	require.NoError(t, err)
	return "http://127.0.0.1:" + port
}

func localStatus(tr *health.Tracker) func() health.Status {
	return tr.Get
}

// newWatchdog creates a watchdog that ticks every 30 ms — fast enough for
// tests without hammering the scheduler.
func newWatchdog(peerURL string, local *health.Tracker, scanInterval time.Duration, driftPct float64) *watchdog.Watchdog {
	return watchdog.New(watchdog.Config{
		Name:            "test-agent",
		PeerAddr:        peerURL,
		Interval:        30 * time.Millisecond,
		ScanInterval:    scanInterval,
		MaxHostDriftPct: driftPct,
		MaxFailures:     2,
	}, localStatus(local))
}

func runFor(t *testing.T, wd *watchdog.Watchdog, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	wd.Run(ctx) // blocks until ctx expires
}

// TestWatchdog_HealthyPeer verifies that the watchdog completes without
// panicking when the peer is healthy and consistent.
func TestWatchdog_HealthyPeer(t *testing.T) {
	peer := health.NewTracker("peer")
	peer.SetHostCount(10)
	peer.RecordScan()
	peerURL := startPeer(t, peer)

	local := health.NewTracker("local")
	local.SetHostCount(10)
	local.RecordScan()

	wd := newWatchdog(peerURL, local, 5*time.Minute, 20.0)
	runFor(t, wd, 120*time.Millisecond)
}

// TestWatchdog_PeerDown verifies that the watchdog handles a non-existent peer
// gracefully (no panic, no hang).
func TestWatchdog_PeerDown(t *testing.T) {
	// Bind and immediately close a listener to get a guaranteed free port.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	ln.Close()

	local := health.NewTracker("local")
	wd := watchdog.New(watchdog.Config{
		Name:            "local",
		PeerAddr:        "http://" + addr,
		Interval:        30 * time.Millisecond,
		ScanInterval:    5 * time.Minute,
		MaxHostDriftPct: 50.0,
		MaxFailures:     2,
	}, local.Get)
	runFor(t, wd, 150*time.Millisecond)
}

// TestWatchdog_StalePeer verifies that the watchdog fires the freshness check
// when ScanInterval is very short (so any completed scan is immediately stale).
func TestWatchdog_StalePeer(t *testing.T) {
	peer := health.NewTracker("peer")
	peer.RecordScan()
	peerURL := startPeer(t, peer)

	local := health.NewTracker("local")
	local.RecordScan()

	// ScanInterval of 1 ns means 2×ScanInterval = 2 ns — any scan is stale.
	wd := newWatchdog(peerURL, local, time.Nanosecond, 50.0)
	runFor(t, wd, 120*time.Millisecond)
}

// TestWatchdog_HighDrift verifies that the watchdog fires the consistency check
// when host counts diverge beyond the threshold.
func TestWatchdog_HighDrift(t *testing.T) {
	peer := health.NewTracker("peer")
	peer.SetHostCount(200)
	peer.RecordScan()
	peerURL := startPeer(t, peer)

	local := health.NewTracker("local")
	local.SetHostCount(1) // extreme divergence
	local.RecordScan()

	wd := newWatchdog(peerURL, local, 5*time.Minute, 10.0)
	runFor(t, wd, 120*time.Millisecond)
}

// TestWatchdog_UnhealthyPeerReturns503 verifies that a peer returning 503
// is treated as a liveness failure.
func TestWatchdog_UnhealthyPeerReturns503(t *testing.T) {
	peer := health.NewTracker("peer")
	peer.SetHealthy(false)
	peerURL := startPeer(t, peer)

	local := health.NewTracker("local")
	wd := newWatchdog(peerURL, local, 5*time.Minute, 50.0)
	runFor(t, wd, 150*time.Millisecond)
}
