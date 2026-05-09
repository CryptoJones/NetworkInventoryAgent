package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
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

	logger := buildLogger(cfg.Log)
	slog.SetDefault(logger)

	db, err := sqlite.Open(cfg.Database.Path)
	if err != nil {
		slog.Error("failed to open database", "path", cfg.Database.Path, "err", err)
		os.Exit(1)
	}
	defer db.Close()

	slog.Info("agent started", "db", cfg.Database.Path)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// TODO: start scanner loop, passing db.Hosts(), db.Ports(), db.Scans()
	<-ctx.Done()
	slog.Info("agent stopped")
}

func buildLogger(cfg config.LogConfig) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}
	if cfg.Format == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
