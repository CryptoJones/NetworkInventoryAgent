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

## 26.16 — 2026-05-27

Listener-address environment variable overrides. Containerised
deployments can now repoint the health + admin listeners without
rewriting the JSON config file.

### Added

- **`INVENTORY_HEALTH_ADDR`** — overrides `health.addr`.
- **`INVENTORY_ADMIN_ADDR`** — overrides `admin.addr`.

Existing config validation still applies: an off-loopback bind
without a corresponding `INVENTORY_AUTH_TOKEN` is refused at boot,
regardless of which surface (file or env) supplied the address.

### Fixed

- **README env-var table now lists `INVENTORY_AUTH_TOKEN` and
  `INVENTORY_PEER_TOKEN`**, which the code has supported since 26.06
  but the docs never advertised.

---

## 26.15 — 2026-05-27

Per-subnet scan profiles (P2-05). Operators can now run aggressive
hourly deep scans on critical infrastructure while leaving the guest
network on a lazy daily liveness sweep — all from one config file, one
agent process. Closes the original P2 operator-feedback batch.

### Added

- **`config.SubnetProfile`** — per-subnet overrides for `ScanInterval`,
  `Timeout`, `ProbePorts`, `DeepProbe`, `DeepProbePorts`, `UDPPorts`,
  `EnrichARP`. Bool fields are `*bool` so a profile can explicitly
  disable deep probing even when the global default is on (the zero
  value would be ambiguous).
- **`config.ScannerConfig.Profiles []SubnetProfile`** — the new
  per-subnet list. Mutually exclusive with the legacy `Subnets`
  field; both are validated at boot.
- **`config.ResolvedProfile` + `ScannerConfig.Resolve()`** — flattens
  Subnets + Profiles into one fully-defaulted list. Every field is
  populated from either the profile override or the global default,
  so the agent's runtime path has no further fallback logic.
- **`scanner.SubnetOptions`** — new struct passed to `Scan(ctx,
  subnet, opts)`. Carries the per-call probe configuration so the
  scanner can serve multiple profiles without per-scan reconstruction.
- **`config.True()` / `config.False()`** — bool-pointer helpers for
  building profiles programmatically.

### Changed

- **`scanner.Scan(ctx, subnet)` → `scanner.Scan(ctx, subnet, SubnetOptions{})`**.
  In-tree callers updated; out-of-tree callers pass `SubnetOptions{}`
  to retain pre-26.15 behaviour.
- **`scanner.probe`, `deepScan`, `udpScan` helpers** now take their
  timeout + port-list parameters explicitly rather than reading from
  the Scanner struct. The Scanner-level fields remain as defaults
  consulted by `resolve()` at the top of Scan.
- **`agent.New` now returns `(*Agent, error)`** — `Resolve()` runs at
  construction so config errors (duplicate subnets, mutually-exclusive
  flat list + profiles) surface at boot, not on the first scan tick.
  Test suites updated.
- **Agent scheduling**: the single global `ScanInterval` ticker is
  replaced with one that ticks at the *shortest* per-profile
  interval. Each profile keeps its own `nextDue` timestamp; only
  profiles past their due time get scanned on each tick. The
  housekeeping pass (prune, change-detect diff, tracker updates) runs
  every tick regardless, so zero-profile deployments — watchdog-only
  mode — still work as before.
- **Tick-interval safety floor** of 1 second. Prevents pathological
  busy-loops if an operator types `"1ns"` by accident.

### Tests

- 7 new tests in `internal/config` covering: legacy-Subnets path,
  profile-overrides-win, explicit-False-beats-global-True, mutually-
  exclusive validation, duplicate-subnet rejection, empty-subnet
  rejection, zero-profile happy path.
- Scanner + agent test suites updated for the new `SubnetOptions{}`
  third argument and `(a, err)` constructor.

### Migration notes

Existing configs keep working — set `scanner.subnets` and the global
fields (`scan_interval`, `timeout`, `probe_ports`, …) as before.
**Operators who want per-subnet tuning** switch to:

```json
{
  "scanner": {
    "profiles": [
      { "subnet": "10.0.0.0/24", "scan_interval": "1h", "deep_probe": true },
      { "subnet": "192.168.1.0/24", "scan_interval": "24h" }
    ],
    "scan_interval": "5m",
    "timeout": "2s",
    "workers": 50
  }
}
```

Any field absent from a profile inherits the corresponding global.
The flat `scanner.subnets` and per-subnet `scanner.profiles` fields
are mutually exclusive — boot fails fast if both are set.

---

## P2 operator-feedback batch — complete

Original asks from the operator pass: **all five shipped.**

| # | Item | Sprint |
|---|---|---|
| 1 | Service / application discovery | 26.12 |
| 2 | Change detection + webhook/syslog alerts | 26.13 |
| 3 | Device-type classifier | 26.11 |
| 4 | Query API beyond bulk export | 26.14 |
| 5 | Per-subnet scan profiles | 26.15 |

The agent now does end-to-end inventory: discovery → enrichment →
classification → change detection → alerting → queryable API, with
per-subnet scheduling. Next-feature backlog is empty; future work
should be driven by a fresh round of operator feedback or `/ultrareview`
findings.

---

## 26.14 — 2026-05-27

JSON query API (P2-04). Adds filterable, paginated `/api/v1/hosts` and
`/api/v1/hosts/{ip}` endpoints alongside the existing bulk-export
endpoints. Operators piping inventory into a CMDB / ticketing webhook /
monitoring stack no longer have to download the full snapshot and grep.

### Added

- **`GET /api/v1/hosts`** — list hosts as JSON. Query parameters
  (all optional, all AND together):
  - `vendor` — exact match on `Host.Vendor`
  - `device_type` — exact match on `Host.DeviceType`
  - `hostname` — case-insensitive substring match
  - `subnet` — CIDR; host IP must be inside it
  - `port` — integer; host must have that TCP port open
  - `limit` — page size (default 100, capped at 1000)
  - `offset` — zero-based offset (default 0)

  Response envelope:
  ```json
  {
    "total":  42,    // total matching the filter, before pagination
    "limit":  100,
    "offset": 0,
    "hosts":  [ {…full Host JSON…}, … ]
  }
  ```

- **`GET /api/v1/hosts/{ip}`** — single-host detail. Returns
  `{ "host": {…}, "ports": [ {…}, … ] }` so consumers don't need a
  second round-trip for the ports table.

- **`internal/admin/api.go`** — separated from the HTML-template
  handlers in `handlers.go` so the two concerns can evolve
  independently. New `internal/admin/api_test.go` covers every filter
  dimension, combined filters, pagination edges, invalid input → 400,
  unknown host → 404.

### Notes

- API errors return `{"error": "..."}` with appropriate 4xx/5xx codes.
- Pagination is offset-based for simplicity. Cursor-based pagination
  is the right choice past ~10k hosts; that's a future cleanup if
  someone hits the scale.
- The endpoints live under `/api/v1/` so future v2 changes can land
  alongside without breaking consumers.
- Filtering happens in Go after a `List()` from the host store. Fine
  for the inventory sizes this agent targets (LAN-scale, hundreds to
  low-thousands of hosts). Move to SQL `WHERE` clauses if needed.
- The API is on the same admin server port (default 9090, loopback
  bind by default). Off-loopback access is the operator's call —
  same posture as the existing `/export.json` endpoint.

---

## 26.13 — 2026-05-27

Change detection + alert sinks (P2-02). The agent now diffs the host
inventory pre- and post-cycle and emits `host.discovered` /
`host.vanished` events to a configurable HTTP webhook, syslog, or both.
Operators no longer have to grep logs to notice a new device appeared.

### Added

- **`internal/alerts` package** — `Event` (JSON-tagged for wire reuse),
  `EventType` (`host.discovered`, `host.vanished`), `Emitter`
  interface, `Multiplexer` that fans events out to N sinks in
  parallel, `NoopEmitter` for the alerts-disabled deployment.
- **WebhookSink** — HTTP POST JSON. One retry on transient failures
  (network error or 5xx); 4xx is final. Optional `Authorization`
  header passed verbatim (so `Bearer …` and `Basic …` both work
  without per-scheme code).
