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

## Security considerations for operators

NetworkInventoryAgent is designed to run on a trusted internal network. Before deploying, consider the following:

**Health endpoints are unauthenticated.** The `/health` and `/status` endpoints expose agent name, scan counts, host counts, and timestamps to anyone who can reach the listening address. Bind the health server to a loopback or management interface (`127.0.0.1:8080`) rather than `0.0.0.0` unless the network segment is trusted.

**The agent performs active TCP scanning.** Running the agent on networks you do not own or have explicit written permission to scan may violate laws and terms of service. The operator is solely responsible for ensuring scans are authorised.

**Config files may contain sensitive paths.** The database path and peer addresses are stored in plaintext JSON. Restrict file permissions appropriately:

```bash
chmod 600 wintermute.json neuromancer.json
```

**Database files are unencrypted.** SQLite databases are written to disk without encryption. Apply filesystem-level encryption or access controls if the host inventory data is sensitive.

**Log output may contain IP addresses.** At `debug` level, logs include discovered host IPs and scan details. Treat log files with the same access controls as the database.
