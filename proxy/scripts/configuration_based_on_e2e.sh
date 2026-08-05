#!/usr/bin/env bash
# Creates tmp/proxy-e2e-<env>.toml from a local docker-compose e2e environment
# (test/e2e/envs/<env>/docker-compose.yml, e.g. "op-pp" or "op-pp-2chains").
#
# Unlike configuration_based_on_kurtosis.sh, these environments publish fixed,
# statically-known host ports (no port discovery needed): the L1 RPC and every
# L2 network's RPC / aggkit bridge REST port are read straight from the
# docker-compose.yml port mappings, in file order:
#   - the first "# HTTP RPC" port is the L1 execution client (network 0)
#   - each subsequent "# HTTP RPC" port is an L2 execution client, in the same
#     order its "# REST API" (aggkit bridge) port appears
#   - the "# gRPC RPC" port is the agglayer gRPC endpoint
# The RollupManagerAddr and L1GlobalExitRootAddress are read from the first
# network's aggkit-config.toml.
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn()  { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
ENVS_DIR="$PROJECT_ROOT/test/e2e/envs"

usage() {
    cat >&2 <<EOF
Usage: $0 [OPTIONS]

Creates tmp/proxy-e2e-<env>.toml based on a local docker-compose e2e
environment (test/e2e/envs/<env>/docker-compose.yml).

The generated config points [L1RPC] at the environment's L1 node, sets
[BridgeServiceFinder].RollupManagerAddr and [Tracker].L1GlobalExitRootAddress
from the first network's aggkit config, and fills static
[BridgeServiceFinder.BridgeURLs] /
[BridgeServiceFinder.RPCURLs] overrides for every L2 network in the
environment, plus network 0 (L1): its RPC is the environment's L1 node and
its bridge service is the first aggkit instance (all of them sync the L1
side). The static overrides are required because the on-chain URLs
(trustedSequencerURL / aggchainMetadata) point at docker-compose-internal
hostnames that are not reachable from the host. It also sets
[Tracker.AgglayerClient.GRPC].URL from the environment's agglayer service.

If neither --env nor --file is given, the script tries to auto-detect a
running e2e environment via 'docker compose ls' (matching a running
project's compose file against test/e2e/envs/<env>/docker-compose.yml).
If none or more than one are running, it falls back to the default (op-pp).

Options:
  -e, --env       ENV         e2e environment name under test/e2e/envs (default: op-pp,
                               or the auto-detected running environment)
  -f, --file      PATH        Path to a docker-compose.yml (overrides --env and auto-detection)
  -o, --output    PATH        Output path relative to project root (default: tmp/proxy-e2e-op-pp.toml,
                               regardless of --env/auto-detected environment)
  -l, --list                  List available e2e environments and exit
  -h, --help                  Show this help

Environment variables (override defaults):
  BLOCK_FINALITY        BridgeServiceFinder.BlockFinality (default: LatestBlock —
                         FinalizedBlock lags too much on a local devnet)
  POLL_INTERVAL         BridgeServiceFinder.PollInterval (default: 10s)
  HEALTH_CHECK_PATH     BridgeServiceFinder.HealthCheckPath (default: "/" — the aggkit
                         bridge REST service serves its health check at the root path)
  OUTPUT_FILE           Output path (relative to project root)

Examples:
  $0                      # Environment "op-pp"
  $0 --env op-pp-2chains
  $0 --list
EOF
    exit 1
}

ENV_NAME="op-pp"
ENV_EXPLICIT=false
COMPOSE_FILE=""
FILE_EXPLICIT=false
OUTPUT_FILE="${OUTPUT_FILE:-}"
BLOCK_FINALITY="${BLOCK_FINALITY:-LatestBlock}"
POLL_INTERVAL="${POLL_INTERVAL:-10s}"
HEALTH_CHECK_PATH="${HEALTH_CHECK_PATH:-/}"

list_envs() {
    log_info "Available e2e environments in $ENVS_DIR:"
    for d in "$ENVS_DIR"/*/; do
        [[ -f "${d}docker-compose.yml" ]] && echo "  - $(basename "$d")" >&2
    done
    exit 0
}

# Auto-detects a running e2e environment via 'docker compose ls', matching each
# running project's compose file against test/e2e/envs/<env>/docker-compose.yml.
# Sets ENV_NAME when exactly one match is found; otherwise leaves it untouched
# (default or explicitly-provided value) and logs why.
detect_running_env() {
    if ! command -v docker &>/dev/null; then
        return
    fi

    local compose_json
    compose_json=$(docker compose ls --format json 2>/dev/null) || return
    [[ -z "$compose_json" || "$compose_json" == "[]" ]] && return

    local configs
    if command -v jq &>/dev/null; then
        configs=$(echo "$compose_json" | jq -r '.[].ConfigFiles')
    elif command -v python3 &>/dev/null; then
        configs=$(echo "$compose_json" | python3 -c '
import json, sys
for p in json.load(sys.stdin):
    print(p.get("ConfigFiles", ""))
')
    else
        log_warn "Neither jq nor python3 found — skipping e2e environment auto-detection"
        return
    fi

    local detected=() cfg path env
    while IFS= read -r cfg; do
        [[ -z "$cfg" ]] && continue
        # ConfigFiles can be a comma-separated list of paths (multiple -f flags)
        IFS=',' read -ra paths <<< "$cfg"
        for path in "${paths[@]}"; do
            if [[ "$path" == "$ENVS_DIR"/*/docker-compose.yml ]]; then
                env="${path#"$ENVS_DIR"/}"
                env="${env%/docker-compose.yml}"
                detected+=("$env")
            fi
        done
    done <<< "$configs"

    [[ ${#detected[@]} -eq 0 ]] && return
    mapfile -t detected < <(printf '%s\n' "${detected[@]}" | sort -u)

    if [[ ${#detected[@]} -eq 1 ]]; then
        ENV_NAME="${detected[0]}"
        log_info "Auto-detected running e2e environment: $ENV_NAME"
    else
        log_warn "Multiple running e2e environments detected (${detected[*]}) — pass --env to disambiguate, defaulting to '$ENV_NAME'"
    fi
}

# Parse flags
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -l|--list) list_envs ;;
        -e|--env)
            [[ $# -lt 2 ]] && { log_error "--env requires a value"; usage; }
            ENV_NAME="$2"; ENV_EXPLICIT=true; shift 2 ;;
        -f|--file)
            [[ $# -lt 2 ]] && { log_error "--file requires a value"; usage; }
            COMPOSE_FILE="$2"; FILE_EXPLICIT=true; shift 2 ;;
        -o|--output)
            [[ $# -lt 2 ]] && { log_error "--output requires a value"; usage; }
            OUTPUT_FILE="$2"; shift 2 ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

if [[ "$ENV_EXPLICIT" == false && "$FILE_EXPLICIT" == false ]]; then
    detect_running_env
fi

ENV_DIR="$ENVS_DIR/$ENV_NAME"
[[ -z "$COMPOSE_FILE" ]] && COMPOSE_FILE="$ENV_DIR/docker-compose.yml"
[[ -z "$OUTPUT_FILE" ]] && OUTPUT_FILE="tmp/proxy-e2e-op-pp.toml"
OUTPUT_PATH="$PROJECT_ROOT/$OUTPUT_FILE"

if [[ ! -f "$COMPOSE_FILE" ]]; then
    log_error "docker-compose file not found: $COMPOSE_FILE"
    log_error "Run '$0 --list' to see available e2e environments"
    exit 1
fi

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Extracts, in file order, the *published* (host-side) port of every active
# (non-commented) port mapping tagged with the given trailing comment marker,
# e.g. "HTTP RPC" or "REST API".
extract_ports_by_marker() {
    local marker="$1"
    grep -E "^[[:space:]]*- \"[0-9]+:[0-9]+\"[[:space:]]*# ${marker}\$" "$COMPOSE_FILE" \
        | sed -E 's/.*- "([0-9]+):[0-9]+".*/\1/'
}

get_rollup_manager_addr() {
    local config_file="$1" addr
    addr=$(grep -E '^\s*polygonRollupManagerAddress\s*=' "$config_file" \
        | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "polygonRollupManagerAddress not found in $config_file"
        exit 1
    fi
    echo "$addr"
}

get_l1_ger_addr() {
    local config_file="$1" addr
    addr=$(grep -E '^\s*polygonZkEVMGlobalExitRootAddress\s*=' "$config_file" \
        | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "polygonZkEVMGlobalExitRootAddress not found in $config_file"
        exit 1
    fi
    echo "$addr"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

log_info "Environment:    $ENV_NAME"
log_info "Compose file:   $COMPOSE_FILE"
log_info "Output:         $OUTPUT_PATH"

mapfile -t HTTP_RPC_PORTS < <(extract_ports_by_marker "HTTP RPC")
mapfile -t REST_API_PORTS < <(extract_ports_by_marker "REST API")
mapfile -t AGGLAYER_GRPC_PORTS < <(extract_ports_by_marker "gRPC RPC")

if [[ ${#HTTP_RPC_PORTS[@]} -eq 0 ]]; then
    log_error "No '# HTTP RPC' port mappings found in $COMPOSE_FILE"
    exit 1
fi

AGGLAYER_GRPC_URL=""
if [[ ${#AGGLAYER_GRPC_PORTS[@]} -gt 0 ]]; then
    AGGLAYER_GRPC_URL="http://localhost:${AGGLAYER_GRPC_PORTS[0]}"
    log_info "Agglayer gRPC URL: $AGGLAYER_GRPC_URL"
else
    log_warn "No '# gRPC RPC' port mapping found in $COMPOSE_FILE — Tracker.AgglayerClient.GRPC.URL override omitted"
fi

L1_RPC_PORT="${HTTP_RPC_PORTS[0]}"
L1_RPC_URL="http://localhost:${L1_RPC_PORT}"
log_info "L1 RPC URL: $L1_RPC_URL"

L2_COUNT=$(( ${#HTTP_RPC_PORTS[@]} - 1 ))
if [[ $L2_COUNT -ne ${#REST_API_PORTS[@]} ]]; then
    log_warn "Found $L2_COUNT L2 execution client(s) but ${#REST_API_PORTS[@]} aggkit REST API port(s)" \
              "— networks are matched by order and the mismatch may misalign them"
fi

# RollupManagerAddr: read from the first network's aggkit config (the same
# RollupManager contract is shared by every network in the environment).
FIRST_NETWORK_DIR=$(find "$ENV_DIR/config" -maxdepth 1 -type d -regextype posix-extended \
    -regex '.*/[0-9]{3}' | sort | head -1)
if [[ -z "$FIRST_NETWORK_DIR" ]]; then
    log_error "No network config directory (e.g. config/001) found under $ENV_DIR/config"
    exit 1
fi
ROLLUP_MANAGER_ADDR=$(get_rollup_manager_addr "$FIRST_NETWORK_DIR/aggkit-config.toml")
log_info "RollupManagerAddr: $ROLLUP_MANAGER_ADDR"

# L1GlobalExitRootAddress: same source as RollupManagerAddr, read from the first network's
# aggkit config (the GlobalExitRoot contract is shared by every network in the environment)
L1_GER_ADDR=$(get_l1_ger_addr "$FIRST_NETWORK_DIR/aggkit-config.toml")
log_info "L1GlobalExitRootAddress: $L1_GER_ADDR"

# ---------------------------------------------------------------------------
# Per-network static overrides (BridgeURLs / RPCURLs)
# ---------------------------------------------------------------------------

BRIDGE_URLS_BLOCK=""
RPC_URLS_BLOCK=""
L1_BRIDGE_URL=""

for ((i = 1; i <= L2_COUNT; i++)); do
    l2_rpc_url="http://localhost:${HTTP_RPC_PORTS[$i]}"
    log_info "Network $i: L2 RPC URL:         $l2_rpc_url"
    RPC_URLS_BLOCK+="$i = \"$l2_rpc_url\"
"

    if [[ -n "${REST_API_PORTS[$((i - 1))]:-}" ]]; then
        bridge_url="http://localhost:${REST_API_PORTS[$((i - 1))]}"
        log_info "Network $i: bridge service URL: $bridge_url"
        BRIDGE_URLS_BLOCK+="$i = \"$bridge_url\"
"
        # every aggkit bridge service syncs L1 too: the first instance answers for network 0
        [[ -z "$L1_BRIDGE_URL" ]] && L1_BRIDGE_URL="$bridge_url"
    else
        log_warn "Network $i: no aggkit REST API port found — BridgeURLs override omitted"
    fi
done

log_info "Discovered $L2_COUNT L2 network(s)"

# Network 0 (L1) is not enumerated on-chain, so the static overrides are the only way the
# finder can serve it: the L1 RPC is already known, and the first aggkit bridge service
# answers for network 0 (all instances sync the L1 side)
RPC_URLS_BLOCK="0 = \"$L1_RPC_URL\"
$RPC_URLS_BLOCK"
if [[ -n "$L1_BRIDGE_URL" ]]; then
    log_info "Network 0 (L1): bridge service URL: $L1_BRIDGE_URL"
    BRIDGE_URLS_BLOCK="0 = \"$L1_BRIDGE_URL\"
$BRIDGE_URLS_BLOCK"
else
    log_warn "Network 0 (L1): no bridge service discovered — BridgeURLs override omitted"
fi
log_info "Network 0 (L1): RPC URL:            $L1_RPC_URL"

mkdir -p "$(dirname "$OUTPUT_PATH")"

cat > "$OUTPUT_PATH" <<EOF
[Log]
Environment = "development"
Level = "info"
Outputs = ["stderr"]

[L1RPC]
URL = "$L1_RPC_URL"
Mode = "basic"
RetryMode = "backoff"
MaxRetries = 5
InitialBackoff = "2s"
MaxBackoff = "10s"
BackoffMultiplier = 2.0

[BridgeServiceFinder]
RollupManagerAddr = "$ROLLUP_MANAGER_ADDR"
BlockFinality = "$BLOCK_FINALITY"
PollInterval = "$POLL_INTERVAL"
BlockChunkSize = 10000
# The aggkit bridge REST service serves its health check at the root path
HealthCheckPath = "$HEALTH_CHECK_PATH"
HealthCheckTimeout = "5s"
RequireAllHealthyOnStart = false

[Tracker]
L1GlobalExitRootAddress = "$L1_GER_ADDR"
EOF

if [[ -n "$BRIDGE_URLS_BLOCK" ]]; then
    printf '\n# Static overrides: docker-compose publishes the services on localhost ports; the\n' >> "$OUTPUT_PATH"
    printf '# on-chain URLs (trustedSequencerURL / aggchainMetadata) are compose-internal hostnames.\n' >> "$OUTPUT_PATH"
    printf '[BridgeServiceFinder.BridgeURLs]\n%s' "$BRIDGE_URLS_BLOCK" >> "$OUTPUT_PATH"
fi

if [[ -n "$RPC_URLS_BLOCK" ]]; then
    printf '\n[BridgeServiceFinder.RPCURLs]\n%s' "$RPC_URLS_BLOCK" >> "$OUTPUT_PATH"
fi

if [[ -n "$AGGLAYER_GRPC_URL" ]]; then
    printf '\n[Tracker.AgglayerClient.GRPC]\nURL = "%s"\n' "$AGGLAYER_GRPC_URL" >> "$OUTPUT_PATH"
fi

log_info "Configuration written to: $OUTPUT_PATH"

# ---------------------------------------------------------------------------
# VS Code launch.json
# ---------------------------------------------------------------------------

update_vscode_launch() {
    local launch_file="$PROJECT_ROOT/.vscode/launch.json"
    local entry_name="proxy e2e-op-pp"

    if [[ ! -f "$launch_file" ]]; then
        log_info "No .vscode/launch.json found — skipping VS Code launch config"
        return
    fi

    if grep -q "\"$entry_name\"" "$launch_file"; then
        log_info "VS Code launch config '$entry_name' already exists, skipping"
        return
    fi

    if ! command -v python3 &>/dev/null; then
        log_warn "python3 not found — add the following entry manually to .vscode/launch.json:"
        cat >&2 <<MANUAL
        {
            "name": "$entry_name",
            "type": "go",
            "request": "launch",
            "mode": "auto",
            "program": "proxy/cmd/",
            "cwd": "\${workspaceFolder}",
            "args":[
                "run", "--cfg", "$OUTPUT_FILE",
            ]
        },
MANUAL
        return
    fi

    python3 - "$launch_file" "$OUTPUT_FILE" "$entry_name" <<'PYEOF'
import sys

launch_file = sys.argv[1]
config_path = sys.argv[2]
entry_name = sys.argv[3]

new_entry = (
    '        {\n'
    f'            "name": "{entry_name}",\n'
    '            "type": "go",\n'
    '            "request": "launch",\n'
    '            "mode": "auto",\n'
    '            "program": "proxy/cmd/",\n'
    '            "cwd": "${workspaceFolder}",\n'
    '            "args": [\n'
    f'                "run", "--cfg", "{config_path}"\n'
    '            ]\n'
    '        }\n'
)

with open(launch_file, 'r') as f:
    content = f.read()

# launch.json is JSONC (comments, trailing commas), so edit it as text:
# insert the entry right before the last ']' (the configurations array close).
idx = content.rfind(']')
if idx == -1:
    print('ERROR: could not find configurations closing bracket', file=sys.stderr)
    sys.exit(1)

head = content[:idx].rstrip()
tail = content[idx:]
if not head.endswith(('[', ',')):
    head += ','

with open(launch_file, 'w') as f:
    f.write(head + '\n' + new_entry + '    ' + tail)
PYEOF

    log_info "Added '$entry_name' to .vscode/launch.json"
}

update_vscode_launch

log_info "Run the proxy with:"
log_info "  go run ./proxy/cmd run --cfg $OUTPUT_FILE"
