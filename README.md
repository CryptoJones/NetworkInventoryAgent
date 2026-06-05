# NetworkInventoryAgent

A lightweight, autonomous network inventory agent that discovers, catalogs, and reports on devices and assets across your network infrastructure.

## Overview

NetworkInventoryAgent continuously scans your network to build and maintain an up-to-date inventory of all connected devices. It identifies hosts, open ports, running services, operating systems, and hardware details — giving you a living map of your network without requiring manual audits.

The system is designed to run as **two cooperating agent instances** — named **Wintermute** and **Neuromancer** — that scan the same subnets independently and continuously sanity-check each other. If either agent crashes, stalls, or starts reporting wildly different data, the other detects it and logs a clear warning. This mutual watchdog architecture means the inventory is never silently wrong.

## Features

- **Active discovery** — concurrent TCP-probe scanning across configurable CIDR ranges to find live hosts. Optional deep TCP and UDP probe passes per profile.
- **Asset fingerprinting** — banner-grab on SSH, FTP, SMTP, POP3, IMAP, HTTP, HTTPS (with TLS cert peek), MySQL handshake, PostgreSQL (SSLRequest probe), Redis (`INFO`), Memcached (`version`), VNC (RFB greeting), RDP (X.224), MSSQL (TDS pre-login), MongoDB (wire `isMaster`), Telnet, plus UDP DNS and NTP (stratum) probes. Stored per-port in `Port.Service`.
- **Device-type classifier** — heuristic rules over (vendor, OS banner, open ports) tag hosts as printer / router / hypervisor / windows-host / windows-server / windows-dc / nas / database (mysql|postgres|…) / mail-server / dns-server / kubernetes-node / container-host / camera / linux-host / appliance / iot-broker / embedded.
- **MAC + vendor enrichment** — neighbour-cache lookup on Linux (`/proc/net/arp`), macOS (routing socket) and Windows (`GetIpNetTable`), all shell-free + embedded OUI prefix table for ~90 common vendors, including major IP-camera (Hikvision/Dahua/Axis), NAS (Synology/QNAP/WD), networking (Ubiquiti), and IoT (Espressif) brands that also drive device classification.
- **Per-subnet scan profiles** — aggressive hourly deep scans on critical infra, lazy daily liveness on guest networks, all in one config.
- **Change detection + alerts** — diffs host inventory each cycle; fires `host.discovered` / `host.vanished` events to HTTP webhook and/or RFC 5424 syslog.
- **JSON query API** — `/api/v1/hosts` with filters (vendor, device type, hostname, subnet, port) and pagination; `/api/v1/hosts/{ip}` with nested ports; `/api/v1/scans` paginated scan history (optional `subnet` filter).
- **Continuous monitoring** — periodic re-scans detect new devices, removed devices, and configuration changes over time.
- **Mutual watchdog** — two agent instances cross-check each other for liveness, scan freshness, and inventory consistency. Optional mTLS between peers.
- **Web admin console** — dark-themed browser UI with dashboard, host inventory, per-host port detail, scan history, watchdog peer status; auto-starts alongside each agent.
- **Terminal UI console** — full-featured Bubbletea TUI (`cmd/console`) providing the same views as the web console; connects directly to any agent's SQLite database.
- **Prometheus `/metrics`** — counters for scans, probes, DB errors, watchdog events, alerts; gauges for host count and peer-up state. Dependency-free exposer.
- **OpenTelemetry tracing** — OTLP/HTTP exporter, W3C TraceContext propagation across the watchdog peer hop.
- **Structured logging** — human-readable text or machine-readable JSON log output via `log/slog`.
- **Graceful shutdown** — SIGINT / SIGTERM cancel in-flight scans cleanly before exit.
- **Multi-platform releases** — signed binaries (cosign keyless OIDC) for linux/darwin/windows × amd64/arm64, plus a multi-arch Docker image on `ghcr.io`. CycloneDX SBOMs per archive.
- **Low footprint** — no external server process; the database is a single SQLite file.

## Requirements

- Go 1.25+
- Network access to the target subnets

No C toolchain is required. The SQLite driver (`modernc.org/sqlite`) is pure Go.

## Installation

### Docker (multi-arch, signed)

```bash
docker pull ghcr.io/cryptojones/networkinventoryagent:latest
docker run --rm ghcr.io/cryptojones/networkinventoryagent:latest -version
```

