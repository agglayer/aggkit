#!/usr/bin/env bash
# Bridges ETH (or an ERC-20) from L1 to L2 by calling bridgeAsset on the L1 bridge
# contract in a running Kurtosis enclave. Requires: kurtosis, cast (Foundry).
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

Bridges ETH from L1 to L2 by calling bridgeAsset on the L1 bridge contract.

Arguments:
  NETWORK_INDEX   L2 network index to target (default: 1)

Options:
  -e, --enclave   ENCLAVE      Kurtosis enclave name (default: \$KURTOSIS_ENCLAVE or "op")
  -a, --amount    AMOUNT_WEI   Amount to bridge in wei (default: 1234567890)
  -d, --dest      ADDRESS      Destination address on L2 (default: sender address)
  -t, --token     ADDRESS      ERC-20 token address to bridge (default: 0x0 = native ETH)
  -k, --key       PRIVATE_KEY  Sender private key (default: \$PRIVATE_KEY or Foundry test key)
  -w, --wait                   Wait for the deposit to be claimed on L2 (polls isClaimed)
  -h, --help                   Show this help

Environment variables (override defaults):
  KURTOSIS_ENCLAVE                  Enclave name
  PRIVATE_KEY                       Sender private key
  BRIDGE_AMOUNT                     Amount in wei
  DEST_ADDRESS                      Destination address on L2
  TOKEN_ADDRESS                     ERC-20 token address (0x0 for ETH)
  L1_SERVICE                        Kurtosis L1 execution service (default: el-1-geth-lighthouse)
  L2_SERVICE_PREFIX                 Kurtosis L2 execution service prefix (default: op-el-1-op-geth-op-node)
  KURTOSIS_ARTIFACT_AGGKIT_CONFIG   Aggkit config artifact name (default: aggkit-config)

Examples:
  $0                              # Bridge 0.01 ETH to network 1
  $0 2                            # Bridge to network 2
  $0 --amount 1000000000000000000 # Bridge 1 ETH
  $0 --dest 0xABCD... --wait      # Bridge to specific address and wait for claim
  PRIVATE_KEY=0x... $0 1
EOF
    exit 1
}

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------

KURTOSIS_ENCLAVE="${KURTOSIS_ENCLAVE:-aggkit}"
KURTOSIS_ARTIFACT_AGGKIT_CONFIG="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG:-aggkit-config}"
L1_SERVICE="${L1_SERVICE:-el-1-geth-lighthouse}"
L2_SERVICE_PREFIX="${L2_SERVICE_PREFIX:-op-el-1-op-geth-op-node}"

# Kurtosis L1 faucet (1,000,000,000 ETH on L1 in local enclaves).
# Override with PRIVATE_KEY env var or --key flag for other environments.
PRIVATE_KEY="${PRIVATE_KEY:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"

BRIDGE_AMOUNT="${BRIDGE_AMOUNT:-1234567890}"

# address(0) = native ETH
TOKEN_ADDRESS="${TOKEN_ADDRESS:-0x0000000000000000000000000000000000000000}"

DEST_ADDRESS="${DEST_ADDRESS:-}"
NETWORK_INDEX=1
WAIT_FOR_CLAIM=false

