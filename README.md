# Ronin48-NetworkInventoryAgent

A lightweight, autonomous network inventory agent that discovers, catalogs, and reports on devices and assets across your network infrastructure.

## Overview

Ronin48-NetworkInventoryAgent continuously scans your network to build and maintain an up-to-date inventory of all connected devices. It identifies hosts, open ports, running services, operating systems, and hardware details — giving you a living map of your network without requiring manual audits.

## Features

- **Active and passive discovery** — combines active scanning (ping sweeps, port scans) with passive traffic analysis to find devices without flooding the network
- **Asset fingerprinting** — identifies OS, vendor, device type, and running services for each discovered host
- **Continuous monitoring** — detects new devices, removed devices, and configuration changes over time
- **Structured output** — exports inventory data as JSON, CSV, or to a configurable backend (database, SIEM, ticketing system)
- **Low footprint** — designed to run as a background agent with minimal CPU and network impact
- **Alerting** — configurable alerts for unauthorized devices, unexpected open ports, or inventory drift

## Requirements

- Go 1.21+
- Root or `CAP_NET_RAW` capability (for raw socket scanning)
- Network access to the target subnets

## Installation

```bash
git clone https://github.com/Ronin48/Ronin48-NetworkInventoryAgent.git
cd Ronin48-NetworkInventoryAgent
go build -o inventory-agent ./cmd/agent
```

## Usage

```bash
# Run as a continuous background agent
sudo ./inventory-agent --config config.json

# Override the database path at runtime without editing the config
INVENTORY_DB_PATH=/var/lib/inventory/inventory.db sudo ./inventory-agent
```

## Configuration

The agent reads a JSON config file (default: `config.json` in the working directory) and then applies environment variable overrides on top. Environment variables always win, which makes the agent suitable for Docker and Kubernetes deployments without baking secrets into config files.

### Config file

```json
{
  "database": {
    "path": "inventory.db"
  },
  "scanner": {
    "subnets": ["192.168.1.0/24", "10.0.0.0/8"],
    "scan_interval": "5m",
    "timeout": "30s"
  },
  "log": {
    "level": "info",
    "format": "text"
  }
}
```

### Environment variable overrides

| Variable | Overrides | Example |
|----------|-----------|---------|
| `INVENTORY_DB_PATH` | `database.path` | `/var/lib/inventory/db` |
| `INVENTORY_LOG_LEVEL` | `log.level` | `debug` |
| `INVENTORY_LOG_FORMAT` | `log.format` | `json` |

### Config options reference

| Key | Default | Description |
|-----|---------|-------------|
| `database.path` | `inventory.db` | SQLite database file path. Use `:memory:` for tests. |
| `scanner.subnets` | `[]` | CIDR ranges to scan |
| `scanner.scan_interval` | `5m` | How often to re-scan the network |
| `scanner.timeout` | `30s` | Per-host scan timeout |
| `log.level` | `info` | Log verbosity: `debug`, `info`, `warn`, `error` |
| `log.format` | `text` | Log format: `text` (human) or `json` (machine) |

## Output

Each discovered host is recorded with:

- IP address and MAC address
- Hostname (via reverse DNS)
- Open ports and associated services
- OS fingerprint, vendor, and device type
- First seen / last seen timestamps

## Project layout

```
cmd/agent/          Entry point. Additional binaries (server, CLI exporter, etc.)
                    can be added as cmd/<name>/ without restructuring.

internal/config/    Config loading: JSON file merged with environment variable
                    overrides. internal/ so config types never leak to callers
                    outside this module.

internal/store/     Persistence interfaces (HostStore, PortStore, ScanStore) and
                    sentinel errors (ErrNotFound, ErrDuplicate). The rest of the
                    application depends only on these interfaces, never on a
                    concrete database package.

internal/sqlite/    SQLite implementations of the store interfaces. Swapping to
                    Postgres means writing a new internal/postgres/ package; no
                    other code changes.

internal/sqlite/
  migrations/       Versioned SQL files embedded into the binary at compile time.
                    The runner records each applied migration in schema_migrations
                    and wraps each one in a transaction, so a failed migration
                    never leaves the schema in a partial state.

models/             Pure data types shared across the whole codebase. No database
                    imports, no business logic — just structs.
```

## Architecture decisions

These decisions were made at project start to keep the codebase maintainable as it grows. Future contributors should understand the reasoning before changing them.

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