The `:latest` and `:<version>` tags both point at multi-arch manifests
(linux/amd64 + linux/arm64); your Docker client picks the right one for
the host. The manifests are signed with `cosign` keyless OIDC — verify with:

```bash
cosign verify \
  --certificate-identity-regexp 'https://github.com/CryptoJones/NetworkInventoryAgent/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/cryptojones/networkinventoryagent:<version>
```

The image's default entrypoint is `agent` (standalone). To run the paired
Wintermute/Neuromancer mode, override the entrypoint:

```bash
docker run --rm \
  --entrypoint /usr/local/bin/wintermute \
  ghcr.io/cryptojones/networkinventoryagent:latest -version
```

### Pre-built binaries (signed)

Tagged releases at <https://github.com/CryptoJones/NetworkInventoryAgent/releases>
ship binaries for linux/darwin/windows × amd64/arm64. Every archive contains
a CycloneDX SBOM and every artefact is signed with `cosign` keyless OIDC
(via GitHub Actions). Verify before running:

```bash
cosign verify-blob \
  --certificate-identity-regexp 'https://github.com/CryptoJones/NetworkInventoryAgent/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate networkinventoryagent_<ver>_linux_amd64.tar.gz.pem \
  --signature   networkinventoryagent_<ver>_linux_amd64.tar.gz.sig \
                networkinventoryagent_<ver>_linux_amd64.tar.gz
```

### Build from source

```bash
git clone https://codeberg.org/Ronin48/NetworkInventoryAgent.git
cd NetworkInventoryAgent
go build -o wintermute  ./cmd/wintermute
go build -o neuromancer ./cmd/neuromancer
go build -o console     ./cmd/console
```

### Windows Installation

The project builds natively on Windows using the standard Go toolchain. You can either:
- Use Git Bash/MSYS2/WSL to run the provided shell scripts
- Or execute the equivalent commands manually in Command Prompt/PowerShell

To build natively on Windows:
```cmd
go build -o wintermute.exe  ./cmd/wintermute
go build -o neuromancer.exe ./cmd/neuromancer
go build -o console.exe     ./cmd/console
```

For cross-compilation from Linux/macOS to Windows:
```bash
GOOS=windows GOARCH=amd64 go build -o wintermute.exe  ./cmd/wintermute
GOOS=windows GOARCH=amd64 go build -o neuromancer.exe ./cmd/neuromancer
```

Or use `make`:

```bash
make build   # compiles all binaries
make test    # runs the full test suite with the race detector
make lint    # gofmt + go vet
```

## Docker

The repository ships a multi-stage `Dockerfile` and a `docker-compose.yml` that runs the Wintermute/Neuromancer pair.

### Quick start

```bash
docker compose up --build -d
```

This compiles both agent binaries in a `golang:1.25-bookworm` build stage and runs them in a minimal `alpine:3.20` image as a non-root user. Two containers start:

| Container | Health port | Admin console | Watchdog peer |
|-----------|------------|---------------|---------------|
| `wintermute` | `8080` | `9090` | `http://neuromancer:8081` |
| `neuromancer` | `8081` | `9091` | `http://wintermute:8080` |

Databases are written to named Docker volumes (`wintermute-db`, `neuromancer-db`) and persist across restarts.

### Running a single agent

```bash
docker run -d \
  -v "$PWD/configs/wintermute.docker.json:/etc/inventory/config.json:ro" \
  -v inventorydata:/data \
  -p 8080:8080 \
  -p 9090:9090 \
  --entrypoint /usr/local/bin/wintermute \
  networkinventoryagent -config /etc/inventory/config.json
```

### Make targets

| Target | Description |
|--------|-------------|
| `make docker-build` | Build the image locally |
| `make docker-up` | Start the Wintermute/Neuromancer pair in the background |
| `make docker-down` | Stop and remove containers |
| `make docker-logs` | Tail combined logs from both agents |

### Docker-specific config

The configs in `configs/*.docker.json` differ from the local configs in four ways:

1. `health.addr` binds to `0.0.0.0:<port>` so Docker's network stack can route traffic into the container.
2. `admin.addr` binds to `0.0.0.0:9090` so the admin console is reachable from the host.
3. `watchdog.peer_addr` uses the Compose service name (`http://neuromancer:8081`) instead of `localhost`.
4. `database.path` writes to `/data/<name>.db` inside the mounted volume.

