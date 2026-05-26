// Package logging configures the global slog logger from agent config.
package logging

import (
	"log/slog"
	"os"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
)

// Setup initialises the global slog logger according to cfg. When name is
// non-empty every record carries an "agent" field so a combined stdout from
// a paired deployment is unambiguous even before the scan loop attaches its
// own logger.
func Setup(cfg config.LogConfig, name string) {
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
	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	logger := slog.New(handler)
	if name != "" {
		logger = logger.With("agent", name)
	}
	slog.SetDefault(logger)
}
