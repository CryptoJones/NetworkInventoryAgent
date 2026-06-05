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
	return json.Marshal(d.String())
}

// Config is the top-level configuration object.
type Config struct {
	Database DatabaseConfig `json:"database"`
	Scanner  ScannerConfig  `json:"scanner"`
	Log      LogConfig      `json:"log"`
	Health   HealthConfig   `json:"health"`
	Admin    AdminConfig    `json:"admin"`
	Watchdog WatchdogConfig `json:"watchdog"`
	Tracing  TracingConfig  `json:"tracing,omitempty"`
	Alerts   AlertsConfig   `json:"alerts,omitempty"`
}

// AlertsConfig configures change-detection event sinks. Either or both
// sub-sections may be set; absence of both silently disables alerting.
type AlertsConfig struct {
	Webhook WebhookConfig `json:"webhook,omitempty"`
	Syslog  SyslogConfig  `json:"syslog,omitempty"`
}

// WebhookConfig is the HTTP-POST-with-JSON sink.
type WebhookConfig struct {
	// URL is the receiver endpoint. Empty disables this sink.
	URL string `json:"url,omitempty"`
	// AuthHeader, if non-empty, is sent verbatim as the Authorization
	// header (e.g. "Bearer abc123" or "Basic dXNlcjpwYXNz").
	AuthHeader string `json:"auth_header,omitempty"`
}

// SyslogConfig is the RFC 5424 over UDP/TCP sink.
type SyslogConfig struct {
	// Addr is a URL like "udp://syslog.example.com:514" or "tcp://...".
	// Empty disables this sink.
	Addr string `json:"addr,omitempty"`
	// Tag is the APP-NAME field. Default "network-inventory".
	Tag string `json:"tag,omitempty"`
	// Facility is the RFC 5424 facility number 0..23. Default 16 (local0).
	Facility int `json:"facility,omitempty"`
}

// TracingConfig controls the OpenTelemetry exporter. When Endpoint is empty
// the agent still installs the tracer provider so spans are produced
// internally but they go to a no-op exporter — the cost of turning real
// export on later is therefore zero. The OTEL_EXPORTER_OTLP_ENDPOINT env
// var, when set, overrides this value (so containerised deployments don't
// need a config edit).
type TracingConfig struct {
	Endpoint string `json:"endpoint,omitempty"`
}

type DatabaseConfig struct {
	// Path is the file system path to the SQLite database file.
	// Use ":memory:" for an in-memory database (tests only).
	Path string `json:"path"`
}

type ScannerConfig struct {
	// Subnets is the legacy flat list — every entry uses the global
	// defaults below. Mutually exclusive with Profiles; setting both
	// is a config error.
	Subnets []string `json:"subnets,omitempty"`
	// Profiles is the per-subnet override list. Any field a profile
	// leaves empty falls back to the corresponding global default
	// (ScanInterval, Timeout, ProbePorts, …). Operators tune critical
	// infrastructure with an aggressive profile while leaving the
	// guest network on the lazy default.
	Profiles []SubnetProfile `json:"profiles,omitempty"`

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
	// ScanHistoryTTL bounds how long completed scan records are retained.
	// Zero (the default) keeps history forever. When set, the end of each
	// cycle DELETEs scan rows whose started_at is older than now -
	// ScanHistoryTTL, so the scans table and the /scans view stay bounded
	// on long-running deployments.
	ScanHistoryTTL Duration `json:"scan_history_ttl,omitempty"`
	// DeepProbe enables a second-pass scan of DeepProbePorts on every host
	// confirmed alive by the liveness probe. Disabled by default — operators
	// must opt in because the worst case wall-clock budget per host grows
	// from one Timeout (parallel liveness) to roughly two Timeouts
	// (liveness + the longest deep dial).
	DeepProbe bool `json:"deep_probe,omitempty"`
	// DeepProbePorts is the TCP port list dialled by the deep-probe pass.
	// Empty falls back to a small "top services" list when DeepProbe is on
	// (see scanner.defaultDeepProbePorts).
	DeepProbePorts []int `json:"deep_probe_ports,omitempty"`
	// UDPPorts is the UDP port list to probe per live host. Best-effort:
	// open is recorded only when a response arrives, closed when the kernel
	// surfaces an ICMP port-unreachable error. The ambiguous "no reply"
	// case is not persisted to avoid filling the ports table with noise.
	// Disabled when empty (the default).
	UDPPorts []int `json:"udp_ports,omitempty"`
	// EnrichARP populates Host.MACAddress and Host.Vendor from /proc/net/arp
	// for hosts on a directly-attached subnet. Linux only; on other
	// platforms the lookup is a silent no-op. Default false to preserve
	// previous behaviour (and because the lookup requires the host to
	// already be in the kernel's neighbour cache).
	EnrichARP bool `json:"enrich_arp,omitempty"`
}