Edit the `subnets` list in these files before deploying.

## Running the agents locally

### Quick start with the startup script

The easiest way to run the agents locally is `start.sh`. It builds the binaries, optionally updates the subnet list in your config files, then starts the agents and prints the console URLs. Press `Ctrl+C` to stop everything cleanly.

**Prerequisites:** Go 1.25+ and `jq` must be on your `PATH`.

```bash
# Interactive — prompts for mode and subnets
./start.sh

# Non-interactive examples
./start.sh --mode paired     --subnet 192.168.1.0/24
./start.sh --mode standalone --subnet 10.0.0.0/24 --subnet 10.1.0.0/24

# Build binaries only, do not start agents
./start.sh --build-only
```

#### Startup script options

| Flag | Values | Description |
|------|--------|-------------|
| `-m`, `--mode` | `paired` \| `standalone` | Agent mode (default: interactive prompt) |
| `-s`, `--subnet` | CIDR, e.g. `10.0.0.0/24` | Subnet to scan — repeat for multiple subnets |
| `-b`, `--build-only` | — | Build binaries and exit without starting |
| `-h`, `--help` | — | Show usage |

**Paired mode** starts Wintermute and Neuromancer as a mutual-watchdog pair (recommended). **Standalone mode** starts a single agent with no watchdog peer.

### Manual startup

If you prefer to start the agents yourself, build and run them directly.

**Requirements:** Go 1.25+. No C toolchain needed.

Edit the `subnets` list in the relevant config file first, then:

```bash
# Build
go build -o wintermute  ./cmd/wintermute
go build -o neuromancer ./cmd/neuromancer
go build -o agent       ./cmd/agent
go build -o console     ./cmd/console

# Paired mode (two terminals)
./wintermute  -config configs/wintermute.json   # Terminal 1
./neuromancer -config configs/neuromancer.json  # Terminal 2

# Standalone mode
./agent -config configs/agent.json
```

Each agent:

1. Opens its own SQLite database
2. Starts an HTTP health server (Wintermute on `127.0.0.1:8080`, Neuromancer on `127.0.0.1:8081`)
3. Starts the web admin console (Wintermute on `127.0.0.1:9090`, Neuromancer on `127.0.0.1:9091`)
4. Launches a watchdog goroutine pointed at its partner's health server
5. Runs the scan loop in the foreground until it receives a signal

Ready-to-use configs are in `configs/`. Press `Ctrl+C` to stop an agent cleanly.

## Admin console

Each agent automatically starts a browser-based admin console alongside the scan loop. The console does not require any additional setup — open the address logged at startup to explore the current inventory.

### Web console

| Page | URL | Description |
|------|-----|-------------|
| Dashboard | `/` | Summary cards and latest 10 scans and hosts; auto-refreshes every 30 s |
| Host inventory | `/hosts` | List of discovered hosts with metadata; paginated (`?limit=`, `?offset=`, default 100) |
| Host detail | `/hosts/{ip}` | Per-host metadata and open port table |
| Scan history | `/scans` | Subnet sweeps with duration and status; paginated (`?limit=`, `?offset=`, default 100) |

### Terminal UI console

The `console` binary connects directly to any agent's SQLite database and provides the same views in a Bubbletea TUI. It opens the database read-only so it is safe to run against a live agent's database file.

```bash
./console -db wintermute.db
```

| Key | Action |
|-----|--------|
| `1` | Dashboard |
| `2` | Host inventory |
| `3` | Scan history |
| `Enter` | Drill into host detail (ports) |
| `Esc` / `Backspace` | Back to host list |
| `r` | Refresh current view |
| `q` / `Ctrl+C` | Quit |

## How the mutual watchdog works

Every `watchdog.interval` seconds, each agent performs three checks against its partner:

### 1. Liveness

```
GET /health  →  200 OK (healthy) | 503 Service Unavailable (unhealthy)
```

If the peer fails to respond or returns a non-200 status, the failure is logged as a warning. After `max_failures` consecutive failures the peer is declared **DOWN** and an error is logged. The watchdog never kills or restarts the peer — that is left to an external supervisor (systemd, Docker, Kubernetes).

### 2. Freshness

```
GET /status  →  JSON { last_scan_at, scan_count, host_count, ... }
```

If the peer's `last_scan_at` timestamp is older than `2 × scanner.scan_interval`, the peer is considered stale and a warning is logged. This catches a peer that is alive and responding to pings but whose scan loop has silently stopped making progress.

