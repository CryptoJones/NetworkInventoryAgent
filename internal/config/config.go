// Package config loads application configuration from a JSON file with
// environment variable overrides. Environment variables always win so the
// agent can be configured in Docker/k8s without touching the config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Duration wraps time.Duration so it can be unmarshalled from a JSON string
// (e.g. "5m", "30s") in addition to a raw nanosecond integer.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		dur, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", s, err)
		}
		d.Duration = dur
		return nil
	}
	var n int64
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("duration must be a string (e.g. \"5m\") or nanosecond integer")
	}
	d.Duration = time.Duration(n)
	return nil
}

// Config is the top-level configuration object.
type Config struct {
	Database DatabaseConfig  `json:"database"`
	Scanner  ScannerConfig   `json:"scanner"`
	Log      LogConfig       `json:"log"`
	Health   HealthConfig    `json:"health"`
	Watchdog WatchdogConfig  `json:"watchdog"`
}

type DatabaseConfig struct {
	// Path is the file system path to the SQLite database file.
	// Use ":memory:" for an in-memory database (tests only).
	Path string `json:"path"`
}

type ScannerConfig struct {
	Subnets      []string `json:"subnets"`
	ScanInterval Duration `json:"scan_interval"`
	Timeout      Duration `json:"timeout"`
}

type LogConfig struct {
	Level  string `json:"level"`  // debug | info | warn | error
	Format string `json:"format"` // text | json
}

type HealthConfig struct {
	// Addr is the address the health HTTP server listens on (e.g. ":8080").
	Addr string `json:"addr"`
}

type WatchdogConfig struct {
	// PeerAddr is the base URL of the peer agent's health server
	// (e.g. "http://localhost:8081").
	PeerAddr string   `json:"peer_addr"`
	Interval Duration `json:"interval"`
	// MaxHostDriftPct is the maximum acceptable percentage difference between
	// this agent's host count and the peer's before a warning is raised.
	MaxHostDriftPct float64 `json:"max_host_drift_pct"`
	// MaxFailures is the number of consecutive failed health checks before the
	// peer is declared down.
	MaxFailures int `json:"max_failures"`
}

// Default returns a Config populated with safe defaults.
func Default() *Config {
	return &Config{
		Database: DatabaseConfig{
			Path: "inventory.db",
		},
		Scanner: ScannerConfig{
			ScanInterval: Duration{5 * time.Minute},
			Timeout:      Duration{30 * time.Second},
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Health: HealthConfig{
			Addr: ":8080",
		},
		Watchdog: WatchdogConfig{
			Interval:        Duration{30 * time.Second},
			MaxHostDriftPct: 50.0,
			MaxFailures:     3,
		},
	}
}

// Load reads the JSON config file at path and merges it over the defaults.
// If the file does not exist the defaults are returned without error.
// Environment variable overrides are applied last.
func Load(path string) (*Config, error) {
	cfg := Default()

	f, err := os.Open(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	if err == nil {
		defer f.Close()
		if err := json.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("decode config %q: %w", path, err)
		}
	}

	applyEnv(cfg)
	return cfg, nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("INVENTORY_DB_PATH"); v != "" {
		cfg.Database.Path = v
	}
	if v := os.Getenv("INVENTORY_LOG_LEVEL"); v != "" {
		cfg.Log.Level = v
	}
	if v := os.Getenv("INVENTORY_LOG_FORMAT"); v != "" {
		cfg.Log.Format = v
	}
}
