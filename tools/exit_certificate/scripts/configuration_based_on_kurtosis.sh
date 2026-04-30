#!/usr/bin/env bash
# Creates tmp/exit_certificate-kurtosis.json from a running Kurtosis enclave.
# Uses kurtosis port print and files download to extract RPC URLs and the bridge address.
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
Usage: $0 [OPTIONS] [NETWORK_INDEX]

Creates tmp/exit_certificate-kurtosis.json based on a running Kurtosis enclave.

Arguments:
  NETWORK_INDEX   L2 network index to target (default: 1 → service suffix 001)

Options:
  -e, --enclave   ENCLAVE    Kurtosis enclave name (default: \$KURTOSIS_ENCLAVE or "aggkit")
  -o, --output    PATH       Output path relative to project root (default: tmp/exit_certificate-kurtosis.json)
  -h, --help                 Show this help

Environment variables (override defaults):
  KURTOSIS_ENCLAVE            Enclave name
  KURTOSIS_ARTIFACT_AGGKIT_CONFIG  Aggkit config artifact name (default: aggkit-config-artifact)
  L2_SERVICE_PREFIX           Kurtosis L2 execution client service prefix (default: op-el-1-op-geth-op-node)
  L1_SERVICE                  Kurtosis L1 execution service name (default: el-1-geth-lighthouse)
  EXIT_ADDRESS                Address to receive SC-locked value (default: zero address)
  OUTPUT_FILE                 Output path (relative to project root)

Examples:
  $0                          # Network 1, enclave "aggkit"
  $0 2                        # Network 2 (service op-geth-002)
  $0 --enclave op 1           # Network 1, enclave "op"
  KURTOSIS_ENCLAVE=op $0 1
EOF
    exit 1
}

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

# Defaults (can be overridden by env vars)
KURTOSIS_ENCLAVE="${KURTOSIS_ENCLAVE:-aggkit}"
KURTOSIS_ARTIFACT_AGGKIT_CONFIG="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG:-aggkit-config}"
L2_SERVICE_PREFIX="${L2_SERVICE_PREFIX:-op-el-1-op-geth-op-node}"
L1_SERVICE="${L1_SERVICE:-el-1-geth-lighthouse}"
AGGLAYER_SERVICE="${AGGLAYER_SERVICE:-agglayer}"
EXIT_ADDRESS="${EXIT_ADDRESS:-0x0000000000000000000000000000000000000000}"
OUTPUT_FILE="${OUTPUT_FILE:-tmp/exit_certificate-kurtosis.json}"
NETWORK_INDEX=1

# Parse flags and positional args
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -e|--enclave)
            [[ $# -lt 2 ]] && { log_error "--enclave requires a value"; usage; }
            KURTOSIS_ENCLAVE="$2"; shift 2 ;;
        -o|--output)
            [[ $# -lt 2 ]] && { log_error "--output requires a value"; usage; }
            OUTPUT_FILE="$2"; shift 2 ;;
        [0-9]*)
            NETWORK_INDEX="$1"; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

NETWORK_SUFFIX=$(printf '%03d' "$NETWORK_INDEX")
L2_SERVICE="${L2_SERVICE_PREFIX}-${NETWORK_SUFFIX}"
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

get_l2_rpc_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$L2_SERVICE" rpc 2>/dev/null); then
        log_error "Failed to get L2 RPC port from service '$L2_SERVICE' in enclave '$KURTOSIS_ENCLAVE'"
        log_error "To use a different prefix (e.g. cdk-erigon-sequencer), set L2_SERVICE_PREFIX"
        exit 1
    fi
    port_to_localhost_url "$raw"
}

get_agglayer_admin_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$AGGLAYER_SERVICE" aglr-admin 2>/dev/null); then
        log_error "Failed to get agglayer admin port from service '$AGGLAYER_SERVICE' in enclave '$KURTOSIS_ENCLAVE'"
        exit 1
    fi
    port_to_localhost_url "$raw"
}