### 3. Consistency

If both agents have completed at least one scan, their `host_count` values are compared. If the percentage difference exceeds `max_host_drift_pct`, a warning is logged:

```
drift_pct = |local_hosts - peer_hosts| / max(local_hosts, peer_hosts) × 100
```

This catches split-brain scenarios where both agents are running but scanning different effective subsets of the network (e.g., due to a routing change or misconfiguration).

## Configuration

Each agent reads a JSON config file and then applies environment variable overrides on top. Environment variables always win, which makes the agents suitable for Docker and Kubernetes deployments.

### Full config reference

```json
{
  "database": {
    "path": "wintermute.db"
  },
  "scanner": {
    "subnets": ["192.168.1.0/24", "10.0.0.0/24"],
    "scan_interval": "5m",
    "timeout": "2s",
    "workers": 50,
    "max_hosts": 65535
  },
  "log": {
    "level": "info",
    "format": "text"
  },
  "health": {
    "addr": "127.0.0.1:8080"
  },
  "admin": {
    "addr": "127.0.0.1:9090"
  },
  "watchdog": {
    "peer_addr": "http://localhost:8081",
    "interval": "30s",
    "max_host_drift_pct": 50.0,
    "max_failures": 3
  }
}
```

| Key | Default | Description |
|-----|---------|-------------|
| `database.path` | `inventory.db` | SQLite database file. Use `:memory:` for tests. |
| **Scanner — global defaults** | | |
| `scanner.subnets` | `[]` | Legacy flat CIDR list. Mutually exclusive with `scanner.profiles`. |
| `scanner.profiles` | `[]` | Per-subnet override list (see below). |
| `scanner.scan_interval` | `5m` | How often to re-scan; default for any profile that doesn't set its own. |
| `scanner.timeout` | `2s` | Per-host TCP probe timeout; also bounds reverse-DNS (PTR) lookups. |
| `scanner.workers` | `50` | GLOBAL concurrent probe cap across every subnet (not per-subnet). |
| `scanner.max_hosts` | `65535` | Maximum usable addresses per subnet; larger subnets are rejected. |
| `scanner.probe_ports` | `[22, 80, 443, 8080]` | TCP liveness ports — host alive if any answer. |
| `scanner.deep_probe` | `false` | Second-pass scan of `deep_probe_ports` on every live host. |
| `scanner.deep_probe_ports` | `top-services list` | TCP ports for the deep pass when `deep_probe` is on. |
| `scanner.udp_ports` | `[]` | UDP ports to probe per live host. Empty disables UDP probing. |
| `scanner.enrich_arp` | `false` | Populate Host.MACAddress + Vendor from the OS neighbour cache (Linux `/proc/net/arp`, macOS routing socket, Windows `GetIpNetTable`). No-op on other platforms. |
| `scanner.host_ttl` | `0` (disabled) | Hosts not seen within this duration are deleted at the end of each cycle. |
| `scanner.scan_history_ttl` | `0` (disabled) | Scan-history rows older than this duration are deleted at the end of each cycle, bounding the `scans` table and `/scans` view. |
| **Scanner — per-subnet profile (each item in `scanner.profiles`)** | | |
| `subnet` | required | CIDR for this profile. Must be unique. |
| `scan_interval` | inherits global | Per-profile scan cadence. |
| `timeout` | inherits global | Per-profile dial budget. |
| `probe_ports` | inherits global | Per-profile liveness ports. |
| `deep_probe` | inherits global | Per-profile deep probing (bool). |
| `deep_probe_ports` | inherits global | Per-profile deep ports. |
| `udp_ports` | inherits global | Per-profile UDP ports. |
| `enrich_arp` | inherits global | Per-profile ARP enrichment (bool). |
| **Log** | | |
| `log.level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error`. |
| `log.format` | `text` | Log format: `text` (human) or `json` (machine). |
| **Health server** | | |
| `health.addr` | `127.0.0.1:8080` | Listen address for `/health`, `/status`, `/metrics`. |
| `health.auth_token` | — | Bearer token; required when `health.addr` is off-loopback. |
| `health.tls_cert_path` | — | When set with `tls_key_path`, serves HTTPS. |
| `health.tls_key_path` | — | Private key matching `tls_cert_path`. |
| `health.client_ca_path` | — | When set, requires mTLS (clients must present a cert signed by this CA). |
| **Admin console** | | |
| `admin.addr` | `127.0.0.1:9090` | Listen address for the admin console + `/api/v1/*`. |
| `admin.auth_token` | — | Shared secret gating the whole console. Required when `admin.addr` is off-loopback. Clients send `Authorization: Bearer <token>` or HTTP Basic with the token as the password. |
| **Watchdog** | | |
| `watchdog.peer_addr` | — | Base URL of the partner agent's health server. |
| `watchdog.peer_token` | — | Bearer token sent to the peer. Must match peer's `health.auth_token`. |
| `watchdog.interval` | `30s` | How often the watchdog checks the partner. |
| `watchdog.max_host_drift_pct` | `50.0` | Max % host-count difference before a warning. |
| `watchdog.max_failures` | `3` | Consecutive liveness failures before declaring peer DOWN. |
| `watchdog.tls.ca_cert_path` | — | Project CA the peer's cert must chain to. |
| `watchdog.tls.client_cert_path` | — | Client cert for mTLS to the peer. |
| `watchdog.tls.client_key_path` | — | Client key matching `client_cert_path`. |
| `watchdog.tls.server_name` | — | SNI / cert-verification hostname override. |
| **Tracing** | | |
| `tracing.endpoint` | — | OTLP/HTTP collector URL. Empty = no-op exporter (instrumentation active, spans discarded). |
| **Alerts** | | |
| `alerts.webhook.url` | — | HTTP POST target for host.discovered / host.vanished events. Must be `http`/`https`; scheme-validated at startup. |
| `alerts.webhook.auth_header` | — | Verbatim `Authorization` header (e.g. `Bearer abc123`). |
| `alerts.syslog.addr` | — | `udp://host:514` or `tcp://host:514`. RFC 5424. Scheme-validated at startup. |
| `alerts.syslog.tag` | `network-inventory` | APP-NAME field. |
| `alerts.syslog.facility` | `16` (local0) | RFC 5424 facility number 0..23. |