- **SyslogSink** — RFC 5424 over UDP/TCP. Hand-rolled because
  stdlib `log/syslog` is Unix-only and the agent runs on Windows.
  Message body is the same JSON as the webhook payload, so syslog
  parsers (rsyslog mmjsonparse, syslog-ng, Splunk) get structured
  fields for free.
- **`config.AlertsConfig`** — new optional top-level section:
  ```json
  "alerts": {
    "webhook": { "url": "https://hooks.example/x", "auth_header": "Bearer …" },
    "syslog":  { "addr": "udp://syslog.example:514", "tag": "inventory" }
  }
  ```
  Either or both sub-sections may be set; absence of both silently
  disables alerting (no change for existing deployments).

### Changed

- **`agent.New` gained an `alerts.Emitter` parameter** (8th positional
  arg). Pass `nil` for the noop emitter; the constructor substitutes
  one automatically so tests stay terse.
- **`agent.runCycle` snapshots the host inventory** before scanning
  and again after, then diffs the two by IP. Discovered hosts get the
  fresh enrichment (Hostname/Vendor/DeviceType); vanished hosts get
  the last-known pre-cycle row (since the post-cycle one is gone).
  Cycle-failed runs intentionally skip the diff to avoid alert spam
  on transient DB blips.
- **`mockHostStore.Upsert`** in the agent test suite now mirrors the
  sqlite UPSERT (overwrite-on-conflict) so test stores stay parity
  with production — same fix landed in the scanner mock during 26.11.

### Tests

- 11 new tests covering: multiplexer fan-out, sibling sinks surviving
  a peer-sink error, webhook auth-header round-trip, 5xx retry, 4xx
  no-retry, nil-on-empty-URL/addr guards, syslog RFC 5424 format
  against a real UDP listener (PRI calculation, JSON MSG, MSGID =
  event type), bad-scheme rejection, agent-level diff producing
  `host.vanished` on prune and `host.discovered` on a mid-cycle insert.

### Notes

- Sinks deliver in goroutines. Multiplexer.Emit returns immediately;
  failures show up in slog warnings, not back at the call site. This
  matches operator expectations for alert pipelines and keeps the
  scan-cycle hot path off the network.
- Port-level events (`port.appeared` / `port.vanished`) and watchdog
  events are deliberately deferred — host-level coverage satisfies the
  headline operator ask first.

---

## 26.12 — 2026-05-27

Service / application discovery (P2-01). Turns "port 22 is open" into
"SSH-2.0-OpenSSH_9.6p1 Ubuntu", and similar for nine more protocols.
Banners now flow into `Port.Service` for every persisted port, in addition
to the existing `Host.OSFingerprint` for the liveness winner.

### Added

- **`internal/scanner/banner.go`** — three new banner-grab strategies:
  - `lineBanner` for protocols where the server greets first (SMTP
    25/465/587, FTP 21, POP3 110, IMAP 143, Telnet 23). Bounded
    `capReader` defends against peers that flood without an EOL.
  - `tlsHTTPSFingerprint` for HTTPS (443/8443). Completes a TLS
    handshake (InsecureSkipVerify — we're scraping for ID, not
    trusting), peeks at the peer cert CN/SAN, then reuses the same
    connection for a HEAD to capture the Server header. Two IDs for
    the cost of one dial.
  - `mysqlGreeting` for MySQL/MariaDB/Percona (3306). Reads the v10
    handshake packet and extracts the server-version string. Passive
    — we never write to the socket.
- **HTTP port list expanded** to include 8000 and 8888 (common
  developer-server defaults).
- **`Port.Service` populated by the scanner** for every TCP port
  upserted by the liveness, deep-probe, and HTTPS-fingerprint paths.
  The column has existed in the schema since the initial migration
  but was never written until now.
- **`internal/scanner/banner_test.go`** — full coverage of every
  banner: SMTP greeting parse, FTP greeting parse, silent-server
  timeout (must return ""), valid v10 MySQL handshake parse, wrong
  protover MySQL guard, HTTPS handshake with cert CN extraction +
  Server header pickup via a `httptest.NewUnstartedServer`.

