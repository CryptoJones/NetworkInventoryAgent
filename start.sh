#!/usr/bin/env bash
# start.sh — build and launch Network Inventory Agent without Docker.
#
# Usage:
#   ./start.sh            interactive mode (prompts for all options)
#   ./start.sh --help     show usage
#
# Modes
#   paired     — Wintermute + Neuromancer (mutual watchdog, recommended)
#   standalone — single agent, no watchdog peer required
#
# The script builds the binaries, optionally updates the subnet list in the
# config files, then starts the selected mode. Logs go to stdout/stderr.
# Press Ctrl+C to stop all running agents.

set -euo pipefail

# ---------- helpers ----------------------------------------------------------

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { printf "${GREEN}[info]${RESET}  %s\n" "$*"; }
warn()    { printf "${YELLOW}[warn]${RESET}  %s\n" "$*"; }
error()   { printf "${RED}[error]${RESET} %s\n" "$*" >&2; }
heading() { printf "\n${BOLD}${CYAN}%s${RESET}\n" "$*"; }

die() { error "$*"; exit 1; }

need_cmd() {
    command -v "$1" &>/dev/null || die "'$1' not found — please install it and re-run."
}

# ---------- usage ------------------------------------------------------------

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Build and run the Network Inventory Agent locally (without Docker).

Options:
  -m, --mode MODE       paired | standalone  (default: prompt)
  -s, --subnet CIDR     subnet to scan, e.g. 192.168.1.0/24 (default: prompt)
                        repeat to add multiple subnets: -s 10.0.0.0/24 -s 10.1.0.0/24
  -b, --build-only      build binaries and exit without starting agents
  -h, --help            show this help

Modes:
  paired      Start Wintermute + Neuromancer as a mutual-watchdog pair.
              Wintermute: health :8080  admin console :9090
              Neuromancer: health :8081  admin console :9091

  standalone  Start a single agent with no watchdog peer.
              Health :8080  admin console :9090

After starting, open the admin console in your browser:
  Paired:     http://127.0.0.1:9090  (Wintermute)
              http://127.0.0.1:9091  (Neuromancer)
  Standalone: http://127.0.0.1:9090

Press Ctrl+C to stop all agents.
EOF
}

# ---------- argument parsing -------------------------------------------------

MODE=""
SUBNETS=()
BUILD_ONLY=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -m|--mode)       MODE="$2"; shift 2 ;;
        -s|--subnet)     SUBNETS+=("$2"); shift 2 ;;
        -b|--build-only) BUILD_ONLY=true; shift ;;
        -h|--help)       usage; exit 0 ;;
        *) die "Unknown option: $1. Run with --help for usage." ;;
    esac
done

# ---------- preflight --------------------------------------------------------

heading "Network Inventory Agent — local startup"

need_cmd go
need_cmd jq

GO_VERSION=$(go version | grep -oP 'go\K[0-9]+\.[0-9]+' | head -1)
info "Go $GO_VERSION detected"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# ---------- mode selection ---------------------------------------------------

if [[ -z "$MODE" ]]; then
    heading "Select mode"
    echo "  1) paired      — Wintermute + Neuromancer with mutual watchdog (recommended)"
    echo "  2) standalone  — single agent, no watchdog peer"
    printf "\nChoice [1/2]: "
    read -r choice
    case "$choice" in
        1|paired)     MODE=paired ;;
        2|standalone) MODE=standalone ;;
        *) die "Invalid choice. Enter 1 or 2." ;;
    esac
fi

[[ "$MODE" == "paired" || "$MODE" == "standalone" ]] \
    || die "Mode must be 'paired' or 'standalone', got: $MODE"

info "Mode: $MODE"

# ---------- subnet configuration ---------------------------------------------