get_bridge_address() {
    local tmp_dir
    tmp_dir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmp_dir'" RETURN

    # Try network-specific artifact first (multi-chain: aggkit-config-artifact-001)
    # then fall back to the generic single-chain artifact name.
    local artifact_name="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG}-${NETWORK_SUFFIX}"
    if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$artifact_name" "$tmp_dir" &>/dev/null; then
        log_warn "Artifact '$artifact_name' not found, trying '$KURTOSIS_ARTIFACT_AGGKIT_CONFIG'..."
        artifact_name="$KURTOSIS_ARTIFACT_AGGKIT_CONFIG"
        if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$artifact_name" "$tmp_dir" &>/dev/null; then
            log_error "Could not download artifact '$artifact_name' from enclave '$KURTOSIS_ENCLAVE'"
            exit 1
        fi
    fi

    local config_file="$tmp_dir/config.toml"
    if [[ ! -f "$config_file" ]]; then
        log_error "config.toml not found in downloaded artifact '$artifact_name'"
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
# Main
# ---------------------------------------------------------------------------

log_info "Enclave:        $KURTOSIS_ENCLAVE"
log_info "Network:        $NETWORK_SUFFIX (l2NetworkId: $NETWORK_INDEX)"
log_info "L1 service:     $L1_SERVICE"
log_info "L2 service:     $L2_SERVICE"
log_info "Output:         $OUTPUT_PATH"

log_info "Getting L1 RPC URL..."
L1_RPC_URL=$(get_l1_rpc_url)
log_info "L1 RPC URL: $L1_RPC_URL"

log_info "Getting L2 RPC URL..."
L2_RPC_URL=$(get_l2_rpc_url)
log_info "L2 RPC URL: $L2_RPC_URL"

log_info "Getting bridge address from aggkit config artifact..."
BRIDGE_ADDR=$(get_bridge_address)
log_info "Bridge address: $BRIDGE_ADDR"

log_info "Getting agglayer admin URL..."
AGGLAYER_ADMIN_URL=$(get_agglayer_admin_url)
log_info "Agglayer admin URL: $AGGLAYER_ADMIN_URL"

mkdir -p "$(dirname "$OUTPUT_PATH")"

cat > "$OUTPUT_PATH" <<EOF
{
    "l2RpcUrl": "$L2_RPC_URL",
    "l1RpcUrl": "$L1_RPC_URL",
    "l2BridgeAddress": "$BRIDGE_ADDR",
    "l1BridgeAddress": "$BRIDGE_ADDR",
    "l2NetworkId": $NETWORK_INDEX,
    "targetBlock": "latest",
    "exitAddress": "$EXIT_ADDRESS",
    "destinationNetwork": 0,
    "options": {
        "blockRange": 5000,
        "concurrencyLimit": 20,
        "rpcBatchSize": 200,
        "rpcDelayMs": 0,
        "outputDir": "./output-kurtosis",
        "l1StartBlock": 0,
        "agglayerAdminURL": "$AGGLAYER_ADMIN_URL"
    }
}
EOF

log_info "Configuration written to: $OUTPUT_PATH"

# ---------------------------------------------------------------------------
# VS Code launch.json
# ---------------------------------------------------------------------------

update_vscode_launch() {
    local launch_file="$PROJECT_ROOT/.vscode/launch.json"

    if [[ ! -f "$launch_file" ]]; then
        log_warn ".vscode/launch.json not found, skipping VS Code configuration"
        return
    fi

    if grep -q '"exit_tool kurtosis"' "$launch_file"; then
        log_info "VS Code launch config 'exit_tool kurtosis' already exists, skipping"
        return
    fi

    if ! command -v python3 &>/dev/null; then
        log_warn "python3 not found — add the following entry manually to .vscode/launch.json:"
        cat >&2 <<MANUAL
        {
            "name": "exit_tool kurtosis",
            "type": "go",
            "request": "launch",
            "mode": "auto",
            "program": "tools/exit_certificate/cmd/",
            "cwd": "\${workspaceFolder}",
            "args":[
                "-c", "$OUTPUT_FILE",
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
    '            "name": "exit_tool kurtosis",\n'
    '            "type": "go",\n'
    '            "request": "launch",\n'
    '            "mode": "auto",\n'
    '            "program": "tools/exit_certificate/cmd/",\n'
    '            "cwd": "${workspaceFolder}",\n'
    '            "args":[\n'
    f'                "-c", "{config_path}",\n'
    '            ]\n'
    '        },\n'
)

with open(launch_file, 'r') as f:
    content = f.read()

# Insert before the closing ] of the configurations array
idx = content.rfind('    ]')
if idx == -1:
    print('ERROR: could not find configurations closing bracket', file=sys.stderr)
    sys.exit(1)

with open(launch_file, 'w') as f:
    f.write(content[:idx] + new_entry + content[idx:])
PYEOF

    log_info "Added 'exit_tool kurtosis' to .vscode/launch.json"
}

update_vscode_launch