Schema changes live in numbered SQL files (`001_initial.sql`, `002_add_tags.sql`, etc.) that are embedded into the binary at compile time using `//go:embed`. A lightweight runner applies any unapplied migrations in order and records each one in a `schema_migrations` table. Each migration runs inside its own transaction.

**Why:** Keeping migrations in separate files means every schema change is reviewable in git history. Embedding them in the binary means deployments are self-contained — no external migration tool or file to distribute. Transactional application means a failed migration never leaves the schema half-applied.

To add a new migration, create the next numbered file: `internal/sqlite/migrations/002_<description>.sql`. The runner picks it up automatically on next startup.

---

### `context.Context` on every store method

All `HostStore`, `PortStore`, and `ScanStore` methods accept a `context.Context` as their first argument.

**Why:** Context is the Go-idiomatic way to propagate deadlines, cancellation signals, and request-scoped values (such as trace IDs). Adding it later requires changing every call site. Adding it now costs nothing and means the codebase is ready for:
- Per-request database timeouts
- Graceful shutdown (cancel the context, all in-flight queries stop)
- Distributed tracing (attach a span to the context, the store layer participates automatically)

---

### SQLite WAL mode and `busy_timeout`

The database is opened with two pragmas beyond the defaults:

```sql
PRAGMA journal_mode = WAL;
PRAGMA busy_timeout = 5000;
```

**Why:** SQLite's default rollback journal blocks all readers while a write is in progress. WAL (Write-Ahead Logging) allows concurrent reads during a write, which matters once the agent is also serving an HTTP API or exporting data while scanning. `busy_timeout` tells SQLite to wait up to 5 seconds for a lock rather than returning `SQLITE_BUSY` immediately, smoothing over brief write contention between goroutines.

`SetMaxOpenConns(1)` is also set so the driver never opens a second connection — SQLite supports only one writer at a time, and serialising at the driver level is simpler than handling `SQLITE_BUSY` in application code.

---

### Foreign key enforcement

```sql
PRAGMA foreign_keys = ON;
```

**Why:** SQLite does not enforce foreign key constraints by default. Without this pragma, deleting a host would leave orphaned rows in the `ports` table indefinitely. Enabling it ensures referential integrity is maintained at the database level, which is a safety net that remains effective even when application-level delete logic has bugs.

---

### `cmd/` entry point structure

The agent binary lives at `cmd/agent/main.go` rather than a root `main.go`.

**Why:** A root `main.go` implies the repository is a single binary forever. `cmd/<name>/` is the idiomatic Go layout for projects that may grow multiple binaries — for example, a `cmd/server/` that exposes the inventory over HTTP, or a `cmd/export/` that dumps data to a SIEM. Adding a new binary later requires no restructuring.

---

### Structured logging (`log/slog`)

All log output goes through `log/slog` from the Go standard library (Go 1.21+). The format is selectable between `text` (human-readable) and `json` (machine-readable) at runtime.

**Why:** `fmt.Println` and `log.Printf` produce unstructured strings that are difficult to query, alert on, or ingest into log aggregation systems (Elasticsearch, Loki, CloudWatch, etc.). Structured logging with consistent field names means logs are queryable from day one. Using the stdlib package avoids a dependency and ensures any logging framework added later can wrap or replace it cleanly. JSON format is ready for container log drivers and log shippers without any pipeline changes.

---

### Graceful shutdown via `signal.NotifyContext`

`main` creates a context that is cancelled on `SIGINT` or `SIGTERM` and passes it to all long-running operations.

**Why:** A scanner loop that is killed mid-write can corrupt state or leave partial scan records. A context-aware shutdown gives in-flight operations the opportunity to finish cleanly before the process exits. This is essential for any agent that will eventually run under systemd, Kubernetes, or Docker with proper lifecycle management.

---

### SQLite as the database

SQLite was chosen as the initial backing store.

**Why:** For a local network inventory agent, SQLite is the correct default. It requires no server process, no connection string management, no separate installation, and no configuration. The database is a single file that can be copied, backed up, and inspected with standard tooling. It is fully ACID compliant and handles the read/write patterns of a periodic scanner with ease. The repository interface design means that if the project later needs multi-node storage or higher write throughput, the backing store can be replaced without touching any code outside `internal/sqlite`.

---

## Contributing

Pull requests are welcome. Please open an issue first to discuss any significant changes.

## License

[MIT](LICENSE)