# ---------------------------------------------------------------------------
# Parse flags and positional args
# ---------------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -e|--enclave)
            [[ $# -lt 2 ]] && { log_error "--enclave requires a value"; usage; }
            KURTOSIS_ENCLAVE="$2"; shift 2 ;;
        -a|--amount)
            [[ $# -lt 2 ]] && { log_error "--amount requires a value"; usage; }
            BRIDGE_AMOUNT="$2"; shift 2 ;;
        -d|--dest)
            [[ $# -lt 2 ]] && { log_error "--dest requires a value"; usage; }
            DEST_ADDRESS="$2"; shift 2 ;;
        -t|--token)
            [[ $# -lt 2 ]] && { log_error "--token requires a value"; usage; }
            TOKEN_ADDRESS="$2"; shift 2 ;;
        -k|--key)
            [[ $# -lt 2 ]] && { log_error "--key requires a value"; usage; }
            PRIVATE_KEY="$2"; shift 2 ;;
        -w|--wait)
            WAIT_FOR_CLAIM=true; shift ;;
        [0-9]*)
            NETWORK_INDEX="$1"; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

NETWORK_SUFFIX=$(printf '%03d' "$NETWORK_INDEX")
L2_SERVICE="${L2_SERVICE_PREFIX}-${NETWORK_SUFFIX}"

# ---------------------------------------------------------------------------
# Dependency checks
# ---------------------------------------------------------------------------

check_deps() {
    local missing=()
    command -v kurtosis &>/dev/null || missing+=("kurtosis")
    command -v cast     &>/dev/null || missing+=("cast (foundry)")
    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required tools: ${missing[*]}"
        log_error "Install Foundry: https://getfoundry.sh"
        exit 1
    fi
}

# ---------------------------------------------------------------------------
# Kurtosis helpers
# ---------------------------------------------------------------------------

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
        log_error "To use a different prefix, set L2_SERVICE_PREFIX"
        exit 1
    fi
    port_to_localhost_url "$raw"
}

get_bridge_address() {
    local tmp_dir
    tmp_dir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmp_dir'" RETURN

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
# bridgeAsset ABI
#
# function bridgeAsset(
#   uint32  destinationNetwork,
#   address destinationAddress,
#   uint256 amount,
#   address token,
#   bool    forceUpdate,
#   bytes   calldata permitData
# ) external payable
# ---------------------------------------------------------------------------

# wait_for_claim polls isClaimed(depositCount, sourceBridgeNetwork=0) on the L2 bridge.
# depositCount is extracted from the DepositCount field of the BridgeEvent emitted in the tx.
wait_for_claim() {
    local l2_rpc="$1"
    local l2_bridge="$2"
    local deposit_count="$3"
    local timeout_secs="${4:-300}"
    local poll_secs=5

    # isClaimed(uint32 leafIndex, uint32 sourceBridgeNetwork) — selector: 0xcc461632
    local leaf_idx_hex
    local src_net_hex
    leaf_idx_hex=$(printf '%064x' "$deposit_count")
    src_net_hex=$(printf '%064x' 0)
    local calldata="0xcc461632${leaf_idx_hex}${src_net_hex}"

    log_info "Waiting for claim on L2 (depositCount=$deposit_count, timeout=${timeout_secs}s)..."

    local elapsed=0
    while [[ $elapsed -lt $timeout_secs ]]; do
        local result
        result=$(cast call --rpc-url "$l2_rpc" "$l2_bridge" "$calldata" 2>/dev/null || true)
        # isClaimed returns a non-zero uint256 when claimed
        local val
        val=$(cast --to-dec "${result:-0x0}" 2>/dev/null || echo "0")
        if [[ "$val" != "0" ]]; then
            log_info "Deposit claimed on L2! (depositCount=$deposit_count)"
            return 0
        fi
        sleep "$poll_secs"
        elapsed=$((elapsed + poll_secs))
        log_info "  Still waiting... ${elapsed}s / ${timeout_secs}s"
    done

    log_warn "Timed out waiting for claim after ${timeout_secs}s"
    return 1
}

# extract_deposit_count parses the DepositCount from a BridgeEvent log in a tx receipt.
# BridgeEvent topic: keccak256("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)")
extract_deposit_count() {
    local tx_hash="$1"
    local l1_rpc="$2"

    local bridge_event_topic="0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b"

    local receipt
    receipt=$(cast receipt --rpc-url "$l1_rpc" "$tx_hash" --json 2>/dev/null || true)
    if [[ -z "$receipt" ]]; then
        log_warn "Could not fetch receipt for $tx_hash — skipping claim wait"
        echo ""
        return
    fi

    # Find the BridgeEvent log and decode depositCount from the ABI-encoded data.
    # Layout (32-byte words): leafType | originNetwork | originAddress | destNetwork |
    #                          destAddress | amount | metadataOffset | depositCount | ...
    local data
    data=$(echo "$receipt" | python3 -c "
import sys, json
receipt = json.load(sys.stdin)
topic = '$bridge_event_topic'
for log in receipt.get('logs', []):
    if log.get('topics', [None])[0] == topic:
        print(log.get('data', ''))
        break
" 2>/dev/null || true)

    if [[ -z "$data" ]]; then
        log_warn "BridgeEvent log not found in tx $tx_hash"
        echo ""
        return
    fi

    # depositCount is the 8th 32-byte word (offset 7*32=224 bytes, 0x prefix stripped)
    local hex_data="${data#0x}"
    local deposit_count_hex="${hex_data:448:64}"  # word 7 (0-indexed): 7*64=448 chars
    local deposit_count
    deposit_count=$(python3 -c "print(int('$deposit_count_hex', 16))" 2>/dev/null || echo "")
    echo "$deposit_count"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

check_deps

log_info "Enclave:          $KURTOSIS_ENCLAVE"
log_info "Network index:    $NETWORK_INDEX (suffix: $NETWORK_SUFFIX)"
log_info "L1 service:       $L1_SERVICE"
log_info "L2 service:       $L2_SERVICE"
log_info "Amount:           $BRIDGE_AMOUNT wei"
log_info "Token:            $TOKEN_ADDRESS"

log_info "Getting L1 RPC URL..."
L1_RPC_URL=$(get_l1_rpc_url)
log_info "L1 RPC URL: $L1_RPC_URL"

log_info "Getting bridge address from aggkit config artifact..."
BRIDGE_ADDR=$(get_bridge_address)
log_info "Bridge address: $BRIDGE_ADDR"

# Derive sender address from private key
SENDER_ADDR=$(cast wallet address --private-key "$PRIVATE_KEY")
log_info "Sender address: $SENDER_ADDR"

# Use sender as destination if not specified
if [[ -z "$DEST_ADDRESS" ]]; then
    DEST_ADDRESS="$SENDER_ADDR"
fi
log_info "Destination:      $DEST_ADDRESS (network $NETWORK_INDEX)"

# Check sender balance
SENDER_BALANCE=$(cast balance --rpc-url "$L1_RPC_URL" "$SENDER_ADDR")
SENDER_BALANCE_ETH=$(cast --from-wei "$SENDER_BALANCE" ether)
log_info "Sender L1 balance: $SENDER_BALANCE_ETH ETH ($SENDER_BALANCE wei)"

IS_ETH_BRIDGE="false"
if [[ "$TOKEN_ADDRESS" == "0x0000000000000000000000000000000000000000" ]]; then
    IS_ETH_BRIDGE="true"
fi

# bash integer arithmetic overflows for wei values > 2^63; use python3 for the comparison.
if [[ "$IS_ETH_BRIDGE" == "true" ]] && python3 -c "import sys; sys.exit(0 if int('$SENDER_BALANCE') < int('$BRIDGE_AMOUNT') else 1)"; then
    BRIDGE_AMOUNT_ETH=$(cast --from-wei "$BRIDGE_AMOUNT" ether)
    log_error "Insufficient balance: sender has $SENDER_BALANCE_ETH ETH, needs $BRIDGE_AMOUNT_ETH ETH"
    exit 1
fi

# ---------------------------------------------------------------------------
# For ERC-20: approve the bridge contract first
# ---------------------------------------------------------------------------

if [[ "$IS_ETH_BRIDGE" != "true" ]]; then
    log_info "ERC-20 bridge: approving bridge contract to spend $BRIDGE_AMOUNT of $TOKEN_ADDRESS..."
    APPROVE_TX=$(cast send \
        --rpc-url "$L1_RPC_URL" \
        --private-key "$PRIVATE_KEY" \
        "$TOKEN_ADDRESS" \
        "approve(address,uint256)" \
        "$BRIDGE_ADDR" \
        "$BRIDGE_AMOUNT")
    log_info "Approve tx: $APPROVE_TX"
fi

# ---------------------------------------------------------------------------
# Call bridgeAsset
#
#   bridgeAsset(
#     uint32  destinationNetwork,   → NETWORK_INDEX
#     address destinationAddress,   → DEST_ADDRESS
#     uint256 amount,               → BRIDGE_AMOUNT
#     address token,                → TOKEN_ADDRESS (0x0 for ETH)
#     bool    forceUpdate,          → true  (forces local exit root update)
#     bytes   permitData            → 0x    (empty)
#   )
#   msg.value = BRIDGE_AMOUNT for ETH; 0 for ERC-20
# ---------------------------------------------------------------------------

log_info "Calling bridgeAsset on L1 bridge..."

if [[ "$IS_ETH_BRIDGE" == "true" ]]; then
    TX_HASH=$(cast send \
        --rpc-url "$L1_RPC_URL" \
        --private-key "$PRIVATE_KEY" \
        --value "$BRIDGE_AMOUNT" \
        --json \
        "$BRIDGE_ADDR" \
        "bridgeAsset(uint32,address,uint256,address,bool,bytes)" \
        "$NETWORK_INDEX" \
        "$DEST_ADDRESS" \
        "$BRIDGE_AMOUNT" \
        "0x0000000000000000000000000000000000000000" \
        true \
        "0x" | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")
else
    TX_HASH=$(cast send \
        --rpc-url "$L1_RPC_URL" \
        --private-key "$PRIVATE_KEY" \
        --json \
        "$BRIDGE_ADDR" \
        "bridgeAsset(uint32,address,uint256,address,bool,bytes)" \
        "$NETWORK_INDEX" \
        "$DEST_ADDRESS" \
        "$BRIDGE_AMOUNT" \
        "$TOKEN_ADDRESS" \
        true \
        "0x" | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")
fi

log_info "Bridge tx hash: $TX_HASH"

log_info "Waiting for receipt..."
TX_STATUS=$(cast receipt --rpc-url "$L1_RPC_URL" "$TX_HASH" status 2>/dev/null || true)
TX_BLOCK=$(cast receipt --rpc-url "$L1_RPC_URL" "$TX_HASH" blockNumber 2>/dev/null || true)
if [[ -z "$TX_STATUS" ]]; then
    log_warn "Could not fetch receipt for $TX_HASH"
elif [[ "$TX_STATUS" == *"success"* ]]; then
    log_info "Receipt: status=success blockNumber=$TX_BLOCK"
else
    log_error "Receipt: status=REVERTED blockNumber=$TX_BLOCK"
    log_error "Replaying transaction to get revert reason..."
    cast run --rpc-url "$L1_RPC_URL" "$TX_HASH" 2>&1 | grep -E "revert|Revert|error|Error|←" | head -20 >&2
    exit 1
fi

log_info "Bridge from L1 to L2 network $NETWORK_INDEX submitted successfully."
log_info "  Sender:       $SENDER_ADDR"
log_info "  Destination:  $DEST_ADDRESS"
log_info "  Amount:       $BRIDGE_AMOUNT wei"
log_info "  Token:        $TOKEN_ADDRESS"
log_info "  Bridge:       $BRIDGE_ADDR"

# ---------------------------------------------------------------------------
# Optionally wait for the deposit to be auto-claimed on L2
# ---------------------------------------------------------------------------

if [[ "$WAIT_FOR_CLAIM" == "true" ]]; then
    log_info "Getting L2 RPC URL..."
    L2_RPC_URL=$(get_l2_rpc_url)
    log_info "L2 RPC URL: $L2_RPC_URL"

    DEPOSIT_COUNT=$(extract_deposit_count "$TX_HASH" "$L1_RPC_URL")
    if [[ -n "$DEPOSIT_COUNT" ]]; then
        wait_for_claim "$L2_RPC_URL" "$BRIDGE_ADDR" "$DEPOSIT_COUNT" 300
    else
        log_warn "Could not determine depositCount — skipping claim check"
    fi
fi
