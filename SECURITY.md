# Security Policy

## Supported versions

Only the latest commit on `main` is actively maintained. There are no versioned releases at this time. Security fixes are applied to `main` and not backported.

## Reporting a vulnerability

**Please do not report security vulnerabilities through public issue trackers, pull requests, or discussion threads.**

Report vulnerabilities privately by emailing **security@ronin48.io**. Include as much of the following as possible:

- A description of the vulnerability and its potential impact
- The affected component (e.g. health server, scanner, config loading)
- Steps to reproduce or a proof-of-concept
- Any suggested mitigations you have already identified

You will receive an acknowledgement within **72 hours**. We will keep you informed as the issue is investigated and resolved, and will credit you in the fix unless you prefer to remain anonymous.

Please do not disclose the vulnerability publicly until a fix has been released and affected users have had reasonable time to update.

## Scope

The following are in scope for security reports:

- Unauthorised access to the HTTP health or status endpoints
- Injection vulnerabilities in subnet or config input handling
- SQL injection in any database query
- Information disclosure via log output or the `/status` endpoint
- Denial-of-service conditions reachable without authentication
- Insecure defaults in the shipped configuration files

The following are out of scope:

- Vulnerabilities in dependencies not introduced or configurable by this project
- Issues requiring physical access to the host machine
- Issues in Go versions prior to the current stable release
- Scanner behaviour on networks the operator does not own or have permission to scan (this is an operator responsibility)

## OWASP Top 10 compliance

The following table documents the project's posture against the [OWASP Top 10 (2021)](https://owasp.org/Top10/).

| # | Category | Status | Notes |
|---|----------|--------|-------|
| A01 | Broken Access Control | ⚠️ Partial | `/health` and `/status` are unauthenticated on the loopback default but require `health.auth_token` when bound off-loopback (enforced at startup). The admin console (full inventory, exports, JSON API, `POST /scan`) likewise requires `admin.auth_token` when bound off-loopback — the agent refuses to start otherwise. Both default to `127.0.0.1` (loopback only). |
| A02 | Cryptographic Failures | ✅ Pass | Peer-to-peer watchdog traffic supports TLS (with optional mTLS) — set `watchdog.tls.ca_cert_path` and `health.tls_cert_path`/`tls_key_path` in the configs. TLS 1.2+ enforced. Database is stored unencrypted; operators should apply filesystem-level encryption where needed. |
| A03 | Injection | ✅ Pass | All SQL queries use parameterized `?` placeholders. No shell commands are invoked; the scanner uses `net.Dialer` directly. |
| A04 | Insecure Design | ✅ Pass | Health server binds to loopback by default. `peer_addr` is validated to `http`/`https` schemes only, preventing SSRF via alternate URI schemes. No user-controlled input reaches internal APIs without validation. |
| A05 | Security Misconfiguration | ✅ Pass | Default `health.addr` is `127.0.0.1:8080` (loopback only). HTTP server has explicit read, write, and idle timeouts. Response bodies from peers are capped at 1 MiB. |
| A06 | Vulnerable Components | ✅ Pass | All dependencies are pure Go (no C libraries). `go.sum` is committed and verified on every build. `govulncheck` is required before dependency PRs (see CONTRIBUTING.md). |
| A07 | Auth Failures | ⚠️ Partial | Loopback-only defaults are unauthenticated by design. Off-loopback binds of both the health server and the admin console require a shared bearer/Basic token, enforced at startup; tokens are compared in constant time. |
| A08 | Data Integrity | ✅ Pass | `go.sum` provides cryptographic verification of all module downloads. Config validation rejects malformed or unexpected values at startup. |
| A09 | Logging & Monitoring | ✅ Pass | Structured `log/slog` output in text or JSON format. All three watchdog failure conditions (liveness, freshness, consistency) are logged at `WARN` or `ERROR` level with structured fields. |
| A10 | SSRF | ✅ Pass | `peer_addr` is validated to `http`/`https` only at config load time. Response bodies from external HTTP calls are limited to 1 MiB via `io.LimitReader`. Scanner targets come from operator-controlled config, not external input. |

## OWASP AI Top 10

The OWASP AI Top 10 is **not applicable** to this project. NetworkInventoryAgent contains no AI or machine-learning components, no LLM integrations, no model inference, and no training pipelines.

## Security considerations for operators

NetworkInventoryAgent is designed to run on a trusted internal network. Before deploying, consider the following:

**Health endpoints are unauthenticated.** The `/health` and `/status` endpoints expose agent name, scan counts, host counts, and timestamps to anyone who can reach the listening address. The default bind address is `127.0.0.1` (loopback only). Binding off-loopback requires `health.auth_token` (or `INVENTORY_AUTH_TOKEN`); the agent refuses to start without it.

**The admin console is gated off-loopback.** The console at `admin.addr` (default `127.0.0.1:9090`) serves the full host/port inventory, `/export.json|csv`, the `/api/v1/*` query API, and the `POST /scan` trigger. On the loopback default it is unauthenticated for convenience; binding it off-loopback (e.g. `0.0.0.0` for Docker, or via `INVENTORY_ADMIN_ADDR`) requires `admin.auth_token` (or `INVENTORY_ADMIN_TOKEN`) and the agent refuses to start without it. Clients authenticate with `Authorization: Bearer <token>` or HTTP Basic auth using the token as the password.

**Peer communication can use TLS.** Watchdog checks between Wintermute and Neuromancer default to plain HTTP for the loopback case. For off-loopback deployments, switch `watchdog.peer_addr` to `https://…`, set `watchdog.tls.ca_cert_path` to the CA that signs the peer's cert, and set `health.tls_cert_path` / `health.tls_key_path` on the peer. For full mutual auth, set `health.client_ca_path` on both sides and `watchdog.tls.client_cert_path` / `client_key_path` on the dialer side. Bearer tokens stack on top of TLS.

**The agent performs active TCP scanning.** Running the agent on networks you do not own or have explicit written permission to scan may violate laws and terms of service. The operator is solely responsible for ensuring scans are authorised.

**Config files may contain sensitive paths.** The database path and peer addresses are stored in plaintext JSON. Restrict file permissions appropriately:

```bash
chmod 600 wintermute.json neuromancer.json
```

**Database files are unencrypted.** SQLite databases are written to disk without encryption. Apply filesystem-level encryption or access controls if the host inventory data is sensitive.

**Log output may contain IP addresses.** At `debug` level, logs include discovered host IPs and scan details. Treat log files with the same access controls as the database.