Duration values in the JSON config accept human-readable strings (`"5m"`, `"30s"`, `"2h"`) in addition to raw nanosecond integers.

#### Per-subnet profile example

Aggressive hourly deep scans on critical infrastructure, lazy daily liveness on guest network:

```json
{
  "scanner": {
    "profiles": [
      { "subnet": "10.0.0.0/24", "scan_interval": "1h", "deep_probe": true, "enrich_arp": true },
      { "subnet": "192.168.99.0/24", "scan_interval": "24h" }
    ],
    "scan_interval": "5m",
    "timeout": "2s",
    "workers": 50,
    "host_ttl": "168h"
  }
}
```

Profiles inherit any field they don't set from the `scanner.*` globals.
`scanner.subnets` and `scanner.profiles` are mutually exclusive — boot
fails fast if both are set.

### Environment variable overrides

| Variable | Overrides |
|----------|-----------|
| `INVENTORY_DB_PATH` | `database.path` |
| `INVENTORY_LOG_LEVEL` | `log.level` |
| `INVENTORY_LOG_FORMAT` | `log.format` |
| `INVENTORY_HEALTH_ADDR` | `health.addr` |
| `INVENTORY_ADMIN_ADDR` | `admin.addr` |
| `INVENTORY_AUTH_TOKEN` | `health.auth_token` |
| `INVENTORY_PEER_TOKEN` | `watchdog.peer_token` |
| `INVENTORY_ADMIN_TOKEN` | `admin.auth_token` |

## Health endpoints

Both agents expose two HTTP endpoints used by the watchdog and for external monitoring:

**Health server** (default `127.0.0.1:8080`, bearer-gated when off-loopback):

| Endpoint | Method | Response |
|----------|--------|----------|
| `/health` | GET | `200 OK` if healthy and last scan is fresh; `503 Service Unavailable` otherwise |
| `/status` | GET | JSON-encoded status snapshot (see below) |
| `/metrics` | GET | Prometheus text exposition format — counters for scans, probes, DB, watchdog, alerts; gauges for host count + peer-up state |

**Admin console** (default `127.0.0.1:9090`). Unauthenticated on the loopback default; set `admin.auth_token` (or `INVENTORY_ADMIN_TOKEN`) to gate every route below. A token is **required** when binding off-loopback — the agent refuses to start otherwise. Authenticate with `Authorization: Bearer <token>` or HTTP Basic auth using the token as the password (browsers get a native login prompt):

