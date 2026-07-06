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
  KURTOSIS_ENCLAVE                    Enclave name
  KURTOSIS_ARTIFACT_AGGKIT_CONFIG     Aggkit config artifact name (default: aggkit-config)
  KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE  Sequencer keystore artifact (default: aggkit-sequencer-keystore)
  L2_SERVICE_PREFIX                   Kurtosis L2 execution client service prefix (default: op-el-1-op-geth-op-node)
  L1_SERVICE                          Kurtosis L1 execution service name (default: el-1-geth-lighthouse)
  ZKEVM_BRIDGE_SERVICE_PREFIX         Kurtosis zkevm-bridge-service prefix (default: zkevm-bridge-service)
  EXIT_ADDRESS                        Address to receive SC-locked value (default: zero address)
  GENESIS_PREFUND_ETH_WEI             options.genesisPrefundETHWei value: native ETH preminted at
                                      genesis by the Kurtosis enclave (default: 110000 ETH in wei)
  OUTPUT_FILE                         Output path (relative to project root)

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
KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE="${KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE:-aggkit-sequencer-keystore}"
L2_SERVICE_PREFIX="${L2_SERVICE_PREFIX:-op-el-1-op-geth-op-node}"
L1_SERVICE="${L1_SERVICE:-el-1-geth-lighthouse}"
AGGLAYER_SERVICE="${AGGLAYER_SERVICE:-agglayer}"
ZKEVM_BRIDGE_SERVICE_PREFIX="${ZKEVM_BRIDGE_SERVICE_PREFIX:-zkevm-bridge-service}"
_EXIT_ADDRESS_DEFAULT="0xe25f5B65E4976025f670e52b790a9746F27A3DB6"
_EXIT_PRIVKEY_DEFAULT="0xe78f81aa81c6cf9e996084770b2aae4ee1d9e7cddb8724f4dfe60a8bd1c309fe"
_EXIT_KEYSTORE_DEFAULT='{"crypto":{"cipher":"aes-128-ctr","cipherparams":{"iv":"ed35c21427a13a62ca21f86751eb2138"},"ciphertext":"8bbd3830f060f97242508910cbfe38684647fbd915da1ba69298ba7a4fce751d","kdf":"scrypt","kdfparams":{"dklen":32,"n":8192,"p":1,"r":8,"salt":"fb2518fcadcccc9c72cac2dee9c379b3d6f744e7ceabd54f0a830acdbd51589f"},"mac":"371d6de1c647951463b43faab5f7a7f01da59cab491b518ca9c46a023b3875a0"},"id":"0983519f-aef8-448b-8a4b-7f2e0e924845","version":3}'
_EXIT_KEYSTORE_PASSWORD="test"
EXIT_ADDRESS="${EXIT_ADDRESS:-$_EXIT_ADDRESS_DEFAULT}"
if [[ "$EXIT_ADDRESS" == "$_EXIT_ADDRESS_DEFAULT" ]]; then
    _key_dir="$PROJECT_ROOT/tmp"
    mkdir -p "$_key_dir"
    _privkey_file="$_key_dir/exit_address_privatekey.txt"
    _keystore_file="$_key_dir/exit_address.keystore"
    if [[ ! -f "$_privkey_file" ]]; then
        printf '%s\n' "$_EXIT_PRIVKEY_DEFAULT" > "$_privkey_file"
        chmod 600 "$_privkey_file"
        log_info "Exit address private key saved to: $_privkey_file"
    fi
    if [[ ! -f "$_keystore_file" ]]; then
        printf '%s\n' "$_EXIT_KEYSTORE_DEFAULT" > "$_keystore_file"
        chmod 600 "$_keystore_file"
        log_info "Exit address keystore saved to: $_keystore_file (password: $_EXIT_KEYSTORE_PASSWORD)"
    fi
fi
OUTPUT_FILE="${OUTPUT_FILE:-tmp/exit_certificate-kurtosis.json}"
# Native ETH preminted at genesis by the Kurtosis enclave (110000 ETH), discounted by Step F.
GENESIS_PREFUND_ETH_WEI="${GENESIS_PREFUND_ETH_WEI:-110000000000000000000000}"
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
ZKEVM_BRIDGE_SERVICE="${ZKEVM_BRIDGE_SERVICE_PREFIX}-${NETWORK_SUFFIX}"
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

