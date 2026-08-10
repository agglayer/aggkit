#!/usr/bin/env bash
# Creates tmp/proxy-kurtosis.toml from a running Kurtosis enclave.
# Uses kurtosis port print and files download to extract the L1 RPC URL, the
# RollupManager address and the per-network bridge service / L2 RPC endpoints.
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn()  { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

usage() {
    cat >&2 <<EOF
Usage: $0 [OPTIONS]

Creates tmp/proxy-kurtosis.toml based on a running Kurtosis enclave.

The generated config points [L1RPC] at the enclave's L1 node, sets
[BridgeServiceFinder].RollupManagerAddr and [Tracker].L1GlobalExitRootAddress
from the aggkit config artifact, and fills static
[BridgeServiceFinder.BridgeURLs] / [BridgeServiceFinder.RPCURLs]
overrides for every L2 network found in the enclave, plus network 0 (L1):
its RPC is the enclave's L1 node and its bridge service is the first aggkit
instance (all of them sync the L1 side). The static overrides are required
when running the proxy from the host: the URLs the finder resolves on-chain
(trustedSequencerURL / aggchainMetadata) point at Kurtosis-internal hostnames
that are not reachable outside the enclave — and network 0 is not enumerated
on-chain at all, so the override is the only way to serve it. It also sets
[Tracker.AgglayerClient.GRPC].URL from the enclave's agglayer service.

Options:
  -e, --enclave   ENCLAVE    Kurtosis enclave name (default: \$KURTOSIS_ENCLAVE or "aggkit")
  -o, --output    PATH       Output path relative to project root (default: tmp/proxy-kurtosis.toml)
  -h, --help                 Show this help

Environment variables (override defaults):
  KURTOSIS_ENCLAVE                 Enclave name
  KURTOSIS_ARTIFACT_AGGKIT_CONFIG  Aggkit config artifact name (default: aggkit-config)
  L1_SERVICE                       Kurtosis L1 execution service name (default: el-1-geth-lighthouse)
  L2_SERVICE_PREFIX                Kurtosis L2 execution client service prefix (default: op-el-1-op-geth-op-node)
  AGGKIT_BRIDGE_SERVICE_PREFIX     Aggkit bridge service prefix; service name is
                                   <prefix>-<suffix>-bridge (default: aggkit)
  AGGLAYER_SERVICE                 Kurtosis agglayer service name (default: agglayer)
  MAX_NETWORKS                     Upper bound for the network auto-discovery loop (default: 20)
  BLOCK_FINALITY                   BridgeServiceFinder.BlockFinality (default: LatestBlock —
                                   FinalizedBlock lags too much on a local devnet)
  POLL_INTERVAL                    BridgeServiceFinder.PollInterval (default: 10s)
  HEALTH_CHECK_PATH                BridgeServiceFinder.HealthCheckPath (default: "/" — the aggkit
                                   bridge REST service serves its health check at the root path)
  OUTPUT_FILE                      Output path (relative to project root)

Examples:
  $0                          # Enclave "aggkit"
  $0 --enclave op
  KURTOSIS_ENCLAVE=op $0
EOF
    exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Defaults (can be overridden by env vars)
KURTOSIS_ENCLAVE="${KURTOSIS_ENCLAVE:-aggkit}"
KURTOSIS_ARTIFACT_AGGKIT_CONFIG="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG:-aggkit-config}"
L1_SERVICE="${L1_SERVICE:-el-1-geth-lighthouse}"
L2_SERVICE_PREFIX="${L2_SERVICE_PREFIX:-op-el-1-op-geth-op-node}"
AGGKIT_BRIDGE_SERVICE_PREFIX="${AGGKIT_BRIDGE_SERVICE_PREFIX:-aggkit}"
AGGLAYER_SERVICE="${AGGLAYER_SERVICE:-agglayer}"
MAX_NETWORKS="${MAX_NETWORKS:-20}"
BLOCK_FINALITY="${BLOCK_FINALITY:-LatestBlock}"
POLL_INTERVAL="${POLL_INTERVAL:-10s}"
HEALTH_CHECK_PATH="${HEALTH_CHECK_PATH:-/}"
OUTPUT_FILE="${OUTPUT_FILE:-tmp/proxy-kurtosis.toml}"

# Parse flags
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -e|--enclave)
            [[ $# -lt 2 ]] && { log_error "--enclave requires a value"; usage; }
            KURTOSIS_ENCLAVE="$2"; shift 2 ;;
        -o|--output)
            [[ $# -lt 2 ]] && { log_error "--output requires a value"; usage; }
            OUTPUT_FILE="$2"; shift 2 ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

OUTPUT_PATH="$PROJECT_ROOT/$OUTPUT_FILE"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# kurtosis port print returns something like "http://127.0.0.1:PORT"
# Extract port and rebuild as http://localhost:PORT.
port_to_localhost_url() {
    local raw_url="$1"
    local port
    port=$(echo "$raw_url" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

get_l1_rpc_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$L1_SERVICE" rpc 2>/dev/null); then
        log_error "Failed to get L1 RPC port from service '$L1_SERVICE' in enclave '$KURTOSIS_ENCLAVE'"
        log_error "Ensure the enclave is running: kurtosis enclave inspect $KURTOSIS_ENCLAVE"
        exit 1
    fi
    port_to_localhost_url "$raw"
}

# Aggkit bridge REST endpoint of one L2 network (service aggkit-XXX-bridge, port "rest").
get_bridge_service_url() {
    local suffix="$1" raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "${AGGKIT_BRIDGE_SERVICE_PREFIX}-${suffix}-bridge" rest 2>/dev/null); then
        return 1
    fi
    port_to_localhost_url "$raw"
}

# L2 execution client JSON-RPC endpoint of one network (service op-el-1-...-XXX, port "rpc").
get_l2_rpc_url() {
    local suffix="$1" raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "${L2_SERVICE_PREFIX}-${suffix}" rpc 2>/dev/null); then
        return 1
    fi
    port_to_localhost_url "$raw"
}

# Agglayer gRPC endpoint (service "agglayer", port "aglr-grpc").
get_agglayer_grpc_url() {
    local raw port
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$AGGLAYER_SERVICE" aglr-grpc 2>/dev/null); then
        return 1
    fi
    # kurtosis returns grpc://host:PORT — rebuild as http://localhost:PORT (insecure gRPC)
    port=$(echo "$raw" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

# ---------------------------------------------------------------------------
# Aggkit config artifact — source of the RollupManager address
# ---------------------------------------------------------------------------

AGGKIT_CONFIG_DIR=""

download_aggkit_config() {
    AGGKIT_CONFIG_DIR=$(mktemp -d)

    local artifact_name="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG}-001"
    if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$artifact_name" "$AGGKIT_CONFIG_DIR" &>/dev/null; then
        log_warn "Artifact '$artifact_name' not found, trying '$KURTOSIS_ARTIFACT_AGGKIT_CONFIG'..."
        artifact_name="$KURTOSIS_ARTIFACT_AGGKIT_CONFIG"
        if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$artifact_name" "$AGGKIT_CONFIG_DIR" &>/dev/null; then
            log_error "Could not download artifact '$artifact_name' from enclave '$KURTOSIS_ENCLAVE'"
            exit 1
        fi
    fi

    if [[ ! -f "$AGGKIT_CONFIG_DIR/config.toml" ]]; then
        log_error "config.toml not found in downloaded artifact '$artifact_name'"
        exit 1
    fi
}

cleanup_aggkit_config() {
    [[ -n "$AGGKIT_CONFIG_DIR" ]] && rm -rf "$AGGKIT_CONFIG_DIR"
}

get_rollup_manager_addr() {
    local addr
    addr=$(grep -E '^\s*polygonRollupManagerAddress\s*=' "$AGGKIT_CONFIG_DIR/config.toml" \
        | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "polygonRollupManagerAddress not found in config.toml"
        exit 1
    fi
    echo "$addr"
}

get_l1_ger_addr() {
    local addr
    addr=$(grep -E '^\s*polygonZkEVMGlobalExitRootAddress\s*=' "$AGGKIT_CONFIG_DIR/config.toml" \
        | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "polygonZkEVMGlobalExitRootAddress not found in config.toml"
        exit 1
    fi
    echo "$addr"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

log_info "Enclave:        $KURTOSIS_ENCLAVE"
log_info "L1 service:     $L1_SERVICE"
log_info "Output:         $OUTPUT_PATH"

log_info "Getting L1 RPC URL..."
L1_RPC_URL=$(get_l1_rpc_url)
log_info "L1 RPC URL: $L1_RPC_URL"

log_info "Getting agglayer gRPC URL..."
if AGGLAYER_GRPC_URL=$(get_agglayer_grpc_url); then
    log_info "Agglayer gRPC URL: $AGGLAYER_GRPC_URL"
else
    log_warn "Service '$AGGLAYER_SERVICE' (port 'aglr-grpc') not found — Tracker.AgglayerClient.GRPC.URL override omitted"
    AGGLAYER_GRPC_URL=""
fi

log_info "Downloading aggkit config artifact..."
download_aggkit_config
trap cleanup_aggkit_config EXIT

log_info "Getting RollupManager address from aggkit config artifact..."
ROLLUP_MANAGER_ADDR=$(get_rollup_manager_addr)
log_info "RollupManagerAddr: $ROLLUP_MANAGER_ADDR"

# L1GlobalExitRootAddress: same source as RollupManagerAddr, read from the aggkit config
# artifact (the GlobalExitRoot contract is shared by every network in the enclave)
log_info "Getting L1 GlobalExitRoot address from aggkit config artifact..."
L1_GER_ADDR=$(get_l1_ger_addr)
log_info "L1GlobalExitRootAddress: $L1_GER_ADDR"

# ---------------------------------------------------------------------------
# Per-network static overrides (BridgeURLs / RPCURLs)
# ---------------------------------------------------------------------------

log_info "Discovering L2 networks (up to $MAX_NETWORKS)..."
BRIDGE_URLS_BLOCK=""
RPC_URLS_BLOCK=""
L1_BRIDGE_URL=""
DISCOVERED=0
for ((i = 1; i <= MAX_NETWORKS; i++)); do
    suffix=$(printf '%03d' "$i")

    if ! bridge_url=$(get_bridge_service_url "$suffix"); then
        [[ $i -eq 1 ]] && log_warn "Service '${AGGKIT_BRIDGE_SERVICE_PREFIX}-${suffix}-bridge' not found"
        break
    fi
    log_info "Network $i: bridge service URL: $bridge_url"
    BRIDGE_URLS_BLOCK+="$i = \"$bridge_url\"
"
    # every aggkit bridge service syncs L1 too: the first instance answers for network 0
    [[ -z "$L1_BRIDGE_URL" ]] && L1_BRIDGE_URL="$bridge_url"

    if l2_rpc_url=$(get_l2_rpc_url "$suffix"); then
        log_info "Network $i: L2 RPC URL:         $l2_rpc_url"
        RPC_URLS_BLOCK+="$i = \"$l2_rpc_url\"
"
    else
        log_warn "Network $i: service '${L2_SERVICE_PREFIX}-${suffix}' not found — RPCURLs override omitted"
    fi

    DISCOVERED=$i
done

if [[ $DISCOVERED -eq 0 ]]; then
    log_warn "No L2 networks discovered — the finder will rely on the on-chain URLs, which point"
    log_warn "at Kurtosis-internal hostnames and are NOT reachable from the host."
else
    log_info "Discovered $DISCOVERED network(s)"
fi

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
    printf '\n# Static overrides: Kurtosis publishes the services on localhost ports; the on-chain\n' >> "$OUTPUT_PATH"
    printf '# URLs (trustedSequencerURL / aggchainMetadata) are enclave-internal hostnames.\n' >> "$OUTPUT_PATH"
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

    if [[ ! -f "$launch_file" ]]; then
        log_info "No .vscode/launch.json found — skipping VS Code launch config"
        return
    fi

    if grep -q '"proxy kurtosis"' "$launch_file"; then
        log_info "VS Code launch config 'proxy kurtosis' already exists, skipping"
        return
    fi

    if ! command -v python3 &>/dev/null; then
        log_warn "python3 not found — add the following entry manually to .vscode/launch.json:"
        cat >&2 <<MANUAL
        {
            "name": "proxy kurtosis",
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

    python3 - "$launch_file" "$OUTPUT_FILE" <<'PYEOF'
import sys

launch_file = sys.argv[1]
config_path = sys.argv[2]

new_entry = (
    '        {\n'
    '            "name": "proxy kurtosis",\n'
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

    log_info "Added 'proxy kurtosis' to .vscode/launch.json"
}

update_vscode_launch

log_info "Run the proxy with:"
log_info "  go run ./proxy/cmd run --cfg $OUTPUT_FILE"
