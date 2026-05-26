// Package runtime is the shared bootstrap for the three agent binaries
// (cmd/agent, cmd/wintermute, cmd/neuromancer). Each binary's main.go calls
// Run with its own name, default config path, and a flag that decides
// whether to start a watchdog peer probe.
//
// The three binaries used to be ~95% duplicated; collapsing the boilerplate
// here means new wiring (e.g. metrics endpoint, additional middleware) only
// has to be written once.
package runtime

import (
	"context"
	"flag"
	"log/slog"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/admin"
	"github.com/Ronin48/NetworkInventoryAgent/internal/agent"
	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/logging"
	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite"
	"github.com/Ronin48/NetworkInventoryAgent/internal/watchdog"
)

// Options controls how Run sets up the binary.
type Options struct {
	// Name is the agent's identifying string. Used in logs, the admin nav,
	// and the health Status payload.
	Name string
	// DefaultConfigPath is the value for the -config flag when the operator
	// doesn't pass one.
	DefaultConfigPath string
	// WithWatchdog decides whether to start a watchdog peer probe. Set to
	// true for the paired Wintermute/Neuromancer binaries; false for the
	// standalone agent.
	WithWatchdog bool
}

// Run is the binary entry point. It parses the -config flag, loads the
// config, starts the health server, admin console, optional watchdog, and
// scan loop, then blocks until SIGINT/SIGTERM. Returns an exit code.
func Run(opts Options) int {
	configPath := flag.String("config", opts.DefaultConfigPath, "path to JSON config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		return 1
	}
	logging.Setup(cfg.Log, opts.Name)

	db, err := sqlite.Open(context.Background(), cfg.Database.Path)
	if err != nil {
		slog.Error("failed to open database", "path", cfg.Database.Path, "err", err)
		return 1
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("close database", "err", err)
		}
	}()

	tracker := health.NewTracker(opts.Name)

	healthSrv := health.NewServer(
		cfg.Health.Addr, tracker,
		3*cfg.Scanner.ScanInterval.Duration,
		cfg.Health.AuthToken,
	)
	if err := healthSrv.Start(); err != nil {
		slog.Error("failed to start health server", "addr", cfg.Health.Addr, "err", err)
		return 1
	}
	slog.Info("health server started", "addr", healthSrv.Addr())

	a := agent.New(opts.Name, cfg.Scanner, db.Hosts(), db.Ports(), db.Scans(), tracker)

	adminSrv, err := admin.NewServer(
		cfg.Admin.Addr, opts.Name,
		db.Hosts(), db.Ports(), db.Scans(),
		tracker.Get, a.Trigger,
	)
	if err != nil {
		slog.Error("failed to create admin server", "err", err)
		return 1
	}
	if err := adminSrv.Start(); err != nil {
		slog.Error("failed to start admin server", "addr", cfg.Admin.Addr, "err", err)
		return 1
	}
	slog.Info("admin console started", "addr", adminSrv.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if opts.WithWatchdog && cfg.Watchdog.PeerAddr != "" {
		wd := watchdog.New(watchdog.Config{
			Name:            opts.Name,
			PeerAddr:        cfg.Watchdog.PeerAddr,
			PeerToken:       cfg.Watchdog.PeerToken,
			Interval:        cfg.Watchdog.Interval.Duration,
			ScanInterval:    cfg.Scanner.ScanInterval.Duration,
			MaxHostDriftPct: cfg.Watchdog.MaxHostDriftPct,
			MaxFailures:     cfg.Watchdog.MaxFailures,
		}, tracker.Get, tracker.SetPeer)
		go wd.Run(ctx)
	}

	a.Run(ctx) // blocks until ctx cancelled

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("health server shutdown error", "err", err)
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("admin server shutdown error", "err", err)
	}
	slog.Info("agent stopped", "name", opts.Name)
	return 0
}
