// Agent is a standalone network inventory agent that scans configured subnets
// and persists discovered hosts to SQLite. Unlike the paired Wintermute/
// Neuromancer agents, this binary does not require a watchdog peer.
//
// Usage:
//
//	agent [-config config.json]
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/admin"
	"github.com/Ronin48/NetworkInventoryAgent/internal/agent"
	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/logging"
	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite"
)

func main() {
	configPath := flag.String("config", "config.json", "path to JSON config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}
	logging.Setup(cfg.Log)

	db, err := sqlite.Open(context.Background(), cfg.Database.Path)
	if err != nil {
		slog.Error("failed to open database", "path", cfg.Database.Path, "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := db.Close(); err != nil {
			slog.Error("close database", "err", err)
		}
	}()

	tracker := health.NewTracker("agent")

	srv := health.NewServer(cfg.Health.Addr, tracker)
	if err := srv.Start(); err != nil {
		slog.Error("failed to start health server", "addr", cfg.Health.Addr, "err", err)
		os.Exit(1)
	}
	slog.Info("health server started", "addr", srv.Addr())

	adminSrv, err := admin.NewServer(cfg.Admin.Addr, "agent", db.Hosts(), db.Ports(), db.Scans(), tracker.Get)
	if err != nil {
		slog.Error("failed to create admin server", "err", err)
		os.Exit(1)
	}
	if err := adminSrv.Start(); err != nil {
		slog.Error("failed to start admin server", "addr", cfg.Admin.Addr, "err", err)
		os.Exit(1)
	}
	slog.Info("admin console started", "addr", adminSrv.Addr())

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := agent.New("agent", cfg.Scanner, db.Hosts(), db.Ports(), db.Scans(), tracker)
	a.Run(ctx) // blocks until ctx cancelled

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("health server shutdown error", "err", err)
	}
	if err := adminSrv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("admin server shutdown error", "err", err)
	}
	slog.Info("agent stopped")
}