| Endpoint | Method | Response |
|----------|--------|----------|
| `/` | GET | HTML dashboard |
| `/hosts` | GET | HTML host inventory (paginated: `?limit=`, `?offset=`) |
| `/hosts/{ip}` | GET | HTML host detail (with ports) |
| `/scans` | GET | HTML scan history (paginated: `?limit=`, `?offset=`) |
| `/watchdog` | GET | HTML watchdog peer-status panel |
| `/export.json` | GET | Full inventory snapshot as JSON |
| `/export.csv` | GET | Full inventory snapshot as CSV |
| `/api/v1/hosts` | GET | Filterable JSON list — `?vendor=`, `?device_type=`, `?hostname=`, `?subnet=`, `?port=`, `?limit=`, `?offset=` |
| `/api/v1/hosts/{ip}` | GET | Single-host JSON with nested ports |
| `/api/v1/scans` | GET | Paginated JSON scan history — `?subnet=`, `?limit=`, `?offset=` |
| `/scan` | POST | Trigger an out-of-cycle scan (CSRF-gated) |

### `/status` response

```json
{
  "name":         "wintermute",
  "healthy":      true,
  "started_at":   "2024-01-15T10:00:00Z",
  "last_scan_at": "2024-01-15T10:05:00Z",
  "host_count":   42,
  "scan_count":   3
}
```

## Project layout

```
cmd/
  agent/          Generic single-agent binary (no watchdog peer required).
  wintermute/     Wintermute entry point. Watchdog pointed at Neuromancer.
  neuromancer/    Neuromancer entry point. Watchdog pointed at Wintermute.
  console/        Interactive Bubbletea TUI console. Opens the SQLite
                  database directly (read-only); no agent required.
    tui/          TUI model, views, and lipgloss styles.

configs/
  wintermute.json         Local config for Wintermute.
  neuromancer.json        Local config for Neuromancer.
  wintermute.docker.json  Docker config for Wintermute (0.0.0.0 binding,
                          service-name peer address, /data volume path).
  neuromancer.docker.json Docker config for Neuromancer.

models/           Pure domain types (Host, Port, Scan). No database
                  imports, no business logic — just structs.

internal/
  store/          Persistence interfaces (HostStore, PortStore, ScanStore)
                  and the ErrNotFound sentinel. The rest of the application
                  depends only on these interfaces, never on a concrete DB.

  sqlite/         SQLite implementations of the store interfaces.
    migrations/   Versioned SQL files embedded into the binary at
                  compile time. The runner records each applied
                  migration in schema_migrations and wraps each one
                  in a transaction, so a failed migration never
                  leaves the schema in a partial state.

  config/         Config loading: JSON file merged with environment
                  variable overrides. Custom Duration type supports
                  human-readable strings ("5m") in JSON and marshals
                  back to the same format.

  health/         Status type, concurrency-safe Tracker, HTTP server
                  (/health and /status endpoints), and HTTP client
                  used by the watchdog to poll its partner.

  admin/          Web admin console HTTP server. Parses embedded HTML
                  templates at startup. Serves dashboard, host inventory,
                  per-host port detail, and scan history pages.
    templates/    Embedded HTML templates (Go text/template, GitHub-dark
                  colour scheme). base.html defines the shared head and
                  nav partials used by the four page templates.

  watchdog/       Watchdog loop: runs three checks (liveness,
                  freshness, consistency) against the partner agent
                  on every tick. Logs warnings and errors; never
                  kills or restarts the peer process.

  scanner/        Concurrent TCP-probe network scanner. Skips IPv4
                  network and broadcast addresses. Enforces a
                  configurable per-subnet host limit. Uses a worker
                  pool (semaphore) to bound parallelism. Banner-grabs
                  open ports (banner.go) and tags hosts with a
                  device type (classify.go). ARP enrichment via
                  arp.go on Linux.

  agent/          Periodic scan loop. Resolves per-subnet profiles,
                  drives the scanner across due profiles, runs the
                  host TTL prune, diffs the inventory and emits
                  change events, updates the health Tracker.

  alerts/         host.discovered / host.vanished event subsystem.
                  Multiplexer fans out to WebhookSink (HTTP POST
                  JSON) and SyslogSink (RFC 5424 over UDP/TCP).

  metrics/        Dependency-free Prometheus text-format exposer.
                  Counters and gauges incremented as side effects
                  of the agent's normal work.

  tracing/        OpenTelemetry wiring. OTLP/HTTP exporter,
                  HTTPMiddleware for incoming requests, HTTPClient
                  for outgoing requests.

  tlsutil/        Shared *tls.Config builder. Used by both the
                  health server (inbound TLS / optional mTLS) and
                  the watchdog client (CA pinning to a project CA).

  logging/        Shared slog initialisation helper used by all
                  agent binaries.

start.sh          Local startup script. Builds binaries, optionally
                  updates subnet config, then starts the selected mode
                  (paired or standalone). Ctrl+C stops all agents.

Dockerfile        Multi-stage build: golang:1.25-bookworm → alpine:3.20.
                  Compiles all four binaries; runs as non-root user.
docker-compose.yml Runs the Wintermute/Neuromancer pair with named
                  volumes and Docker health checks. Exposes admin
                  console on ports 9090 (wintermute) and 9091 (neuromancer).
```

