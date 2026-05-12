// Package logging configures the global slog logger from agent config
// and provides file-based logging support.
//
// Levels:
//
//	none     — all logging suppressed (slog.Level 12, above all built-in)
//	standard — info and above (agent lifecycle, scan start/end, errors)
//	verbose  — info + warning + health/ping/subnet details
//	debug    — everything (per-host probe results, raw timings)
//
// Formats: text (human-readable) or json (machine-parseable).
// Output: stdout by default; a file path can be set via config's "file" field.
package logging

import (
	"io"
	"log/slog"
	"os"
	"sync"

	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
)

// LevelName maps a human-friendly level string to the slog level.
var LevelName = map[string]slog.Level{
	"none":     slog.LevelError + 1, // 12, above all built-in levels
	"standard": slog.LevelInfo,
	"verbose":  slog.LevelInfo - 2, // between Info and Debug
	"debug":    slog.LevelDebug,
}

// Verbose is a custom log level between Info and Debug.
var Verbose = slog.LevelInfo - 2

// output holds the current write target and its mutex so SetOutputFile can
// swap between stdout and a file without races.
var (
	outputMu sync.Mutex
	output   io.WriteCloser = os.Stdout
)

// Setup initialises the global slog logger according to the application config.
// Valid levels: none, standard, verbose, debug.
// Valid formats: text, json.
// If cfg.File is set, log output goes to that file.
func Setup(cfg config.LogConfig) {
	level, ok := LevelName[cfg.Level]
	if !ok {
		level = slog.LevelInfo // default to standard
	}

	opts := &slog.HandlerOptions{Level: level}
	wr := &writer{&output}
	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(wr, opts)
	} else {
		handler = slog.NewTextHandler(wr, opts)
	}
	slog.SetDefault(slog.New(handler))

	// Open log file if specified.
	if cfg.File != "" {
		_ = SetOutputFile(cfg.File)
	}
}

// SetOutputFile opens (or reopens) file as the log target.
// Passing "" reverts to stdout. Call this after Setup() to
// hot-swap the log output path.
func SetOutputFile(path string) error {
	outputMu.Lock()
	defer outputMu.Unlock()
	if output != os.Stdout {
		output.Close()
	}
	if path == "" {
		output = os.Stdout
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		output = os.Stdout
		return err
	}
	output = f
	return nil
}

// CloseOutput closes the current log file if one is set. No-op for stdout.
func CloseOutput() {
	outputMu.Lock()
	defer outputMu.Unlock()
	if output != os.Stdout {
		output.Close()
		output = os.Stdout
	}
}

// writer implements io.Writer by delegating to the swap-able output.
type writer struct {
	target *io.WriteCloser
}

func (w *writer) Write(p []byte) (int, error) {
	outputMu.Lock()
	out := *w.target
	outputMu.Unlock()
	return out.Write(p)
}

// Logv logs at the Verbose level when the logger is at or below verbose.
func Logv(msg string, args ...any) {
	if slog.Default().Enabled(nil, Verbose) {
		slog.Info(msg, args...)
	}
}