### Changed

- **`scanner.upsertPort` gained a `service string` parameter.** The
  three existing call sites (liveness, deepScan, udpScan) pass the
  result of `fingerprint()` for TCP and `""` for UDP. UDP banner
  probes are protocol-specific and out of scope for this sprint.
- **`fingerprint()` dispatch table grew** from 4 entries to 12 — see
  the function comment for the full list.

### Notes

- The liveness path no longer redials for its banner: `host.OSFingerprint`
  was already populated by `fingerprint()` before the port upsert, so
  the same string is reused as the liveness-port `Service`.
- The deepScan path *does* redial inside `fingerprint()` per open
  port. The first dial in `deepScan` was a connect-and-close to
  confirm liveness; protocols where the server speaks first need a
  fresh socket so the read deadline starts cleanly. The cost is one
  extra dial per deep-open port per cycle — well within the global
  worker semaphore.
- UDP services (DNS 53, SNMP 161, NTP 123, …) are not banner-grabbed
  yet. Each requires a protocol-specific request packet rather than a
  passive listen; deferred to a future sprint if asked.

---

## 26.11 — 2026-05-27

Device-type classifier (P2-03 from the operator-feedback queue). Populates
`Host.DeviceType` automatically based on the OUI vendor, OS-fingerprint
banner, and the open TCP + UDP ports found during the scan. The admin
console templates already had the field plumbed; this sprint just gives
them data to show.

### Added

- **`internal/scanner/classify.go`** — pure heuristic function with
  rules for printers (9100/631/515), routers (MikroTik + vendor-pinned
  Cisco), hypervisors (VMware + 902/5988/5989), databases (MySQL,
  Postgres, MSSQL, MongoDB, Redis, Memcached), Windows hosts/servers
  (SMB ± HTTP), Active Directory domain controllers (Kerberos+LDAP),
  mail servers (SMTP+IMAP combo), DNS servers, MQTT brokers, embedded
  systems (Raspberry Pi OUI), Linux hosts (SSH banner), and generic
  web appliances. Conservative: returns "" rather than guessing when
  no rule fires confidently.
- **`internal/scanner/classify_test.go`** — 27 table-driven cases
  covering every rule above, including overlapping cases (DC beats
  generic SMB, SMB+webserver beats SMB alone).
- **`scanner.Scan` integration test** — confirms a host listening on
  11211 (memcached) gets `DeviceType = "database (memcached)"` after a
  real scan, exercising the full classify → re-upsert path.

### Changed

- **`scanner.deepScan` and `scanner.udpScan`** now return the list of
  open ports they found (in addition to upserting them) so the per-
  host goroutine can pass the complete port set to `classify()`. No
  behavioural change for callers that ignore the return value.
- **Per-host scan path** does a second `hosts.Upsert` when the
  classifier produces a non-empty device-type. The cost is one extra
  small SQL write per live host per cycle.

### Fixed

- **`mockHostStore.Upsert` now mirrors the sqlite UPSERT** — it
  previously returned the existing row unchanged on conflict, which
  meant the scanner's "first upsert without device_type, then
  re-upsert with device_type" path tested green against the mock but
  would have lost the device_type write against any real store. Mock
  parity caught the bug before it shipped.

---

## 26.10 — 2026-05-27

Container distribution. Adds multi-arch Docker images on the GitHub
Container Registry alongside the existing binary archives, with the same
cosign keyless OIDC signing flow extended to the image manifests.

### Added

- **`ghcr.io/cryptojones/networkinventoryagent:<version>`** — multi-arch
  manifest covering linux/amd64 + linux/arm64. `:latest` resolves to
  the same image as the most recent `vYY.NN` tag. Default entrypoint is
  `agent`; `wintermute` and `neuromancer` are present in the same image
  and reachable via `--entrypoint /usr/local/bin/wintermute` etc.