// SubnetProfile overrides the scanner defaults for one subnet. Each field
// is optional; the zero value means "use the global ScannerConfig default".
// The Subnet field is the only required entry — everything else inherits.
type SubnetProfile struct {
	// Subnet is the CIDR (required). Must be unique across profiles.
	Subnet string `json:"subnet"`

	// ScanInterval overrides ScannerConfig.ScanInterval. Use a long
	// interval for guest networks, short for critical infra. Zero =
	// inherit global.
	ScanInterval Duration `json:"scan_interval,omitempty"`
	// Timeout overrides ScannerConfig.Timeout. Slow links benefit from
	// a longer per-dial budget. Zero = inherit global.
	Timeout Duration `json:"timeout,omitempty"`
	// ProbePorts overrides ScannerConfig.ProbePorts. nil = inherit
	// global; an empty (non-nil) slice means "no liveness probe" — not
	// supported, validate rejects the empty case.
	ProbePorts []int `json:"probe_ports,omitempty"`
	// DeepProbe overrides ScannerConfig.DeepProbe. Pointer so a profile
	// can explicitly set it false while the global is true (otherwise
	// the zero value would be ambiguous). Use config.True / False
	// helpers below.
	DeepProbe *bool `json:"deep_probe,omitempty"`
	// DeepProbePorts overrides ScannerConfig.DeepProbePorts.
	DeepProbePorts []int `json:"deep_probe_ports,omitempty"`
	// UDPPorts overrides ScannerConfig.UDPPorts.
	UDPPorts []int `json:"udp_ports,omitempty"`
	// EnrichARP overrides ScannerConfig.EnrichARP. See DeepProbe re:
	// pointer-bool.
	EnrichARP *bool `json:"enrich_arp,omitempty"`
}

// True / False are bool-pointer helpers for SubnetProfile.DeepProbe and
// EnrichARP. JSON unmarshalling handles the wire form; these are for Go
// callers building profiles programmatically.
func True() *bool  { v := true; return &v }
func False() *bool { v := false; return &v }

// ResolvedProfile is a fully-defaulted profile — every field is populated
// from either the profile's own setting or the global ScannerConfig
// fallback. Produced by ScannerConfig.Resolve(); consumed by the agent.
type ResolvedProfile struct {
	Subnet         string
	ScanInterval   time.Duration
	Timeout        time.Duration
	ProbePorts     []int
	DeepProbe      bool
	DeepProbePorts []int
	UDPPorts       []int
	EnrichARP      bool
}