## Architecture decisions

These decisions were made at project start to keep the codebase maintainable as it grows. Future contributors should understand the reasoning before changing them.

---

### Mutual watchdog — two named agents (Wintermute and Neuromancer)

The system is intentionally designed to run as a pair. Running a single agent means a silent crash or stalled scan loop goes undetected until someone notices the inventory is stale. Running two independent agents that continuously cross-check each other eliminates that blind spot.

Three checks run on every watchdog tick:

- **Liveness** — is the peer reachable and reporting healthy?
- **Freshness** — has the peer completed a scan recently (within 2× the configured scan interval)?
- **Consistency** — do the two agents agree on how many hosts are on the network (within the drift threshold)?

The watchdog never takes corrective action itself. It only logs. Actual recovery (restart, alert, failover) is the responsibility of an external supervisor. This keeps the watchdog simple, testable, and free of side effects.

The names Wintermute and Neuromancer are a reference to William Gibson's *Neuromancer* (1984), in which two AIs monitor and interact with each other.

---

### Repository interfaces (`internal/store`)

All database access goes through the `HostStore`, `PortStore`, and `ScanStore` interfaces defined in `internal/store`. No package outside `internal/sqlite` ever imports `internal/sqlite` directly.

**Why:** When the project outgrows SQLite — whether because of write volume, multi-node requirements, or team preference — the new backend is written as a new package that satisfies the same interfaces. Business logic, tests, and the rest of the codebase are untouched.

---

### Compile-time interface checks

Every repository type carries a blank-identifier assignment:

```go
var _ store.HostStore = (*HostRepo)(nil)
```

**Why:** If `store.HostStore` gains a new method and `HostRepo` is not updated, the build fails immediately with a clear error pointing at this line. Without this, the mismatch is only caught at runtime (or not at all, if the missing method is never called in tests). In a large legacy codebase this saves hours of debugging.

---

### Versioned, embedded SQL migrations (`internal/sqlite/migrations`)

Schema changes live in numbered SQL files (`001_initial.sql`, `002_add_tags.sql`, etc.) embedded into the binary at compile time using `//go:embed`. A lightweight runner applies any unapplied migrations in order and records each one in a `schema_migrations` table. Each migration runs inside its own transaction.

**Why:** Keeping migrations in separate files means every schema change is reviewable in git history. Embedding them in the binary means deployments are self-contained — no external migration tool or file to distribute. Transactional application means a failed migration never leaves the schema half-applied.

To add a new migration, create the next numbered file: `internal/sqlite/migrations/002_<description>.sql`. The runner picks it up automatically on next startup.

---

### `context.Context` on every store method

All store methods accept a `context.Context` as their first argument.

**Why:** Context is the Go-idiomatic way to propagate deadlines, cancellation signals, and request-scoped values (such as trace IDs). Adding it later requires changing every call site. Adding it now costs nothing and means the codebase is ready for per-request database timeouts, graceful shutdown, and distributed tracing.

---

### SQLite WAL mode and `busy_timeout`