- **`Dockerfile.goreleaser`** — slim COPY-only Dockerfile consumed by
  goreleaser. Pre-built binaries are copied in rather than recompiled
  per arch (the host-level goreleaser build matrix already produced
  them). The existing top-level `Dockerfile` is unchanged so
  `docker build .` from a checkout still works.
- **`cosign` signing on the published manifests.** Verify with:
  ```
  cosign verify ghcr.io/cryptojones/networkinventoryagent:26.10 \
    --certificate-identity-regexp 'https://github.com/CryptoJones/NetworkInventoryAgent/' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com
  ```

### Changed

- **`.github/workflows/release.yml`** now sets up QEMU + Docker Buildx
  and logs in to ghcr.io before invoking goreleaser, so the cross-arch
  `linux/arm64` layer builds succeed on the amd64-only GitHub runner.
- **README** gains a Docker section with the `docker pull` quickstart
  and the `cosign verify` snippet.

### Notes

- The image's default entrypoint is `agent` (standalone single-agent
  binary). For the Wintermute/Neuromancer pair, the existing
  `docker-compose.yml` still applies — point its `image:` at
  `ghcr.io/cryptojones/networkinventoryagent:26.10` and you skip the
  `docker build` step.
- Pulls are unauthenticated for public images; `docker login ghcr.io`
  is only needed if a future release flips visibility to private.

---

## 26.09 — 2026-05-27

