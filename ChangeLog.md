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