if [[ ${#SUBNETS[@]} -eq 0 ]]; then
    heading "Subnet configuration"

    # Show current subnets from the relevant config
    if [[ "$MODE" == "paired" ]]; then
        CURRENT=$(jq -r '.scanner.subnets[]' configs/wintermute.json 2>/dev/null | tr '\n' ' ')
    else
        CURRENT=$(jq -r '.scanner.subnets[]' configs/agent.json 2>/dev/null \
               || jq -r '.scanner.subnets[]' configs/wintermute.json 2>/dev/null | tr '\n' ' ')
    fi
    [[ -n "$CURRENT" ]] && info "Current subnets: $CURRENT"

    printf "Enter subnet(s) to scan (space-separated CIDR, or press Enter to keep current):\n> "
    read -r subnet_input

    if [[ -n "$subnet_input" ]]; then
        read -ra SUBNETS <<< "$subnet_input"
    fi
fi

# ---------- update config files if subnets were provided --------------------

update_subnets() {
    local config_file="$1"
    local subnets_json
    subnets_json=$(printf '%s\n' "${SUBNETS[@]}" | jq -R . | jq -sc .)
    local tmp
    tmp=$(mktemp)
    jq --argjson s "$subnets_json" '.scanner.subnets = $s' "$config_file" > "$tmp"
    mv "$tmp" "$config_file"
    info "Updated subnets in $config_file: ${SUBNETS[*]}"
}

if [[ ${#SUBNETS[@]} -gt 0 ]]; then
    heading "Updating config files"
    if [[ "$MODE" == "paired" ]]; then
        update_subnets configs/wintermute.json
        update_subnets configs/neuromancer.json
    else
        # For standalone mode use configs/agent.json if it exists, otherwise create it
        if [[ ! -f configs/agent.json ]]; then
            cp configs/wintermute.json configs/agent.json
            # Standalone: no watchdog peer needed; clear peer_addr
            jq '.watchdog.peer_addr = ""' configs/agent.json > /tmp/agent_tmp.json \
                && mv /tmp/agent_tmp.json configs/agent.json
        fi
        update_subnets configs/agent.json
    fi
fi

# ---------- build ------------------------------------------------------------

heading "Building binaries"

if [[ "$MODE" == "paired" ]]; then
    go build -o wintermute  ./cmd/wintermute && info "Built: wintermute"
    go build -o neuromancer ./cmd/neuromancer && info "Built: neuromancer"
else
    go build -o agent ./cmd/agent && info "Built: agent"
fi
go build -o console ./cmd/console && info "Built: console"

if $BUILD_ONLY; then
    info "Build complete. Binaries are in: $SCRIPT_DIR"
    exit 0
fi

# ---------- launch -----------------------------------------------------------

heading "Starting agents"

cleanup() {
    echo ""
    warn "Shutting down..."
    kill "${PIDS[@]}" 2>/dev/null || true
    wait "${PIDS[@]}" 2>/dev/null || true
    info "All agents stopped."
}
trap cleanup SIGINT SIGTERM

PIDS=()

if [[ "$MODE" == "paired" ]]; then
    ./wintermute  -config configs/wintermute.json  &
    PIDS+=($!)
    info "Wintermute started (PID $!)"

    ./neuromancer -config configs/neuromancer.json &
    PIDS+=($!)
    info "Neuromancer started (PID $!)"

    echo ""
    info "Admin consoles:"
    info "  Wintermute  → http://127.0.0.1:9090"
    info "  Neuromancer → http://127.0.0.1:9091"
    info "Health endpoints:"
    info "  Wintermute  → http://127.0.0.1:8080/health"
    info "  Neuromancer → http://127.0.0.1:8081/health"
else
    AGENT_CONFIG="configs/agent.json"
    [[ -f "$AGENT_CONFIG" ]] || AGENT_CONFIG="configs/wintermute.json"
    ./agent -config "$AGENT_CONFIG" &
    PIDS+=($!)
    info "Agent started (PID $!)"

    echo ""
    info "Admin console → http://127.0.0.1:9090"
    info "Health        → http://127.0.0.1:8080/health"
fi

info "TUI console: ./console -db <database-file>"
echo ""
warn "Press Ctrl+C to stop all agents"
echo ""

wait "${PIDS[@]}"