The database is opened with:

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
```

`SetMaxOpenConns(1)` is also set so the driver never opens a second connection.

**Why:** WAL allows concurrent reads during a write, which matters once the agent is also serving health checks while scanning. `busy_timeout` tells SQLite to wait up to 5 seconds for a lock rather than returning `SQLITE_BUSY` immediately. Serialising connections at the driver level is simpler than handling `SQLITE_BUSY` in application code.

---

### Foreign key enforcement

```sql
PRAGMA foreign_keys = ON;
```

**Why:** SQLite does not enforce foreign key constraints by default. Without this pragma, deleting a host would leave orphaned rows in the `ports` table indefinitely. Enabling it ensures referential integrity is maintained at the database level — a safety net that works even when application-level delete logic has bugs.

---

### Human-readable durations in JSON config

The custom `config.Duration` type unmarshals both string values (`"5m"`, `"30s"`) and raw nanosecond integers from JSON, and marshals back to the string form.

**Why:** Raw nanosecond integers (`300000000000`) are unreadable in config files. String durations (`"5m"`) are immediately obvious. This wrapper keeps the rest of the codebase using `time.Duration` natively while making configs human-friendly.

---

### Concurrent scanning with a worker pool

The scanner uses a buffered channel as a semaphore to bound the number of concurrent TCP probe goroutines. The `workers` and `max_hosts` fields in `ScannerConfig` give operators control over resource consumption.

**Why:** A naive sequential scanner is too slow on large subnets (/16 or larger). Unbounded goroutine creation risks exhausting file descriptors. A semaphore provides throughput without runaway resource use.

IPv4 network and broadcast addresses (first and last in subnets with a prefix length of /30 or shorter) are skipped, matching RFC behaviour. /31 and /32 ranges are not skipped (RFC 3021).

---

### `cmd/` entry point structure

Agent binaries live under `cmd/<name>/main.go` rather than a root `main.go`.

**Why:** A root `main.go` implies the repository is a single binary forever. `cmd/<name>/` is the idiomatic Go layout for projects that may grow multiple binaries. Adding a new binary requires no restructuring.

---

### Structured logging (`log/slog`)

All log output goes through `log/slog` from the Go standard library (Go 1.21+). The format is selectable between `text` (human-readable) and `json` (machine-readable) at runtime.

**Why:** Unstructured log strings are difficult to query, alert on, or ingest into log aggregation systems. Structured logging with consistent field names means logs are queryable from day one. Using the stdlib package avoids a dependency and ensures any logging framework added later can wrap or replace it cleanly.

---

### Graceful shutdown via `signal.NotifyContext`

`main` creates a context that is cancelled on `SIGINT` or `SIGTERM` and passes it to all long-running operations.

**Why:** A scanner loop killed mid-write can corrupt state or leave partial scan records. A context-aware shutdown gives in-flight operations the opportunity to finish cleanly before the process exits. This is essential for any agent running under systemd, Kubernetes, or Docker with proper lifecycle management.

---

### SQLite as the database

SQLite was chosen as the initial backing store.

**Why:** For a local network inventory agent, SQLite is the correct default. It requires no server process, no connection string management, no separate installation, and no configuration. The database is a single file that can be copied, backed up, and inspected with standard tooling. It is fully ACID compliant and handles the read/write patterns of a periodic scanner with ease. The repository interface design means that if the project later needs multi-node storage or higher write throughput, the backing store can be replaced without touching any code outside `internal/sqlite`.

---

## Security

See [SECURITY.md](SECURITY.md) for the full OWASP Top 10 compliance table, operator hardening guidance, and how to report vulnerabilities.

Summary of design decisions made for security:

| OWASP | Mitigation |
|-------|-----------|
| A03 Injection | All SQL uses parameterized queries; scanner uses `net.Dialer`, never shell invocation; admin console uses `html/template` (auto-escaped) |
| A04 Insecure Design | `peer_addr` validated to `http`/`https` schemes only at config load |
| A05 Misconfiguration | Default `health.addr` and `admin.addr` bind to `127.0.0.1` (loopback), not all interfaces |
| A06 Vulnerable Components | Pure-Go dependencies; `go.sum` enforced; `govulncheck` required on dep PRs |
| A08 Data Integrity | `go.sum` verifies all module downloads; config validated at startup |
| A09 Logging | All three watchdog failure modes logged at WARN/ERROR with structured fields |
| A10 SSRF | `peer_addr` scheme validated; peer HTTP responses capped at 1 MiB |

The OWASP AI Top 10 is not applicable — this project contains no AI or ML components.

## Contributing

Pull requests are welcome. Please open an issue first to discuss any significant changes. See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow.

## License

[MIT](LICENSE)

Proudly Made in Nebraska. Go Big Red! 🌽 https://xkcd.com/2347/
