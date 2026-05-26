# Planning — 42 Recommendations for NetworkInventoryAgent

> Author: Principal Software Architect review
> Reviewed commit: `7e2cd1e` (main)
> Scope: full repository — `cmd/`, `internal/`, `configs/`, `models/`, CI, Docker, docs

The project is well structured for its size: clean repository interfaces,
embedded migrations, structured logging, sensible defaults, and a coherent
two-agent mutual-watchdog architecture. The recommendations below are ordered
by **severity / leverage**, not by file. Items 1–10 are correctness or design
defects; 11–25 are platform/operability gaps; 26–42 are feature, polish, and
process improvements.

Each item lists: **What → Why → Where**.

---

## Critical correctness defects

### 1. Scanner discovers ports but never persists them
The scanner probes hosts on ports 22/80/443/8080 and confirms liveness — but
the successful port observation is thrown away. Only `hosts.Upsert` is called;
`PortRepo.Upsert` is **never invoked anywhere in the codebase**. The "Ports"
view in both the web admin console and the TUI will therefore always be empty
in production.
**Why it matters:** the central value proposition of the product (port
inventory) is non-functional. Every other recommendation below is downstream
of this fact.
**Where:** `internal/scanner/scanner.go:83-100` — the goroutine has the
`addr` and the matching `port`/`probePorts[i]` in scope and should write a
`models.Port` for each open socket.

### 2. Default scanner timeout makes one /24 sweep exceed the scan interval
`config.Default()` sets `scanner.timeout = 30s`. `probe()` tries 4 ports
**sequentially**, so a single dead host can consume 120 s. With the default
50 workers on a /24 of mostly-dead hosts, a sweep can easily exceed 10 min —
longer than the 5 min default `scan_interval`, which means the watchdog's
freshness check will fire continuously.
**Why it matters:** the agents will permanently warn each other as "stale"
out of the box.
**Where:** `internal/config/config.go:108-113` (default) vs.
`configs/wintermute.json` (which already uses `"2s"`). Either lower the
default to ~2 s, parallelise the probe, or short-circuit on first success
with a tighter overall budget.

### 3. `/health` always returns 200 — the Tracker's `Healthy` is never flipped
`SetHealthy(false)` is never called from anywhere in the codebase. The
watchdog logs DOWN peers but does not affect the local `/health` response,
and the scan loop does not mark itself unhealthy on repeated DB failures.
**Why it matters:** Kubernetes liveness probes, the Docker compose
healthcheck, and any external monitor will report "healthy" even when the
agent is wedged.
**Where:** `internal/health/status.go:59`, `internal/agent/agent.go:66-89`,
`internal/watchdog/watchdog.go:80-109`. Define explicit health rules
(e.g. "DB write failed in last cycle" or "no scan in 3× interval") and
update the tracker.

### 4. `SetMaxOpenConns(1)` defeats the WAL configuration
The DB sets `journal_mode=WAL` "to allow concurrent reads during writes",
then immediately caps the pool at 1 connection — so the driver can never
issue a read in parallel with a write. The admin console's dashboard issues
three sequential queries that all serialise behind the scanner.
**Why it matters:** under load, dashboard requests block behind ongoing
upserts. WAL was correctly chosen for the workload but is currently
unreachable.
**Where:** `internal/sqlite/db.go:41`. Open two pools (one writer pinned at
1, one reader at N) or accept WAL's BUSY semantics and remove the comment
about "concurrent reads during writes".

### 5. `start.sh` references `configs/agent.json` that does not exist in the repo
Standalone mode loads `configs/agent.json`; the script falls back to copying
`wintermute.json` if missing, but the README and the agent binary's
`-config` default both point to a file that does not ship.
**Why it matters:** a new operator running `./agent -config configs/agent.json`
sees an immediate file-not-found.
**Where:** ship `configs/agent.json` and `configs/agent.docker.json` (the
latter is also referenced by docs but absent). Remove the runtime mutation
in `start.sh:155-164` once the file exists.

### 6. `start.sh` mutates the user's tracked config files in-place
The script overwrites `configs/wintermute.json`, `configs/neuromancer.json`,
and `configs/agent.json` whenever `-s/--subnet` is passed. These are
git-tracked. A user who runs the script twice with different subnets has
unrelated diffs in their working tree forever.
**Why it matters:** destructive side effect on tracked files breaks the
"safe to re-run" expectation for an `info`-level command.
**Where:** `start.sh:139-165`. Write the rendered config to
`~/.config/network-inventory-agent/` or `./.local/` (already gitignored)
and pass that path to `-config`.

