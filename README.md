# NetworkInventoryAgent

A lightweight, autonomous network inventory agent that discovers, catalogs, and reports on devices and assets across your network infrastructure.

## Overview

NetworkInventoryAgent continuously scans your network to build and maintain an up-to-date inventory of all connected devices. It identifies hosts, open ports, running services, operating systems, and hardware details — giving you a living map of your network without requiring manual audits.

The system is designed to run as **two cooperating agent instances** — named **Wintermute** and **Neuromancer** — that scan the same subnets independently and continuously sanity-check each other. If either agent crashes, stalls, or starts reporting wildly different data, the other detects it and logs a clear warning. This mutual watchdog architecture means the inventory is never silently wrong.

## Features

- **Active discovery** — concurrent TCP-probe scanning across configurable CIDR ranges to find live hosts
- **Asset fingerprinting** — records IP address, open ports, services, OS fingerprint, vendor, and device type per host
- **Continuous monitoring** — periodic re-scans detect new devices, removed devices, and configuration changes over time
- **Mutual watchdog** — two agent instances cross-check each other for liveness, scan freshness, and inventory consistency
- **Web admin console** — dark-themed browser UI with dashboard, host inventory, per-host port detail, and scan history; auto-starts alongside each agent
- **Terminal UI console** — full-featured Bubbletea TUI (`cmd/console`) providing the same views as the web console; connects directly to any agent's SQLite database
- **Structured logging** — human-readable text or machine-readable JSON log output via `log/slog`
- **Graceful shutdown** — SIGINT / SIGTERM cancel in-flight scans cleanly before exit
- **Docker-ready** — single multi-stage image, `docker compose up` starts the full Wintermute/Neuromancer pair
- **Low footprint** — no external server process; the database is a single SQLite file

## Requirements

- Go 1.25+
- Network access to the target subnets

No C toolchain is required. The SQLite driver (`modernc.org/sqlite`) is pure Go.

## Installation

```bash
git clone https://codeberg.org/Ronin48/NetworkInventoryAgent.git
cd NetworkInventoryAgent
go build -o wintermute  ./cmd/wintermute
go build -o neuromancer ./cmd/neuromancer
go build -o console     ./cmd/console
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
| Host inventory | `/hosts` | Full list of all discovered hosts with metadata |
| Host detail | `/hosts/{ip}` | Per-host metadata and open port table |
| Scan history | `/scans` | All subnet sweeps with duration and status |

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
| `scanner.subnets` | `[]` | CIDR ranges to scan |
| `scanner.scan_interval` | `5m` | How often to re-scan the network |
| `scanner.timeout` | `30s` | Per-host TCP probe timeout |
| `scanner.workers` | `50` | Concurrent probe goroutines per subnet scan |
| `scanner.max_hosts` | `65535` | Maximum usable addresses per subnet; larger subnets are rejected |
| `log.level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `log.format` | `text` | Log format: `text` (human) or `json` (machine) |
| `health.addr` | `127.0.0.1:8080` | Address the health HTTP server listens on |
| `admin.addr` | `127.0.0.1:9090` | Address the web admin console listens on |
| `watchdog.peer_addr` | — | Base URL of the partner agent's health server |
| `watchdog.interval` | `30s` | How often the watchdog checks the partner |
| `watchdog.max_host_drift_pct` | `50.0` | Max % host-count difference before a warning |
| `watchdog.max_failures` | `3` | Consecutive liveness failures before declaring peer DOWN |

Duration values in the JSON config accept human-readable strings (`"5m"`, `"30s"`, `"2h"`) in addition to raw nanosecond integers.

### Environment variable overrides

| Variable | Overrides |
|----------|-----------|
| `INVENTORY_DB_PATH` | `database.path` |
| `INVENTORY_LOG_LEVEL` | `log.level` |
| `INVENTORY_LOG_FORMAT` | `log.format` |

## Health endpoints

Both agents expose two HTTP endpoints used by the watchdog and for external monitoring:

| Endpoint | Method | Response |
|----------|--------|----------|
| `/health` | GET | `200 OK` if healthy, `503 Service Unavailable` if not |
| `/status` | GET | JSON-encoded status snapshot (see below) |

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
                  pool (semaphore) to bound parallelism.

  agent/          Periodic scan loop. Drives the scanner across all
                  configured subnets, updates the health Tracker
                  after each cycle with the total DB host count,
                  and blocks until context cancel.

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
