// Package config loads application configuration from a JSON file with
// environment variable overrides. Environment variables always win so the
// agent can be configured in Docker/k8s without touching the config file.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
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

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Duration.String())
}

// Config is the top-level configuration object.
type Config struct {
	Database DatabaseConfig `json:"database"`
	Scanner  ScannerConfig  `json:"scanner"`
	Log      LogConfig      `json:"log"`
	Health   HealthConfig   `json:"health"`
	Admin    AdminConfig    `json:"admin"`
	Watchdog WatchdogConfig `json:"watchdog"`
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
	// Workers is the global cap on concurrent probe goroutines across all
	// subnets in a cycle. Set this for the desired total parallelism, not
	// per-subnet — an operator with 20 subnets used to get 20×Workers
	// in-flight dials, which dwarfed the documented setting.
	Workers int `json:"workers"`
	// MaxHosts is the maximum number of usable addresses allowed in a single
	// subnet before the scan is rejected, preventing accidental /8 scans.
	MaxHosts int `json:"max_hosts"`
	// ProbePorts is the list of TCP ports the scanner dials to confirm a
	// host is live. Leaving it empty falls back to the historical default
	// of [22, 80, 443, 8080].
	ProbePorts []int `json:"probe_ports,omitempty"`
	// HostTTL marks hosts not seen within this many scan intervals as
	// stale. Zero disables pruning. Pruning runs at the end of each scan
	// cycle and DELETEs rows where last_seen < now - HostTTL*ScanInterval.
	HostTTL Duration `json:"host_ttl,omitempty"`
}

type LogConfig struct {
	Level  string `json:"level"`  // debug | info | warn | error
	Format string `json:"format"` // text | json
}

type HealthConfig struct {
	// Addr is the address the health HTTP server listens on.
	// Default is 127.0.0.1:8080 (loopback only). When bound off-loopback the
	// agent refuses to start unless AuthToken is also set, because the
	// /status endpoint leaks inventory size (OWASP A01/A05).
	Addr string `json:"addr"`
	// AuthToken is the shared bearer token required on /health and /status
	// when Addr is not a loopback bind. The same value goes into the peer's
	// watchdog.peer_token. Leave empty (and the file chmod 600) for the
	// loopback-only default deployment.
	AuthToken string `json:"auth_token,omitempty"`
}

type AdminConfig struct {
	// Addr is the address the admin web console listens on.
	// Default is 127.0.0.1:9090 (loopback only). Bind to 0.0.0.0 only in
	// trusted network environments, as the console is unauthenticated (OWASP A01/A05).
	Addr string `json:"addr"`
}

type WatchdogConfig struct {
	// PeerAddr is the base URL of the peer agent's health server
	// (e.g. "http://localhost:8081").
	PeerAddr string `json:"peer_addr"`
	// PeerToken is the bearer token sent on every probe of PeerAddr. Must
	// match the peer's health.auth_token. Empty when peer is on loopback.
	PeerToken string   `json:"peer_token,omitempty"`
	Interval  Duration `json:"interval"`
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
			ScanInterval: Duration{Duration: 5 * time.Minute},
			Timeout:      Duration{Duration: 2 * time.Second},
			Workers:      50,
			MaxHosts:     65535,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Health: HealthConfig{
			// Loopback-only by default; operators must explicitly open this to
			// other interfaces. Binding 0.0.0.0 would expose unauthenticated
			// endpoints network-wide (OWASP A01/A05).
			Addr: "127.0.0.1:8080",
		},
		Admin: AdminConfig{
			Addr: "127.0.0.1:9090",
		},
		Watchdog: WatchdogConfig{
			Interval:        Duration{Duration: 30 * time.Second},
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

	var fileMode os.FileMode
	var loaded bool

	f, err := os.Open(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	if err == nil {
		defer f.Close()
		if info, statErr := f.Stat(); statErr == nil {
			fileMode = info.Mode().Perm()
			loaded = true
		}
		if err := json.NewDecoder(f).Decode(cfg); err != nil {
			return nil, fmt.Errorf("decode config %q: %w", path, err)
		}
	}

	applyEnv(cfg)

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if loaded {
		if err := cfg.checkSecretsPerm(path, fileMode); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// checkSecretsPerm refuses to start when a config file containing a bearer
// token is readable by group or other. The SECURITY.md advice is chmod 600;
// catching this at startup beats discovering it after a token leak.
func (c *Config) checkSecretsPerm(path string, mode os.FileMode) error {
	hasSecret := c.Health.AuthToken != "" || c.Watchdog.PeerToken != ""
	if !hasSecret {
		return nil
	}
	if mode&0o077 == 0 {
		return nil
	}
	return fmt.Errorf("config %q has mode %o but contains a bearer token; chmod 600 it (or remove the token if running loopback-only)", path, mode)
}

// validate checks config values that cannot be enforced by JSON unmarshalling.
func (c *Config) validate() error {
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level %q is not valid; must be debug, info, warn, or error", c.Log.Level)
	}
	switch c.Log.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format %q is not valid; must be text or json", c.Log.Format)
	}
	if c.Watchdog.PeerAddr != "" {
		if err := validatePeerAddr(c.Watchdog.PeerAddr); err != nil {
			return fmt.Errorf("watchdog.peer_addr: %w", err)
		}
	}
	if !isLoopbackBind(c.Health.Addr) && c.Health.AuthToken == "" {
		return fmt.Errorf("health.addr %q is not loopback; set health.auth_token to gate /health and /status (the endpoints expose host counts; binding off-loopback without a token is OWASP A01/A05)", c.Health.Addr)
	}
	return nil
}

// isLoopbackBind reports whether addr listens only on a loopback interface.
// Empty/wildcard binds (":8080", "0.0.0.0:8080", "[::]:8080") are NOT
// loopback. Anything else is checked against net.ParseIP.IsLoopback so
// IPv6 ::1 is handled correctly.
func isLoopbackBind(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		// Hostname — accept "localhost" as loopback, treat anything else as not.
		return strings.EqualFold(host, "localhost")
	}
	return ip.IsLoopback()
}

// validatePeerAddr rejects peer_addr values whose scheme is not http or https,
// preventing accidental SSRF via file://, ftp://, or other URI schemes (OWASP A10).
func validatePeerAddr(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme %q not allowed; must be http or https", scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("missing host in %q", raw)
	}
	return nil
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
	// Tokens preferentially come from the environment so the JSON file can
	// stay world-readable when only loopback binds are used. Setting these
	// in env also avoids the secrets-in-git footgun.
	if v := os.Getenv("INVENTORY_AUTH_TOKEN"); v != "" {
		cfg.Health.AuthToken = v
	}
	if v := os.Getenv("INVENTORY_PEER_TOKEN"); v != "" {
		cfg.Watchdog.PeerToken = v
	}
}
