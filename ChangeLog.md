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
