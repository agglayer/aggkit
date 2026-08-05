#!/usr/bin/env bash
# Resolves connection info (RPC URLs, bridge address, agglayer URLs, ...) from a
# local docker-compose e2e environment (test/e2e/envs/<env>/docker-compose.yml,
# e.g. "op-pp" or "op-pp-2chains") and prints `export VAR=value` lines on stdout
# so they can be loaded into the current shell. Logs go to stderr.
#
# Unlike export_kurtosis_env.sh, these environments publish fixed, statically-known
# host ports (no port discovery needed): every URL is read straight from the
# docker-compose.yml port mappings, in file order:
#   - the first "# HTTP RPC" port is the L1 execution client
#   - each subsequent "# HTTP RPC" port is an L2 execution client, one per network
#     index, in the same order the networks are declared
#
# Usage (load the variables into your shell):
#   source <(tools/exit_certificate/scripts/export_e2e_env.sh 1)
#   # or:
#   eval "$(tools/exit_certificate/scripts/export_e2e_env.sh 1)"
#
# Afterwards bridge_l1_to_l2.sh (and other scripts) reuse L1_RPC_URL, L2_RPC_URL,
# BRIDGE_ADDR, ... directly instead of parsing the docker-compose file again.
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn()  { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
ENVS_DIR="$PROJECT_ROOT/test/e2e/envs"

usage() {
    cat >&2 <<EOF
Usage: $0 [OPTIONS] [NETWORK_INDEX]

Resolves local e2e docker-compose connection info and prints \`export VAR=value\`
lines on stdout. Load it into your shell with: source <($0 NETWORK_INDEX)

Arguments:
  NETWORK_INDEX   L2 network index to target (default: 1 → config/001)

Options:
  -e, --env    ENV     e2e environment name under test/e2e/envs (default: \$E2E_ENV or "op-pp")
  -f, --file   PATH    Path to a docker-compose.yml (overrides --env)
  -l, --list           List available e2e environments and exit
  -h, --help           Show this help

Exported variables:
  E2E_ENV   NETWORK_INDEX   L1_RPC_URL   L2_RPC_URL   BRIDGE_ADDR
  AGGLAYER_ADMIN_URL   AGGLAYER_GRPC_URL   BRIDGE_SERVICE_URL

Examples:
  source <($0)                     # Network 1, environment "op-pp"
  source <($0 2)                   # Network 2 (config/002)
  eval "\$($0 --env op-pp-2chains 2)"
EOF
    exit 1
}

ENV_NAME="${E2E_ENV:-op-pp}"
COMPOSE_FILE=""
NETWORK_INDEX=1

list_envs() {
    log_info "Available e2e environments in $ENVS_DIR:"
    for d in "$ENVS_DIR"/*/; do
        [[ -f "${d}docker-compose.yml" ]] && echo "  - $(basename "$d")" >&2
    done
    exit 0
}

# Parse flags and positional args
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -l|--list) list_envs ;;
        -e|--env)
            [[ $# -lt 2 ]] && { log_error "--env requires a value"; usage; }
            ENV_NAME="$2"; shift 2 ;;
        -f|--file)
            [[ $# -lt 2 ]] && { log_error "--file requires a value"; usage; }
            COMPOSE_FILE="$2"; shift 2 ;;
        [0-9]*)
            NETWORK_INDEX="$1"; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

ENV_DIR="$ENVS_DIR/$ENV_NAME"
[[ -z "$COMPOSE_FILE" ]] && COMPOSE_FILE="$ENV_DIR/docker-compose.yml"
NETWORK_SUFFIX=$(printf '%03d' "$NETWORK_INDEX")

if [[ ! -f "$COMPOSE_FILE" ]]; then
    log_error "docker-compose file not found: $COMPOSE_FILE"
    log_error "Run '$0 --list' to see available e2e environments"
    exit 1
fi

# ---------------------------------------------------------------------------
# docker-compose helpers
# ---------------------------------------------------------------------------

# Extracts, in file order, the *published* (host-side) port of every active
# (non-commented) port mapping tagged with the given trailing comment marker,
# e.g. "HTTP RPC", "gRPC RPC", "Admin API".
extract_ports_by_marker() {
    local marker="$1"
    grep -E "^[[:space:]]*- \"[0-9]+:[0-9]+\"[[:space:]]*# ${marker}\$" "$COMPOSE_FILE" \
        | sed -E 's/.*- "([0-9]+):[0-9]+".*/\1/'
}

get_bridge_address() {
    local config_file="$ENV_DIR/config/$NETWORK_SUFFIX/aggkit-config.toml"
    if [[ ! -f "$config_file" ]]; then
        log_error "aggkit config not found: $config_file"
        exit 1
    fi
    local addr
    addr=$(grep 'BridgeAddr' "$config_file" | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "BridgeAddr not found in $config_file"
        exit 1
    fi
    echo "$addr"
}

# ---------------------------------------------------------------------------
# Resolve
# ---------------------------------------------------------------------------

log_info "Environment:    $ENV_NAME"
log_info "Compose file:   $COMPOSE_FILE"
log_info "Network index:  $NETWORK_INDEX (suffix: $NETWORK_SUFFIX)"

mapfile -t HTTP_RPC_PORTS < <(extract_ports_by_marker "HTTP RPC")
if [[ ${#HTTP_RPC_PORTS[@]} -eq 0 ]]; then
    log_error "No '# HTTP RPC' port mappings found in $COMPOSE_FILE"
    exit 1
fi

L1_RPC_URL="http://localhost:${HTTP_RPC_PORTS[0]}"
log_info "L1 RPC URL: $L1_RPC_URL"

if [[ -z "${HTTP_RPC_PORTS[$NETWORK_INDEX]:-}" ]]; then
    log_error "No L2 execution client found for network index $NETWORK_INDEX in $COMPOSE_FILE"
    log_error "Found $(( ${#HTTP_RPC_PORTS[@]} - 1 )) L2 network(s)"
    exit 1
fi
L2_RPC_URL="http://localhost:${HTTP_RPC_PORTS[$NETWORK_INDEX]}"
log_info "L2 RPC URL: $L2_RPC_URL"

log_info "Getting bridge address from aggkit config..."
BRIDGE_ADDR=$(get_bridge_address)
log_info "Bridge address: $BRIDGE_ADDR"

log_info "Getting bridge service URL..."
BRIDGE_SERVICE_URL=""
mapfile -t REST_API_PORTS < <(extract_ports_by_marker "REST API")
# One "# REST API" port per L2 network's aggkit-XXX service, in file order (no leading
# L1 entry, unlike HTTP_RPC_PORTS) — network N is at index N-1.
rest_api_idx=$((NETWORK_INDEX - 1))
if [[ -n "${REST_API_PORTS[$rest_api_idx]:-}" ]]; then
    BRIDGE_SERVICE_URL="http://localhost:${REST_API_PORTS[$rest_api_idx]}"
    log_info "Bridge service URL: $BRIDGE_SERVICE_URL"
else
    log_warn "No '# REST API' port mapping found for network index $NETWORK_INDEX — BRIDGE_SERVICE_URL will be empty"
fi

log_info "Getting agglayer URLs..."
AGGLAYER_ADMIN_URL=""
if admin_port=$(extract_ports_by_marker "Admin API" | head -1) && [[ -n "$admin_port" ]]; then
    AGGLAYER_ADMIN_URL="http://localhost:${admin_port}"
    log_info "Agglayer admin URL: $AGGLAYER_ADMIN_URL"
else
    log_warn "No '# Admin API' port mapping found — AGGLAYER_ADMIN_URL will be empty"
fi
AGGLAYER_GRPC_URL=""
if grpc_port=$(extract_ports_by_marker "gRPC RPC" | head -1) && [[ -n "$grpc_port" ]]; then
    AGGLAYER_GRPC_URL="http://localhost:${grpc_port}"
    log_info "Agglayer gRPC URL:  $AGGLAYER_GRPC_URL"
else
    log_warn "No '# gRPC RPC' port mapping found — AGGLAYER_GRPC_URL will be empty"
fi

# ---------------------------------------------------------------------------
# Emit export statements on stdout
# ---------------------------------------------------------------------------

cat <<EOF
export E2E_ENV="$ENV_NAME"
export NETWORK_INDEX="$NETWORK_INDEX"
export L1_RPC_URL="$L1_RPC_URL"
export L2_RPC_URL="$L2_RPC_URL"
export BRIDGE_ADDR="$BRIDGE_ADDR"
export BRIDGE_SERVICE_URL="$BRIDGE_SERVICE_URL"
export AGGLAYER_ADMIN_URL="$AGGLAYER_ADMIN_URL"
export AGGLAYER_GRPC_URL="$AGGLAYER_GRPC_URL"
EOF

log_info "Run: source <($0 $NETWORK_INDEX)   to load these into your shell."
