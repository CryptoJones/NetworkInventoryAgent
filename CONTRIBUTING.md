# Contributing to NetworkInventoryAgent

Thank you for your interest in contributing. This document covers everything you need to get started.

## Table of contents

- [Getting started](#getting-started)
- [Development workflow](#development-workflow)
- [Code style](#code-style)
- [Testing](#testing)
- [Submitting changes](#submitting-changes)
- [Reporting issues](#reporting-issues)
- [Architecture overview](#architecture-overview)

---

## Getting started

### Prerequisites

- Go 1.25 or later
- Git
- Docker and Docker Compose (optional, for container-based development)

No C toolchain is required. The SQLite driver (`modernc.org/sqlite`) is pure Go and builds with `CGO_ENABLED=0`.

### Clone and build

```bash
git clone https://codeberg.org/Ronin48/NetworkInventoryAgent.git
cd NetworkInventoryAgent
go build ./...
```

Or using `make`:

```bash
make build
```

### Run the tests

```bash
make test
# equivalent to: go test -race ./...
```

All tests should pass before you begin making changes. If any fail on a clean checkout, please open an issue.

### Docker development

Build the image and start the Wintermute/Neuromancer pair:

```bash
make docker-up        # docker compose up --build -d
make docker-logs      # tail combined logs
make docker-down      # stop and remove containers
```

The Docker-specific configs live in `configs/*.docker.json`. They differ from the local configs in that `health.addr` binds to `0.0.0.0`, `watchdog.peer_addr` uses Compose service names, and `database.path` points to the `/data` volume.

---

## Development workflow

1. **Open an issue first** for any non-trivial change. Describe what you want to change and why. This avoids wasted effort if the direction doesn't fit the project.
2. **Fork the repository** and create a branch from `main`:
   ```bash
   git checkout -b fix/short-description
   ```
3. **Make your changes.** Keep each branch focused on a single logical change.
4. **Write or update tests** to cover your change.
5. **Run the full test suite** and confirm it passes.
6. **Open a pull request** against `main` with a clear description (see [Submitting changes](#submitting-changes)).

---

## Code style

### Formatting

All Go code must be formatted with `gofmt` before submission. Run:

```bash
gofmt -w .
```

Or use the Makefile target which also fails the build on unformatted files:

```bash
make fmt
```

### Linting

Run `go vet` before submitting:

```bash
make vet
# equivalent to: go vet ./...
```

### Comments

- Write comments only when the **why** is non-obvious — a hidden constraint, a subtle invariant, or a workaround for a specific bug.
- Do not describe what the code does; well-named identifiers already do that.
- Package-level doc comments on all exported packages are required.
- One-line doc comments on exported functions and types are required.

### Error handling

- Wrap errors with `fmt.Errorf("context: %w", err)` so callers can inspect the chain.
- Never silently discard errors. If an error genuinely cannot be handled, log it.

### Context

Every function that performs I/O (database queries, HTTP requests, network dials) must accept a `context.Context` as its first parameter and respect cancellation.

### Dependencies

- Keep the dependency count low. Prefer the standard library.
- New dependencies require a clear justification in the pull request description.
- No dependency should require CGo. The project must build with `CGO_ENABLED=0`.

---

## Testing

### Running tests

```bash
# All packages with race detector (required before opening a PR)
make test
# equivalent to: go test -race ./...

# A single package with verbose output
go test -v ./internal/watchdog/...

# All packages without race detector (faster during development)
go test ./...
```

### Vulnerability scanning

Run `govulncheck` before opening a PR to check for known CVEs in dependencies (OWASP A06):

```bash
make vuln
# equivalent to: govulncheck ./...
```

A clean `govulncheck` output is required for any PR that adds or updates a dependency.

### Writing tests

- Place tests in `_test.go` files. Use `package foo_test` for black-box tests and `package foo` for white-box tests that need access to unexported symbols.
- Use `github.com/stretchr/testify/assert` and `require` for assertions.
- Database tests use an in-memory SQLite instance via the `openTestDB(t)` helper in `internal/sqlite/db_test.go` — do not write to the filesystem in tests.
- HTTP server tests bind to `:0` (a random port) and extract the actual address after `Start()` — do not hardcode ports.
- Tests must be hermetic: no shared global state, no dependency on external services, no reliance on execution order.
- Scanner tests use in-memory mock stores — do not make real network connections in unit tests.

### Test coverage areas

When adding a new package or feature, tests should cover at minimum:

| Area | What to test |
|------|-------------|
| Store methods | Happy path, not-found, constraint violations |
| HTTP endpoints | 200/503/correct JSON for each state |
| Watchdog checks | Each of the three checks independently |
| Config loading | Defaults, file override, env override, invalid input, new fields |
| Scanner | CIDR parsing errors, max-hosts guard, context cancellation, network/broadcast skipping |

---

## Submitting changes

### Branch naming

| Type | Pattern | Example |
|------|---------|---------|
| Bug fix | `fix/<description>` | `fix/watchdog-nil-ptr` |
| New feature | `feat/<description>` | `feat/port-scanning` |
| Refactor | `refactor/<description>` | `refactor/scanner-timeout` |
| Documentation | `docs/<description>` | `docs/config-reference` |
| Tests | `test/<description>` | `test/health-server` |

### Pull request checklist

Before opening a PR, confirm:

- [ ] `go build ./...` succeeds
- [ ] `go test ./...` passes
- [ ] `go test -race ./...` passes
- [ ] `gofmt -l .` produces no output
- [ ] `go vet ./...` produces no output
- [ ] `docker build .` succeeds (if Dockerfile was changed)
- [ ] New behaviour is covered by tests
- [ ] Public APIs have doc comments
- [ ] The PR addresses a single logical change

### Pull request description

Include:

- **What** changed and **why**
- A reference to the related issue: `Closes #NN`
- Any migration steps required (new config fields, schema changes, etc.)
- Any decisions or trade-offs made that aren't obvious from the diff

### Commit messages

Use a short imperative subject line (≤ 72 characters) followed by a blank line and a more detailed body when necessary:

```
Add freshness threshold to watchdog config

Exposes max_scan_staleness as a config field so operators can tune
how quickly a stale peer is flagged without recompiling. Defaults to
2× scan_interval to preserve the existing behaviour.
```

---

## Reporting issues

When filing a bug report, include:

- Go version (`go version`)
- Operating system and architecture
- Steps to reproduce
- Expected behaviour
- Actual behaviour (include full log output at `log.level: debug`)
- Config file (redact any sensitive values)

Feature requests are welcome. Describe the problem you are trying to solve, not just the solution you have in mind.

---

## Architecture overview

Before making structural changes, read the **Architecture decisions** section of the [README](README.md). The key constraints are:

- **Store interfaces** — no code outside `internal/sqlite` may import `internal/sqlite` directly. All database access goes through the interfaces in `internal/store`.
- **No CGo** — all dependencies must be pure Go and compile with `CGO_ENABLED=0`.
- **Context everywhere** — all I/O functions accept and respect `context.Context`.
- **Mutual watchdog** — Wintermute and Neuromancer are designed to run as a pair. Changes to the watchdog logic, the health server, or the `/status` response shape affect both agents.
- **Schema migrations** — schema changes belong in a new numbered SQL file in `internal/sqlite/migrations/`. Do not modify existing migration files.
- **Docker** — the `Dockerfile` and `docker-compose.yml` are first-class artifacts. Changes that affect runtime behaviour (ports, paths, config fields) must be reflected in the Docker configs under `configs/*.docker.json`.

If a proposed change conflicts with one of these constraints, discuss it in an issue before investing time in an implementation.