### 7. Race-free but unbounded: scan cycles can overlap silently if a cycle exceeds the interval
The `time.Ticker` will drop ticks if `runCycle` exceeds the interval, but
no skipped-cycle accounting is logged and no metric is incremented. Because
the scanner serialises subnets, large multi-subnet configs will silently
fall behind.
**Why it matters:** operator believes the agent is on a 5-min cadence
while it is actually on a 50-min cadence.
**Where:** `internal/agent/agent.go:46-64`. Track `cycle_duration` per
subnet, log a warning when `duration > interval/2`, and consider scanning
subnets concurrently with a fan-out goroutine pool.

### 8. Migrations are not lock-protected against two agents racing on first boot
`migrations.Run` checks `schema_migrations`, then inserts. If Wintermute and
Neuromancer share a database path (which is permitted by config), they
race. SQLite's `IMMEDIATE` transaction would serialise this; the current
default-deferred transaction will not.
**Why it matters:** crash-on-startup the first time a new migration ships
in a shared-DB deployment.
**Where:** `internal/sqlite/migrations/migrate.go:70`. Use
`BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})` or issue
`BEGIN IMMEDIATE` explicitly. (Even when each agent has its own DB the
shared SQLite file is plausible for backup/inspection workflows.)

### 9. Admin dashboard swallows all errors
`handleDashboard` runs three queries and discards every error: `if n, err := … err == nil`.
**Why it matters:** silently shows empty cards instead of surfacing
backend failure. Diagnostics rely on operators noticing missing data.
**Where:** `internal/admin/server.go:174-188`. Either render an empty
state with an inline error banner, or return 500 with a Retry-After.

### 10. The watchdog never reports its findings to anyone but the log file
DOWN, stale, and drift are written to stdout via `slog.Warn`/`Error` and
otherwise lost. There is no `/peer_status` endpoint, no Prometheus metric,
no panel in the admin console. Operators only see them by tailing logs.
**Why it matters:** the "mutual watchdog" feature is the project's
headline differentiator, yet its output is unindexed text.
**Where:** add a peer-status block to `health.Tracker`, expose it via
`/status` and a new admin page (`/watchdog`).

---

## Security and hardening

### 11. Admin console serves no security headers
No `Content-Security-Policy`, `X-Content-Type-Options`, `Referrer-Policy`,
`X-Frame-Options`, or `Permissions-Policy`. The pages embed inline CSS which
makes a strict CSP non-trivial but feasible (use a hash or nonce).
**Where:** `internal/admin/server.go:241-247` `render()` — set headers
before `ExecuteTemplate`, or wrap the mux in a middleware.

### 12. `/health` and `/status` leak inventory size to anyone on the network when bound off-loopback
`Status` exposes `HostCount`, `ScanCount`, `LastScanAt`. Operators who flip
the bind to `0.0.0.0` for Docker have no way to gate access. Add a
token-based auth (shared bearer in env var) and document that any non-loopback
bind requires it.
**Where:** `internal/health/server.go` + a new `auth.Authn` middleware.

### 13. Peer authenticity is unverified
The watchdog believes any host that answers on `peer_addr` and returns a
matching JSON shape. A malicious host on the network path can spoof
status, masking a real DOWN. The SECURITY.md already lists "TLS planned" —
implement it with a shared CA cert pinned in config.

### 14. Dependency versions are not pinned to digests in Docker
`FROM golang:1.25-bookworm` and `FROM alpine:3.20` float. Pin both by
sha256 digest and update via Renovate/Dependabot.
**Where:** `Dockerfile:3,17`.

### 15. CI does not run `govulncheck`
`make vuln` exists but `.woodpecker.yml` never invokes it. The
contributing guide *requires* it for dependency PRs but enforcement is
manual.
**Where:** `.woodpecker.yml` — add a `vuln` step that runs `go install
golang.org/x/vuln/cmd/govulncheck@latest && govulncheck ./...`.

### 16. CI does not actually publish or even smoke-test the Docker image
The `docker` step has `dry_run: true` and no `when: tag` trigger. There is
no path from a green CI to a usable image.
**Where:** `.woodpecker.yml:22-28`. Add a tag-triggered push to a
registry, and a `docker run … --version` smoke test on every PR.