// Resolve flattens Subnets + Profiles into a unified list with every
// field populated. The flat-Subnets form is preserved for backwards
// compat: each entry becomes a profile with all overrides empty (so it
// inherits every global default).
//
// Validation:
//   - At most one of Subnets / Profiles may be set.
//   - Profile.Subnet values must be unique.
//   - Profile.Subnet must be a parseable CIDR (caller doesn't need to
//     re-validate).
func (c *ScannerConfig) Resolve() ([]ResolvedProfile, error) {
	if len(c.Subnets) > 0 && len(c.Profiles) > 0 {
		return nil, fmt.Errorf("scanner.subnets and scanner.profiles are mutually exclusive — pick one")
	}
	var profiles []SubnetProfile
	switch {
	case len(c.Profiles) > 0:
		profiles = c.Profiles
	case len(c.Subnets) > 0:
		profiles = make([]SubnetProfile, len(c.Subnets))
		for i, s := range c.Subnets {
			profiles[i] = SubnetProfile{Subnet: s}
		}
	}
	seen := make(map[string]bool, len(profiles))
	out := make([]ResolvedProfile, 0, len(profiles))
	for _, p := range profiles {
		if p.Subnet == "" {
			return nil, fmt.Errorf("scanner profile missing subnet")
		}
		if seen[p.Subnet] {
			return nil, fmt.Errorf("scanner profile subnet %q listed twice", p.Subnet)
		}
		seen[p.Subnet] = true
		out = append(out, resolveProfile(p, c))
	}
	return out, nil
}

func resolveProfile(p SubnetProfile, c *ScannerConfig) ResolvedProfile {
	r := ResolvedProfile{
		Subnet:         p.Subnet,
		ScanInterval:   p.ScanInterval.Duration,
		Timeout:        p.Timeout.Duration,
		ProbePorts:     p.ProbePorts,
		DeepProbePorts: p.DeepProbePorts,
		UDPPorts:       p.UDPPorts,
	}
	if r.ScanInterval == 0 {
		r.ScanInterval = c.ScanInterval.Duration
	}
	if r.Timeout == 0 {
		r.Timeout = c.Timeout.Duration
	}
	if r.ProbePorts == nil {
		r.ProbePorts = c.ProbePorts
	}
	if r.DeepProbePorts == nil {
		r.DeepProbePorts = c.DeepProbePorts
	}
	if r.UDPPorts == nil {
		r.UDPPorts = c.UDPPorts
	}
	if p.DeepProbe != nil {
		r.DeepProbe = *p.DeepProbe
	} else {
		r.DeepProbe = c.DeepProbe
	}
	if p.EnrichARP != nil {
		r.EnrichARP = *p.EnrichARP
	} else {
		r.EnrichARP = c.EnrichARP
	}
	return r
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
	// TLSCertPath / TLSKeyPath, when both set, switch the listener to
	// HTTPS. The cert chain at TLSCertPath is presented; the private key
	// at TLSKeyPath must match. Without these the server listens plain
	// HTTP (the existing behaviour).
	TLSCertPath string `json:"tls_cert_path,omitempty"`
	TLSKeyPath  string `json:"tls_key_path,omitempty"`
	// ClientCAPath, when set, requires every incoming connection to
	// present a client certificate signed by the CA in ClientCAPath
	// (mTLS). Spoofed peers can no longer return a healthy /status under
	// this configuration. Bearer tokens stack on top.
	ClientCAPath string `json:"client_ca_path,omitempty"`
}

type AdminConfig struct {
	// Addr is the address the admin web console listens on.
	// Default is 127.0.0.1:9090 (loopback only). When bound off-loopback the
	// agent refuses to start unless AuthToken is also set, because the console
	// exposes the full inventory, exports, the JSON API, and POST /scan
	// (OWASP A01/A05).
	Addr string `json:"addr"`
	// AuthToken is the shared secret required to reach the admin console when
	// Addr is not a loopback bind. Clients authenticate with either
	// `Authorization: Bearer <token>` (curl/API) or HTTP Basic auth using the
	// token as the password (so browsers get a native login prompt). Leave
	// empty (and the file chmod 600) for the loopback-only default deployment.
	AuthToken string `json:"auth_token,omitempty"`
}

type WatchdogConfig struct {
	// PeerAddr is the base URL of the peer agent's health server
	// (e.g. "http://localhost:8081" or "https://neuromancer.local:8443").
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
	// TLS is the optional mTLS configuration for HTTPS peers. Required when
	// peer_addr is https://; ignored otherwise.
	TLS TLSConfig `json:"tls,omitempty"`
}