get_agglayer_grpc_url() {
    local raw port
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$AGGLAYER_SERVICE" aglr-grpc 2>/dev/null); then
        log_error "Failed to get agglayer grpc port from service '$AGGLAYER_SERVICE' in enclave '$KURTOSIS_ENCLAVE'"
        exit 1
    fi
    # kurtosis returns grpc://host:PORT — rebuild as http://localhost:PORT (insecure gRPC)
    port=$(echo "$raw" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

get_zkevm_bridge_service_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$ZKEVM_BRIDGE_SERVICE" rpc 2>/dev/null); then
        return 1
    fi
    port_to_localhost_url "$raw"
}

# ---------------------------------------------------------------------------
# Aggkit config artifact — downloaded once, reused by multiple functions
# ---------------------------------------------------------------------------

AGGKIT_CONFIG_DIR=""

download_aggkit_config() {
    AGGKIT_CONFIG_DIR=$(mktemp -d)

    local artifact_name="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG}-${NETWORK_SUFFIX}"
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

get_bridge_address() {
    local addr
    addr=$(grep 'BridgeAddr' "$AGGKIT_CONFIG_DIR/config.toml" | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "BridgeAddr not found in config.toml"
        exit 1
    fi
    echo "$addr"
}

get_sovereign_rollup_addr() {
    local addr
    addr=$(grep -E '^\s*SovereignRollupAddr\s*=' "$AGGKIT_CONFIG_DIR/config.toml" | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    echo "$addr"
}

get_l1_global_exit_root_address() {
    local addr
    addr=$(grep -E '^\s*polygonZkEVMGlobalExitRootAddress\s*=' "$AGGKIT_CONFIG_DIR/config.toml" | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    echo "$addr"
}

# ---------------------------------------------------------------------------
# Signer / keystore helpers
# ---------------------------------------------------------------------------

# Reads the AggSenderPrivateKey password from config.toml.
get_signer_password() {
    # AggSenderPrivateKey = {Path = "...", Password = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"}
    local password
    password=$(grep 'AggSenderPrivateKey' "$AGGKIT_CONFIG_DIR/config.toml" \
        | sed -E 's/.*Password = "([^"]+)".*/\1/')
    echo "$password"
}

# Downloads the sequencer keystore file from the kurtosis artifact and writes
# it to OUTPUT_KEYSTORE_PATH. Returns 1 if not available (signer skipped).
get_sequencer_keystore() {
    local dest="$1"
    local tmp_dir
    tmp_dir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmp_dir'" RETURN

    if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE" "$tmp_dir" &>/dev/null; then
        log_warn "Artifact '$KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE' not found — signerConfig will be omitted"
        return 1
    fi

    local keystore_file
    keystore_file=$(find "$tmp_dir" -maxdepth 1 -name "*.keystore" 2>/dev/null | head -1)
    if [[ -z "$keystore_file" ]]; then
        log_warn "No *.keystore file found in artifact '$KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE' — signerConfig will be omitted"
        return 1
    fi

    cp "$keystore_file" "$dest"
    chmod 600 "$dest"
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

log_info "Downloading aggkit config artifact..."
download_aggkit_config
trap cleanup_aggkit_config EXIT

log_info "Getting bridge address from aggkit config artifact..."
BRIDGE_ADDR=$(get_bridge_address)
log_info "Bridge address: $BRIDGE_ADDR"

log_info "Getting sovereign rollup address from aggkit config artifact..."
SOVEREIGN_ROLLUP_ADDR=$(get_sovereign_rollup_addr)
if [[ -n "$SOVEREIGN_ROLLUP_ADDR" ]]; then
    log_info "SovereignRollupAddr: $SOVEREIGN_ROLLUP_ADDR"
else
    log_warn "SovereignRollupAddr not found in config.toml — threshold check will be skipped at sign time"
fi

log_info "Getting L1 GlobalExitRoot address from aggkit config artifact..."
L1_GLOBAL_EXIT_ROOT_ADDR=$(get_l1_global_exit_root_address)
if [[ -n "$L1_GLOBAL_EXIT_ROOT_ADDR" ]]; then
    log_info "L1GlobalExitRootAddress: $L1_GLOBAL_EXIT_ROOT_ADDR"
else
    log_warn "polygonZkEVMGlobalExitRootAddress not found in config.toml — l1GlobalExitRootAddress will be omitted (Step I will fail)"
fi

log_info "Getting agglayer URLs..."
AGGLAYER_ADMIN_URL=$(get_agglayer_admin_url)
AGGLAYER_GRPC_URL=$(get_agglayer_grpc_url)
log_info "Agglayer admin URL: $AGGLAYER_ADMIN_URL"
log_info "Agglayer gRPC URL:  $AGGLAYER_GRPC_URL"

log_info "Getting zkevm bridge service URL (service: $ZKEVM_BRIDGE_SERVICE)..."
ZKEVM_BRIDGE_SERVICE_URL=""
if ZKEVM_BRIDGE_SERVICE_URL=$(get_zkevm_bridge_service_url); then
    log_info "zkevm bridge service URL: $ZKEVM_BRIDGE_SERVICE_URL"
else
    log_warn "Service '$ZKEVM_BRIDGE_SERVICE' not found in enclave — bridgeServiceURL will be omitted (Step E bridge service check skipped)"
fi

mkdir -p "$(dirname "$OUTPUT_PATH")"
OUTPUT_DIR="$(dirname "$OUTPUT_PATH")"

# ---------------------------------------------------------------------------
# Signer config
# ---------------------------------------------------------------------------
SIGNER_CONFIG_BLOCK=""
KEYSTORE_DEST="$OUTPUT_DIR/sequencer.keystore"

log_info "Getting signer keystore from artifact '$KURTOSIS_ARTIFACT_SEQUENCER_KEYSTORE'..."
if get_sequencer_keystore "$KEYSTORE_DEST"; then
    SIGNER_PASSWORD=$(get_signer_password)
    if [[ -z "$SIGNER_PASSWORD" ]]; then
        log_warn "AggSenderPrivateKey password not found in config.toml — signerConfig will be omitted"
        rm -f "$KEYSTORE_DEST"
    else
        KEYSTORE_RELATIVE="sequencer.keystore"
        log_info "Keystore saved to: $KEYSTORE_DEST"
        log_info "Signer password:   (extracted from config.toml)"
        # Include trailing newline so the heredoc renders cleanly when the block is present
        SIGNER_CONFIG_BLOCK="    \"signerConfig\": {
        \"Method\": \"local\",
        \"Path\": \"$KEYSTORE_RELATIVE\",
        \"Password\": \"$SIGNER_PASSWORD\"
    },
"
    fi
fi

SOVEREIGN_ROLLUP_LINE=""
if [[ -n "$SOVEREIGN_ROLLUP_ADDR" ]]; then
    SOVEREIGN_ROLLUP_LINE="    \"sovereignRollupAddr\": \"$SOVEREIGN_ROLLUP_ADDR\",
"
fi

L1_GLOBAL_EXIT_ROOT_LINE=""
if [[ -n "$L1_GLOBAL_EXIT_ROOT_ADDR" ]]; then
    L1_GLOBAL_EXIT_ROOT_LINE="    \"l1GlobalExitRootAddress\": \"$L1_GLOBAL_EXIT_ROOT_ADDR\",
"
fi

BRIDGE_SERVICE_OPTS=""
if [[ -n "$ZKEVM_BRIDGE_SERVICE_URL" ]]; then
    BRIDGE_SERVICE_OPTS="        \"bridgeServiceURL\": \"$ZKEVM_BRIDGE_SERVICE_URL\",
        \"bridgeServiceType\": \"zkevm\""
fi

cat > "$OUTPUT_PATH" <<EOF
{
    "l2RpcUrl": "$L2_RPC_URL",
    "l1RpcUrl": "$L1_RPC_URL",
    "l2BridgeAddress": "$BRIDGE_ADDR",
    "l1BridgeAddress": "$BRIDGE_ADDR",
    "l2NetworkId": $NETWORK_INDEX,
    "targetBlock": "LatestBlock",
    "exitAddress": "$EXIT_ADDRESS",
    "destinationNetwork": 0,
${SOVEREIGN_ROLLUP_LINE}${L1_GLOBAL_EXIT_ROOT_LINE}${SIGNER_CONFIG_BLOCK}    "options": {
        "blockRange": 5000,
        "concurrencyLimit": 20,
        "rpcBatchSize": 200,
        "rpcDelayMs": 0,
        "outputDir": "./output-kurtosis",
        "l1StartBlock": 0,
        "agglayerAdminURL": "$AGGLAYER_ADMIN_URL",
        "agglayerClient": { "GRPC": { "URL": "$AGGLAYER_GRPC_URL" } },
        "ignoreGenesisBalance": true,
        "genesisPrefundETHWei": "$GENESIS_PREFUND_ETH_WEI",
        "ignoreBalanceMismatch": true,
        "ignoreUnsupportedL2Events": true${BRIDGE_SERVICE_OPTS:+,
$BRIDGE_SERVICE_OPTS}
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
        mkdir -p "$(dirname "$launch_file")"
        cat > "$launch_file" <<LAUNCH
{
    "version": "0.2.0",
    "configurations": [
    ]
}
LAUNCH
        log_info "Created .vscode/launch.json"
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

# ---------------------------------------------------------------------------
# Suggest clearing previous output
# ---------------------------------------------------------------------------

OUTPUT_KURTOSIS_DIR="$PROJECT_ROOT/tmp/output-kurtosis"
if [[ -d "$OUTPUT_KURTOSIS_DIR" ]]; then
    log_warn "Previous output directory exists: $OUTPUT_KURTOSIS_DIR"
    log_warn "Consider removing it before running the tool to avoid stale intermediate files:"
    log_warn "  rm -rf \"$OUTPUT_KURTOSIS_DIR\""
fi