### 17. The admin server is open to CSRF / clickjacking should write endpoints ever be added
Today the console is read-only. The moment a "Delete host" button is
introduced, CSRF protection becomes a P0. Establish a CSRF middleware now
(even if it currently no-ops on GETs) so the precedent is set.

### 18. Config files have no `umask`/permission check at load time
`SECURITY.md` advises `chmod 600`, but the agent will happily read a 0644
file. Refuse to start if `cfg.Watchdog.PeerAddr != ""` and the file is
world-readable (or warn loudly).
**Where:** `internal/config/config.go:138-158`.

---

## Observability and operations

### 19. No metrics endpoint
A Prometheus `/metrics` endpoint with counters for scans-completed, hosts
upserted, probe-success / probe-failure, DB errors, and watchdog events
would replace 80 % of ad-hoc grep-the-log debugging. Use the
`expvar`-compatible `promhttp` handler so the new dep is small.
**Where:** new package `internal/metrics`, mounted on the health server.

### 20. No tracing
For a system that does HTTP-out (watchdog), HTTP-in (admin + health), and
DB I/O, OpenTelemetry tracing is cheap to wire and pays off the first time
something is slow. Add `otelhttp` wrappers.

### 21. Per-request logging is missing on the admin server
There is no access log. A request that fails at template rendering is
visible; one that returns 200 with stale data is not. Wrap the mux in
`logging.HTTP(next)` middleware producing one slog record per request.

### 22. Slog has no contextual fields set globally
Each agent calls `slog.With("agent", name)` only inside loops. The
top-level `slog.Default()` has no agent name, so the early "config
loaded", "DB opened", and "admin server started" lines are
indistinguishable between Wintermute and Neuromancer in a combined log
stream.
**Where:** `internal/logging/logging.go:32` — accept a `name` and call
`slog.SetDefault(slog.New(handler).With("agent", name))`.

### 23. No on-demand scan trigger
Operators cannot force a scan after a network change without restarting
the agent. Add `POST /scan` (loopback only or auth-gated) that pushes onto
a channel consumed by `agent.Run`. Useful for testing too.

### 24. No host-pruning / staleness policy
The schema has `last_seen` but nothing ever uses it. Hosts decommissioned
years ago accumulate forever, polluting the drift calculation. Add an
optional `scanner.host_ttl` config — hosts not seen for N×scan_interval
get marked stale (new column) or deleted.

### 25. No backup or export mechanism
The SQLite file can be copied — but only when no agent is running. Provide
`GET /export.csv` and `GET /export.json` (loopback) so users can pull
inventory snapshots without stopping the agent.

---

## Scanner quality and feature gaps

### 26. Probe is sequential per host
`probe()` tries 4 ports one after another. Parallelise across the 4 ports
with `errgroup` and return on first success — typical wall-clock for a
live host drops from ~timeout to ~timeout/4.
**Where:** `internal/scanner/scanner.go:111-121`.

### 27. Probe port list is hard-coded
`var probePorts = []string{"22", "80", "443", "8080"}` excludes RDP (3389),
SMB (445), DNS (53), SNMP (161), printers (9100), IoT (1883/8883), etc.
Move to `ScannerConfig.ProbePorts []int` with the current list as
default.

### 28. No real port scan, only liveness probe
The system records hosts but never enumerates the open ports per host.
Add a configurable second pass (e.g. nmap-style top-1000) that runs after
liveness for each found host. Gate it behind `scanner.deep_probe = true`.

### 29. No UDP scanning, despite `models.Protocol` allowing it
Either remove UDP from the model (YAGNI) or add a UDP probe stage. Right
now the type is dishonest.

### 30. No reverse-DNS lookup
`Host.Hostname` is part of the schema and the UI but nothing ever
populates it. A `net.DefaultResolver.LookupAddr(ctx, ip)` call after a
successful probe fills it for free.

### 31. No MAC/vendor enrichment for local subnets
For hosts on a directly attached subnet, ARP table lookups
(`/proc/net/arp` on Linux, `netlink`, or the `arp` syscall) populate
`MACAddress`, and an embedded OUI table populates `Vendor`. Both fields
are currently always empty.

### 32. No OS fingerprinting
`OSFingerprint` is shown in the UI but never populated. Even a coarse
banner-grab on the open port (e.g. SSH version string, HTTP `Server`
header) would be a useful improvement.

