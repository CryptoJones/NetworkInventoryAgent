# ChangeLog

All notable, user-visible changes are recorded here.

The project uses **YY.NN** date-based versioning — `YY` is the two-digit
calendar year and `NN` is a zero-padded incrementing release number within
that year. Versions bump once per merged PR.

Each entry links the recommendation number from
[`Planning.md`](Planning.md) when applicable.

---

## Unreleased

_No unreleased changes._

---

## 26.07 — 2026-05-27

Observability + remaining enrichment + dual-remote parity — items #16, #19,
#28, #29, #31, and #42 from Planning.md.

### Added

- **Prometheus `/metrics` endpoint** (Planning item #19). New
  `internal/metrics` package exposes counters for scans, scan errors,
  hosts/ports upserted, TCP probe success/failure, UDP probe success,
  DB errors, watchdog checks, watchdog failures, peer-down
  transitions, hosts pruned, and on-demand triggers; plus gauges for
  host count and peer-up state. Mounted on the existing health server,
  gated by the same bearer token. Dependency-free implementation — the
  binary footprint is unchanged.
- **Configurable deep TCP port scan** (Planning item #28). New
  `scanner.deep_probe` flag and `scanner.deep_probe_ports` list. When
  enabled, every host confirmed alive by the liveness pass gets a
  second-pass scan across the configured port list. The default list
  is a 34-port "top services" set rather than nmap's top-1000 — the
  former completes in seconds per host, the latter would not fit a
  5-minute scan interval. Each open port is persisted via the existing
  `PortRepo.Upsert`. Disabled by default so existing deployments are
  unaffected.
- **UDP probe stage** (Planning item #29). New `scanner.udp_ports`
  list. Best-effort UDP probing per live host: ports that respond are
  recorded as `state=open udp`; ports the kernel surfaces as ICMP port
  unreachable are recorded as `state=closed udp`; the ambiguous
  "no reply" case is not persisted to avoid filling the table with
  filtered-vs-open noise. Makes the existing `models.UDP` protocol
  type honest. Disabled when the list is empty (the default).
- **MAC + vendor enrichment** (Planning item #31). New
  `scanner.enrich_arp` flag. On Linux the scanner parses
  `/proc/net/arp` after each successful probe and populates
  `Host.MACAddress` and `Host.Vendor` from an embedded OUI prefix
  table (~80 common vendors: Cisco, VMware, Apple, Raspberry Pi,
  MikroTik, Synology, …). Silent no-op on non-Linux platforms and for
  hosts outside the directly attached subnet (the kernel only holds
  neighbour cache entries for hosts it has actually contacted).
- **`-version` flag on every binary** (Planning item #16 prereq).
  `wintermute`, `neuromancer`, `agent`, and `console` now accept
  `-version` and exit 0 with `<name> <revision>`. Revision is read
  from `runtime/debug.ReadBuildInfo()` (vcs.revision when built from a
  checkout) or overridable at link time via
  `-ldflags="-X .../runtime.Version=..."`.
- **Docker smoke test in CI** (Planning item #16). Woodpecker now
  builds the image and runs `docker run --rm <img> -version`, instead
  of the previous `dry_run: true` which compiled and threw the image
  away. Catches GLIBC skew, missing CA-bundle, and entrypoint
  regressions that compile-time checks miss.
- **GitHub mirror parity** (Planning item #42). New `.github/`
  directory:
  - `CODEOWNERS` routes all reviews to `@CryptoJones`.
  - `workflows/ci.yml` mirrors the Woodpecker pipeline (build, vet,
    fmt, test, vuln, lint, docker) on every PR and push to main.
  - `ISSUE_TEMPLATE/{bug_report,feature_request}.md` and
    `PULL_REQUEST_TEMPLATE.md` carry Planning.md cross-reference
    prompts.
  - `CONTRIBUTING.md` gains an "Authoritative remote" section naming
    Codeberg as the canonical home.

### Changed

- **`scanner.New` now takes an `Options` struct.** The positional
  parameter list had grown to nine and was about to grow further with
  this sprint's additions; the struct form keeps each call site readable
  and lets future fields be added without touching unrelated callers.
  See "Notes / breaking changes" below.
- **`internal/scanner` instrumentation.** Probe, host upsert, port
  upsert, and DB-error paths increment the matching `metrics` counters.
  No change to log output or behaviour.
- **`internal/agent` and `internal/watchdog` instrumentation.** Scan
  cycles, triggers, prune counts, watchdog checks/failures/peer-down
  transitions, and host count gauge are all wired through `metrics`.

### Notes / breaking changes

- `scanner.New(hosts, ports, scans, timeout, workers, maxHosts, probePorts)`
  is replaced by `scanner.New(scanner.Options{Hosts: …, Ports: …, …})`.
  Out-of-tree callers need a one-line update; in-tree callers
  (agent.go) are already updated.
- `config.ScannerConfig` gains four optional fields: `deep_probe`,
  `deep_probe_ports`, `udp_ports`, `enrich_arp`. Existing config files
  remain valid.
- `/metrics` is gated by the same bearer token as `/health` and
  `/status`. Loopback-only deployments are unchanged; off-loopback
  deployments scrape with `Authorization: Bearer $INVENTORY_AUTH_TOKEN`.
- Item #26 ("probe is sequential per host") is considered satisfied by
  the parallel-probe change shipped in 26.02 and is not re-listed
  here. Items #13 (peer TLS), #20 (OpenTelemetry tracing), and #41
  (goreleaser/SBOM/cosign) remain outstanding for a future sprint —
  each is substantial enough to warrant its own PR.

---

## 26.06 — 2026-05-26

A second batched pass — items #4, #10, #12, #14, #17, #18, #23, #24, #25,
#27, #30, #32, #33, #34, #35, #36, #37, #38, #39, and #40 from Planning.md.

### Fixed

- **WAL writer/reader pool split** (Planning item #4). `sqlite.DB` now
  opens two `*sql.DB` pools against the same on-disk file: a writer
  pinned at one connection and a reader sized for `2×GOMAXPROCS`.
  Dashboard / watchdog queries no longer queue behind the scanner's
  upserts. `:memory:` paths collapse to one pool because that storage
  is per-connection. Repo structs gained `writer` / `reader` fields and
  dispatch mutations vs. queries accordingly.
- **IPv6 subnet size guard** (Planning item #33). The size check now
  computes `1 << (bits-ones)` up front and refuses before allocating
  the address slice — previously a /64 would have grown the slice to
  2⁶⁴ entries long before the post-allocation check tripped.
- **Global worker semaphore across subnets** (Planning item #34). The
  per-Scan semaphore moved into the `Scanner` struct, so
  `scanner.workers` now caps total concurrent dials across all subnets
  in a cycle. Operators with 20 subnets no longer get `20×workers`
  in-flight probes.

### Added

- **Bearer-token auth for `/health` and `/status`** (Planning item #12).
  When `health.addr` is bound off-loopback the agent refuses to start
  without `health.auth_token` (or `INVENTORY_AUTH_TOKEN`). The
  watchdog peer client takes a matching `peer_token`. Token comparison
  is constant-time; mismatches return 401 with `WWW-Authenticate`.
- **Config-file permission check** (Planning item #18). Boot fails if a
  config containing a bearer token has group/other read permissions.
  Keep them in env vars or `chmod 600` the file.
- **CSRF protection on the admin console** (Planning item #17). A
  per-process random token gates every state-changing method;
  templates carry the value in hidden form inputs so the no-JS flow
  works out of the box.
- **Watchdog peer-status surfacing** (Planning item #10). The watchdog
  publishes its view (`reachable`, drift, staleness, last error) to
  the `health.Tracker`, which exposes it on `GET /status` and at the
  new `GET /watchdog` admin page.
- **`POST /scan` on-demand trigger** (Planning item #23). The admin
  console's dashboard now has a "Trigger Scan" button that pushes onto
  a buffered channel consumed by `agent.Run`; coalesces when a trigger
  is already pending.
- **Host pruning / staleness policy** (Planning item #24). New
  optional `scanner.host_ttl` config. Hosts whose `last_seen` is older
  than this TTL are deleted at the end of each cycle. Disabled by
  default to preserve existing deployments.
- **`/export.json` and `/export.csv`** (Planning item #25) — full host
  + ports snapshot without taking the agent down to copy the SQLite
  file.
- **Configurable probe-port list** (Planning item #27). New
  `scanner.probe_ports []int` config; default unchanged.
- **Reverse-DNS lookup** (Planning item #30). After a successful
  probe, the scanner does a 500 ms PTR lookup and populates `Hostname`.
- **Basic OS fingerprinting** (Planning item #32). Best-effort SSH
  banner read on port 22 and HTTP `Server` header on 80/8080; absent
  on TLS/443 (deferred to a future deep-probe pass).

### Changed

- **`cmd/internal/runtime`** (Planning item #35). The 95%-identical
  `cmd/agent`, `cmd/wintermute`, and `cmd/neuromancer` `main.go` files
  collapsed behind `runtime.Run(opts)` — each binary is now ~10 lines.
- **`internal/admin` split into three files** (Planning item #36):
  `server.go` (router + lifecycle), `handlers.go` (one per page),
  `render.go` (template plumbing + page-data types), `middleware.go`
  (existing middleware + CSRF).
- **`string` funcMap helper removed** (Planning item #37). Templates
  compare `models.Protocol` / `models.PortState` directly since
  `eq` handles the underlying string kind reflectively.
- **TUI uses a cancellable context** (Planning item #38). The console
  binary plumbs a signal-cancelled context into `tui.New`; all
  store loads now respect cancellation instead of using
  `context.Background()`.
- **Docker base images pinned by sha256 digest** (Planning item #14).
  `golang:1.25-bookworm` and `alpine:3.20` now point at concrete
  manifest digests; rebuilds are reproducible and supply-chain
  advisories tie to an exact image.

### Tooling

- **`internal/agent` tests** (Planning item #39). New `agent_test.go`
  covers `Trigger` coalescing, the Healthy flip on `Count()` failure,
  and host pruning with/without TTL.
- **golangci-lint** (Planning item #40). `.golangci.yml` configures
  `errcheck`, `staticcheck`, `govet`, `ineffassign`, `bodyclose`,
  `errorlint`, `gocritic`, and `revive`. CI runs `golangci-lint run
  ./...`; `make lint` does the same locally.

### Notes / breaking changes

- `scanner.New` gained a trailing `probePorts []int` parameter. Pass
  `nil` to keep the historical default.
- `health.NewServer` gained a trailing `authToken string` parameter.
  Pass `""` to disable auth (tests do this).
- `health.NewClient` is preserved as the unauthenticated client;
  use `health.NewAuthedClient(addr, token)` for peers behind auth.
- `watchdog.New` gained a `publish func(health.PeerStatus)` parameter
  alongside the existing `localStatus`. Pass `nil` for tests; pass
  `tracker.SetPeer` in production.
- `admin.NewServer` gained a trailing `trigger admin.Trigger`
  parameter. Pass `nil` to omit `POST /scan` (501 in that case).
- `logging.Setup(cfg, name)` is unchanged from 26.05.
- Off-loopback `health.addr` without an `auth_token` now refuses to
  boot. Docker compose deployments need to set `INVENTORY_AUTH_TOKEN`
  on both agents (with matching values).

---

## 26.05 — 2026-05-26

A batched correctness, observability, and security pass — items #3, #5, #6,
#7, #8, #9, #11, #15, #21, and #22 from Planning.md.

### Fixed

- **`/health` now actually reports unhealthy** (Planning item #3). The
  agent flips `Tracker.Healthy` to `false` when a cycle's DB write fails
  (subnet scan error or host count error), and `/health` additionally
  returns 503 when the most recent scan is older than `3×ScanInterval`.
  Previously `SetHealthy(false)` was never called, so Kubernetes liveness
  probes, the Docker compose healthcheck, and any external monitor
  reported "healthy" even when the agent was wedged.
- **Migrations now race-safe** (Planning item #8). Each pending migration
  upgrades its transaction to `BEGIN IMMEDIATE` and re-checks
  `schema_migrations` after acquiring the write lock, so two agents
  sharing one SQLite file no longer crash on first boot.
- **Standalone agent ships its own config** (Planning item #5).
  `configs/agent.json` and `configs/agent.docker.json` are now in the
  repo, matching what the README and `cmd/agent` `-config` default both
  point at.
- **`start.sh` no longer mutates tracked configs** (Planning item #6).
  Subnet overrides are written to `configs/*.local.json` (already
  gitignored), so re-running with a different subnet leaves a clean
  working tree.

### Changed

- **Admin dashboard surfaces backend failures** (Planning item #9).
  Each of the three queries logs the underlying error and the page
  renders an inline error banner listing which sections were degraded,
  instead of silently showing zeros.
- **Admin server emits one slog record per request** (Planning item
  #21) with method, path, status, duration, and remote address.
- **Admin server sets baseline security headers** on every response
  (Planning item #11): `X-Content-Type-Options: nosniff`,
  `X-Frame-Options: DENY`, `Referrer-Policy: no-referrer`,
  `Permissions-Policy`, and a `Content-Security-Policy` that allows
  inline `<style>` (templates) but blocks scripts/frames/forms by
  default.
- **Scan cycles log their duration** and warn when a cycle uses more
  than half the configured `scan_interval` (Planning item #7), so
  operators see cadence trouble before the ticker starts dropping
  firings.
- **Default slog logger carries an `agent` field** (Planning item #22)
  from `logging.Setup(cfg, name)`, so a combined paired-deployment
  stdout is attributable from the very first line. Callers in
  `cmd/agent`, `cmd/wintermute`, and `cmd/neuromancer` updated.
- **CI runs `govulncheck`** on every PR (Planning item #15) via a new
  Woodpecker step.

### Notes

- `health.NewServer` signature gained a `staleAfter time.Duration`
  parameter. Tests pass `0` to disable the freshness check; binaries
  pass `3*cfg.Scanner.ScanInterval`. Callers outside this repo will
  need a one-line update.
- `logging.Setup` signature gained a `name string` parameter. Pass `""`
  for the previous behaviour.

---

## 26.02 — 2026-05-24

### Fixed

- **Default scanner timeout lowered from 30 s to 2 s** and per-host
  probing is now **concurrent across the 4 probe ports** instead of
  sequential (Planning item #2). Together these change worst-case
  per-host probe time from 4 × timeout ≈ 120 s to ≈ timeout (2 s),
  which keeps a /24 sweep comfortably inside the default 5 min
  `scan_interval`. The previous defaults caused the watchdog's
  freshness check to fire continuously on any network with a
  meaningful fraction of dead hosts.

### Changed

- `scanner.probe` fans out one goroutine per probe port and
  short-circuits the remaining dials via context cancellation as
  soon as the first port answers. Hosts with multiple open probe
  ports may now record any one of them, not deterministically the
  lowest-numbered.
- README `scanner.timeout` row updated to reflect the new default.

---

## 26.01 — 2026-05-24

### Fixed

- **Scanner now persists the open port it discovered** during liveness
  probing (Planning item #1). Previously the `PortRepo` was never written
  to from anywhere in the codebase, so both the web admin console and the
  TUI's "Open Ports" view were always empty in production. Each
  successful probe now writes one `models.Port` row for the answering
  port, keyed on `(host_id, port, tcp)` via the existing upsert.

### Changed

- `scanner.New` and `agent.New` signatures gained a `store.PortStore`
  parameter (immediately after `store.HostStore`). Callers in
  `cmd/agent`, `cmd/wintermute`, and `cmd/neuromancer` updated. A `nil`
  port store is permitted and yields the old liveness-only behaviour.
- `scanner.probe` now returns `(port int, ok bool)` instead of `bool` so
  the calling goroutine knows which port to record.

---

## 26.00 — 2026-05-24

Baseline. `Planning.md` adopted; `ChangeLog.md` introduced. No code changes.