Post-Planning.md tightening pass — clears the lint debt that 26.06's
`golangci-lint` integration (item #40) catches but never fixed, and
pre-empts the September 2026 Node.js 20 → 24 transition on GitHub
Actions before it bites.

### Tooling

- **`golangci-lint run ./...` now passes clean.** The 21 baseline
  findings (15× errcheck on `Close`/`Body.Close`, 4× staticcheck
  QF1008/QF1012, 2× gocritic ifElseChain/exitAfterDefer) are all
  resolved — either by explicit `_ =` discards on Close calls in defer
  position (the Go idiom for "we don't care about this error") or by
  rewriting the offending pattern. `make lint` is now meaningfully
  enforceable.
- **`cmd/console` `os.Exit` no longer skips deferred cleanup**
  (gocritic exitAfterDefer). The real entry point moved into a `run()
  int` helper and `main` calls `os.Exit(run())`, matching the
  established Go idiom.
- **`config.go` `MarshalJSON` drops the redundant `.Duration` selector**
  (staticcheck QF1008).
- **`cmd/console/tui` `WriteString(fmt.Sprintf(...))` rewritten to
  `fmt.Fprintf(&b, ...)`** (staticcheck QF1012) at three sites.
- **`render()` `loading`/`err`/default chain rewritten as `switch`**
  (gocritic ifElseChain).

### Added

- **`renovate.json`** — Renovate Bot configuration for ongoing
  supply-chain updates. Bundles `gomod` minor/patch into one weekly PR
  (`go-deps`), auto-merges GitHub Actions and Docker base-image digest
  bumps, schedules `lockFileMaintenance` monthly, and labels
  vulnerability alerts immediately. No automerges on `gomod` majors —
  those land as manually-reviewed PRs.
- **`internal/tracing/tracing_test.go`** — covers `Setup` with an
  empty endpoint, confirms `HTTPMiddleware` produces a valid span
  inside the handler context, and verifies `HTTPClient` wraps an
  injected base RoundTripper instead of replacing it.

### Changed

- **GitHub Actions opt in to Node.js 24 now.** Both `ci.yml` and
  `release.yml` set `FORCE_JAVASCRIPT_ACTIONS_TO_NODE24=true`,
  pre-empting the September 2026 Node.js 20 removal that would
  otherwise hard-fail CI without warning.
- **`internal/tracing` semconv bumped from v1.26.0 → v1.40.0** to
  match the otel SDK's `resource.Default()` schema URL — the older
  version produced "conflicting Schema URL" errors at `Setup` time.

### Notes

- No new Planning.md items remain. This sprint addresses real lint and
  CI-deprecation debt rather than feature work; future cleanup
  follow-ups should be tracked via issues or a new Planning.md entry.

---

## 26.08 — 2026-05-27

Final Planning.md sprint. Items #13 (peer TLS), #20 (OpenTelemetry tracing),
and #41 (release engineering) — closing out the original 42-item backlog.

### Added

- **OpenTelemetry tracing** (Planning item #20). New `internal/tracing`
  package wires a process-global TracerProvider with an OTLP/HTTP
  exporter. Admin server, health server, and the watchdog's outbound
  HTTP client all produce spans; trace context propagates between the
  two paired agents. `tracing.endpoint` config field (or
  `OTEL_EXPORTER_OTLP_ENDPOINT` env var) selects the collector. With
  no endpoint the SDK runs no-op, so spans are produced but discarded
  — turning real export on later is a one-line change.
- **Peer TLS + optional mTLS** (Planning item #13). New
  `watchdog.tls` (CA pinning + optional client keypair) and
  `health.tls_cert_path` / `tls_key_path` / `client_ca_path` config
  fields. TLS 1.2+ enforced. The watchdog and the health server share
  the same `internal/tlsutil` helpers, so the two sides of the
  handshake are configured by the same struct. Bearer tokens still
  apply on top of TLS.
- **goreleaser release pipeline + SBOM + cosign signing** (Planning
  item #41). New `.goreleaser.yaml` cross-compiles linux/darwin/windows
  × amd64/arm64; `.github/workflows/release.yml` triggers on `v*` tag
  push. Every archive bundles a CycloneDX SBOM
  (`sbom.cyclonedx.json`); every artefact is signed via cosign keyless
  OIDC (GitHub Actions). README documents the `cosign verify-blob`
  command. `make release-snapshot` validates the pipeline locally
  without cutting a tag.

### Changed

- **`health.NewServer` now takes a `ServerOptions` struct.** The
  positional signature is preserved as `health.NewServerLegacy` so
  external callers don't break. New options fields: `TLSConfig`.
- **`health.NewClientWith(addr, ClientOptions)`** is the new full-
  control client constructor; existing `NewClient` / `NewAuthedClient`
  remain as thin wrappers.
- **`watchdog.New` now takes a pre-built `*health.Client`.** Client
  construction (tracing transport, TLS pinning, bearer token, timeouts)
  has moved into `cmd/internal/runtime` so the watchdog package has
  no knowledge of TLS or otel.
- **`config.WatchdogConfig.PeerToken` field is unchanged** but
  `PeerToken` is no longer a `Config` field — it's been part of
  WatchdogConfig since 26.06. The 26.06 ChangeLog mention is still
  accurate.
- **`internal/admin` and `internal/health`** wrap their muxes in
  `tracing.HTTPMiddleware` so every request becomes a server span.

### Tooling

- **CycloneDX SBOM generation** via
  `github.com/CycloneDX/cyclonedx-gomod` (run only at release time;
  not a build-time dep).
- **cosign keyless signing** in CI; transparency log entry uploaded
  to Rekor automatically.
- **`make release-snapshot`** for local pipeline validation.

### Notes / breaking changes

- `health.NewServer(addr, tracker, staleAfter, authToken)` →
  `health.NewServer(addr, tracker, health.ServerOptions{StaleAfter: …, AuthToken: …, TLSConfig: …})`.
  Use `NewServerLegacy` to keep the old signature.
- `watchdog.New(cfg, localStatus, publish)` → `watchdog.New(cfg, client, localStatus, publish)`.
  The `Config.PeerToken` field moved out; build the client with
  `health.NewClientWith(addr, ClientOptions{Token: token, HTTPClient: tracing.HTTPClient(transport)})`.
- New optional config fields: `tracing.endpoint`,
  `watchdog.tls.{ca_cert_path,client_cert_path,client_key_path,server_name}`,
  `health.{tls_cert_path,tls_key_path,client_ca_path}`. All default
  empty (no behaviour change for existing deployments).

### Planning.md status

**All 42 recommendations are now addressed.** Items #1–#42 are either
implemented, satisfied by an earlier change (e.g. #26 by 26.02), or
documented as a deliberate non-goal. Future work should be tracked via
new Planning.md entries or GitHub issues.

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