### 33. IPv6 subnets are not safely bounded
`max_hosts = 65535` is a /48 IPv4 mistake-prevention but a /112 IPv6
subnet is 65 536 addresses. Allowing IPv6 CIDR through `net.ParseCIDR`
without an aggressive prefix-length floor risks an accidental /64 enumerate
that allocates 2⁶⁴ addresses long before the `max_hosts` check trips
(the slice grows linearly inside `usableIPs`).
**Where:** `internal/scanner/scanner.go:126-138`. Compute `1<<(bits-ones)`
first and refuse early if it exceeds `maxHosts`.

### 34. No per-subnet concurrency
Each call to `Scan` allocates its own semaphore — there is no global cap
across subnets. An operator with 20 /24s gets `20 × workers` parallel
dials, dwarfing the documented `workers` setting.
**Where:** `internal/scanner/scanner.go:73`. Move the semaphore to the
`Scanner` struct (constructed once).

---

## Code structure and maintainability

### 35. `cmd/agent`, `cmd/wintermute`, `cmd/neuromancer` are 95 % duplicated
The three `main.go` files differ only in agent name, default config path,
and whether they construct a watchdog. Extract a `cmd/internal/runtime`
package that exposes `Run(name, configPath, withWatchdog bool)` and have
each binary call it.
**Where:** `cmd/agent/main.go`, `cmd/wintermute/main.go`, `cmd/neuromancer/main.go`.

### 36. `internal/admin/server.go` mixes HTTP plumbing with page-data assembly
Once a 5th page is added the handler list will be unwieldy. Split into
`server.go` (router + lifecycle), `handlers.go` (one per page),
`render.go` (template plumbing). Small refactor, big readability win.

### 37. The `template.FuncMap` `string` helper is footgunny
`"string": func(v interface{}) string { return fmt.Sprintf("%s", v) }` is
used in `host.html` for protocol/state comparison. The same effect is
achieved by exposing the `models.Protocol`/`models.PortState` as their
underlying string type, or by branching in Go. Remove the helper.

### 38. TUI uses `context.Background()` for all loads
A blocked DB query has no cancellation path. Plumb a cancellable context
from the bubbletea program, cancel it in `Update` on quit, and on view
switch.
**Where:** `cmd/console/tui/tui.go:329-373`.

### 39. Tests do not cover `internal/agent`, `internal/logging`, or the TUI
The presence of `agent.go` without `agent_test.go` is the single largest
coverage gap. Add tests with an in-memory store and an injected clock
(extract `time.Now` behind a `func() time.Time` field).

### 40. No linter beyond `gofmt` + `go vet`
Adopt `golangci-lint` with a minimal config (`errcheck`, `gosimple`,
`govet`, `ineffassign`, `staticcheck`, `unused`, `gocritic`, `revive`,
`errorlint`, `bodyclose`). Wire it into CI and the Makefile.

---

## Process, release, and documentation

### 41. No release artefacts, no SBOM, no signed binaries
Ship per-commit `goreleaser` builds for linux/darwin/windows × amd64/arm64,
generate `cyclonedx` SBOMs, and sign with `cosign`. This unlocks
"download a binary" as an install option (currently the only path is
`git clone && go build`).

### 42. GitHub mirror parity & CODEOWNERS
The README links Codeberg only; the `dual-remote-pr` skill in this
environment suggests a GitHub mirror exists or is planned. Add:
- `.github/CODEOWNERS` (so reviews route automatically)
- `.github/workflows/ci.yml` that mirrors `.woodpecker.yml`
- A note in `CONTRIBUTING.md` explaining which remote is authoritative
- Identical issue/PR templates on both forges

Without parity, contributors who land on GitHub get a different
experience from those on Codeberg, and the project's "open" surface area
is unclear.

---

## Suggested execution order

A reasonable first sprint:

1. **#1** (persist ports) — restores the headline feature.
2. **#2** (default timeout) — prevents the freshness alarm from firing.
3. **#3** (real `/health` semantics) — unblocks meaningful orchestration.
4. **#5 + #6** (`configs/agent.json` + stop mutating tracked files) — fixes
   the new-user happy path in under an hour.
5. **#9** (admin error surfacing) and **#21** (access log) — buy
   observability cheaply.
6. **#15** + **#40** (govulncheck + golangci-lint in CI) — locks the
   quality floor before further feature work.

Once that floor is in, **#19** (metrics), **#28** (deep port scan), and
**#30–#32** (enrichment) are the high-leverage feature investments.

---

*End of recommendations.*