// TLSConfig pins a peer (or the local server) to a specific CA and,
// optionally, requires an mTLS client certificate. Used by both the
// watchdog's outbound client and the health server's inbound listener,
// so the same struct documents both sides of the handshake.
type TLSConfig struct {
	// CACertPath is the path to a PEM-encoded CA bundle that signs the
	// peer's server certificate. Empty falls back to the system roots,
	// which is rarely what you want for an internal mesh — set this to a
	// project-specific CA so a compromised public CA can't impersonate
	// peers.
	CACertPath string `json:"ca_cert_path,omitempty"`
	// ClientCertPath / ClientKeyPath enable mTLS — the watchdog presents
	// this certificate to the peer, and the health server requires a
	// client cert signed by ClientCAPath (server side). Empty disables
	// mTLS; bearer tokens remain the only auth.
	ClientCertPath string `json:"client_cert_path,omitempty"`
	ClientKeyPath  string `json:"client_key_path,omitempty"`
	// ServerName, when set, overrides the SNI / certificate-verification
	// hostname. Useful when the peer is addressed by IP but the cert is
	// issued for a name.
	ServerName string `json:"server_name,omitempty"`
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
		defer func() { _ = f.Close() }()
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
	hasSecret := c.Health.AuthToken != "" || c.Watchdog.PeerToken != "" || c.Admin.AuthToken != ""
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
	if !isLoopbackBind(c.Admin.Addr) && c.Admin.AuthToken == "" {
		return fmt.Errorf("admin.addr %q is not loopback; set admin.auth_token (or INVENTORY_ADMIN_TOKEN) to gate the console, exports, JSON API, and POST /scan (binding off-loopback without a token is OWASP A01/A05)", c.Admin.Addr)
	}
	if c.Alerts.Webhook.URL != "" {
		if err := validateSinkURL(c.Alerts.Webhook.URL, "http", "https"); err != nil {
			return fmt.Errorf("alerts.webhook.url: %w", err)
		}
	}
	if c.Alerts.Syslog.Addr != "" {
		if err := validateSinkURL(c.Alerts.Syslog.Addr, "udp", "tcp"); err != nil {
			return fmt.Errorf("alerts.syslog.addr: %w", err)
		}
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

// validateSinkURL rejects alert-sink targets (webhook.url, syslog.addr) whose
// scheme is not in the allowed set, or that carry no host. This is the same
// scheme-confusion guard applied to watchdog.peer_addr (OWASP A10): without it
// a webhook URL like file:///etc/passwd or gopher://… reaches the HTTP client
// verbatim. It does not block private/internal hosts — internal receivers
// (an in-cluster collector, localhost) are a legitimate, common deployment.
func validateSinkURL(raw string, schemes ...string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	scheme := strings.ToLower(u.Scheme)
	for _, allowed := range schemes {
		if scheme == allowed {
			if u.Host == "" {
				return fmt.Errorf("missing host in %q", raw)
			}
			return nil
		}
	}
	return fmt.Errorf("scheme %q not allowed; must be one of %v", scheme, schemes)
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
	if v := os.Getenv("INVENTORY_ADMIN_TOKEN"); v != "" {
		cfg.Admin.AuthToken = v
	}
	// Listener addresses also come from env so containerised deployments
	// can repoint without rewriting the JSON file (e.g.
	// INVENTORY_HEALTH_ADDR=0.0.0.0:18080 + INVENTORY_AUTH_TOKEN=... in
	// the orchestrator). Validation in validate() catches off-loopback
	// binds without a token regardless of which surface set the addr.
	if v := os.Getenv("INVENTORY_HEALTH_ADDR"); v != "" {
		cfg.Health.Addr = v
	}
	if v := os.Getenv("INVENTORY_ADMIN_ADDR"); v != "" {
		cfg.Admin.Addr = v
	}
}
