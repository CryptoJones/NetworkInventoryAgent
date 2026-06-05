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
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/Ronin48/NetworkInventoryAgent/internal/admin"
	"github.com/Ronin48/NetworkInventoryAgent/internal/agent"
	"github.com/Ronin48/NetworkInventoryAgent/internal/alerts"
	"github.com/Ronin48/NetworkInventoryAgent/internal/config"
	"github.com/Ronin48/NetworkInventoryAgent/internal/health"
	"github.com/Ronin48/NetworkInventoryAgent/internal/logging"
	"github.com/Ronin48/NetworkInventoryAgent/internal/metrics"
	"github.com/Ronin48/NetworkInventoryAgent/internal/sqlite"
	"github.com/Ronin48/NetworkInventoryAgent/internal/tlsutil"
	"github.com/Ronin48/NetworkInventoryAgent/internal/tracing"
	"github.com/Ronin48/NetworkInventoryAgent/internal/watchdog"
)

// Version is overridable at link time via -ldflags="-X .../runtime.Version=...".
// Defaults to whatever runtime/debug can recover from the build (commit hash
// when built from a checkout, "unknown" otherwise). Kept as a single source
// of truth for both -version and any future User-Agent / metric exposition.
var Version = ""

// VersionString returns a printable version. Falls back to the VCS revision
// embedded by `go build` when Version is empty, which covers the
// non-goreleaser local-build case. Exported so cmd/console (which doesn't
// go through Run) can produce the same -version output.
func VersionString() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			return s.Value
		}
	}
	return "dev"
}

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
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		_, _ = fmt.Fprintf(os.Stdout, "%s %s\n", opts.Name, VersionString())
		return 0
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "err", err)
		return 1
	}
	logging.Setup(cfg.Log, opts.Name)

	traceShutdown, err := tracing.Setup(context.Background(), opts.Name, cfg.Tracing.Endpoint)
	if err != nil {
		// A bad OTLP endpoint shouldn't keep the agent off the network — log
		// and fall through to a no-op tracer rather than failing boot.
		slog.Warn("tracing setup failed; running without span export", "err", err)
		traceShutdown = func(context.Context) error { return nil }
	}

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

	healthTLS, err := tlsutil.ServerConfig(
		cfg.Health.TLSCertPath, cfg.Health.TLSKeyPath, cfg.Health.ClientCAPath,
	)
	if err != nil {
		slog.Error("failed to load health TLS config", "err", err)
		return 1
	}
	healthSrv := health.NewServer(cfg.Health.Addr, tracker, health.ServerOptions{
		StaleAfter: 3 * cfg.Scanner.ScanInterval.Duration,
		AuthToken:  cfg.Health.AuthToken,
		TLSConfig:  healthTLS,
	})
	if err := healthSrv.Start(); err != nil {
		slog.Error("failed to start health server", "addr", cfg.Health.Addr, "err", err)
		return 1
	}
	slog.Info("health server started", "addr", healthSrv.Addr(), "tls", healthTLS != nil)

	alertSinks := buildAlertSinks(cfg.Alerts)
	mux := alerts.NewMultiplexer(alertSinks...)
	if len(alertSinks) > 0 {
		slog.Info("alert sinks configured", "count", len(alertSinks))
	}

	a, err := agent.New(opts.Name, cfg.Scanner, db.Hosts(), db.Ports(), db.Scans(), tracker, mux)
	if err != nil {
		slog.Error("agent setup failed", "err", err)
		return 1
	}

	adminSrv, err := admin.NewServer(
		cfg.Admin.Addr, opts.Name,
		db.Hosts(), db.Ports(), db.Scans(),
		tracker.Get, a.Trigger,
		admin.ServerOptions{AuthToken: cfg.Admin.AuthToken},
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

	// PeerUp gauge is -1 when no watchdog is configured so Grafana panels can
	// distinguish "peer down" (0) from "no peer configured" (-1).
	metrics.PeerUp.Set(-1)

	if opts.WithWatchdog && cfg.Watchdog.PeerAddr != "" {
		peerClient, err := buildPeerClient(cfg.Watchdog)
		if err != nil {
			slog.Error("failed to build peer client", "err", err)
			return 1
		}
		wd := watchdog.New(watchdog.Config{
			Name:            opts.Name,
			PeerAddr:        cfg.Watchdog.PeerAddr,
			Interval:        cfg.Watchdog.Interval.Duration,
			ScanInterval:    cfg.Scanner.ScanInterval.Duration,
			MaxHostDriftPct: cfg.Watchdog.MaxHostDriftPct,
			MaxFailures:     cfg.Watchdog.MaxFailures,
		}, peerClient, tracker.Get, tracker.SetPeer)
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
	if err := traceShutdown(shutdownCtx); err != nil {
		slog.Warn("tracer shutdown error", "err", err)
	}
	mux.Close()
	slog.Info("agent stopped", "name", opts.Name)
	return 0
}

// buildAlertSinks instantiates the configured event sinks. A sink that
// fails to construct (bad syslog URL, etc.) is logged and skipped; the
// other sinks still ship. Returns nil when nothing is configured, which
// NewMultiplexer handles cleanly.
func buildAlertSinks(cfg config.AlertsConfig) []alerts.Sink {
	var sinks []alerts.Sink
	if w := alerts.NewWebhookSink(cfg.Webhook.URL, cfg.Webhook.AuthHeader); w != nil {
		sinks = append(sinks, w)
	}
	if cfg.Syslog.Addr != "" {
		s, err := alerts.NewSyslogSink(cfg.Syslog.Addr, cfg.Syslog.Tag, cfg.Syslog.Facility)
		if err != nil {
			slog.Warn("syslog sink disabled", "err", err)
		} else if s != nil {
			sinks = append(sinks, s)
		}
	}
	return sinks
}

// buildPeerClient assembles the watchdog's health.Client with a
// tracing-instrumented transport. TLS is layered onto the transport before
// otelhttp wraps it, so spans cover the full handshake-through-read path.
func buildPeerClient(wd config.WatchdogConfig) (*health.Client, error) {
	transport, err := buildPeerTransport(wd.TLS)
	if err != nil {
		return nil, err
	}
	httpClient := tracing.HTTPClient(transport)
	httpClient.Timeout = 5 * time.Second
	return health.NewClientWith(wd.PeerAddr, health.ClientOptions{
		Token:      wd.PeerToken,
		HTTPClient: httpClient,
	}), nil
}

// buildPeerTransport returns an http.RoundTripper for the watchdog's HTTP
// client. When TLS is unconfigured (the common loopback case) it returns
// http.DefaultTransport untouched; otherwise it clones the default transport
// and replaces TLSClientConfig with the project-pinned one.
func buildPeerTransport(tlsCfg config.TLSConfig) (http.RoundTripper, error) {
	t, err := tlsutil.ClientConfig(tlsCfg)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return http.DefaultTransport, nil
	}
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = t
	return base, nil
}
