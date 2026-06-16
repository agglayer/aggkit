#!/usr/bin/env bash
# Bridges ETH (or an ERC-20) from L1 to L2 by calling bridgeAsset on the L1 bridge
# contract. Connection info is taken entirely from the environment — this script
# never talks to Kurtosis. Populate the variables first with:
#   source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
# Requires: cast (Foundry).
#
# Required environment variables:
#   L1_RPC_URL    L1 JSON-RPC URL
#   BRIDGE_ADDR   Bridge contract address on L1
#   L2_RPC_URL    L2 JSON-RPC URL (only required with --wait)
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
Connection info comes from the environment (see export_kurtosis_env.sh).

Arguments:
  NETWORK_INDEX   Destination L2 network index (default: 1)

Options:
  -a, --amount    AMOUNT_WEI   Amount to bridge in wei (default: 1234567890)
  -d, --dest      ADDRESS      Destination address on L2 (default: sender address)
  -t, --token     ADDRESS      ERC-20 token address to bridge (default: 0x0 = native ETH)
  -k, --key       PRIVATE_KEY  Sender private key (default: \$PRIVATE_KEY or Foundry test key)
  -w, --wait                   Wait for the deposit to be claimed on L2 (polls isClaimed)
  -T, --timeout   SECS         Seconds to wait for the claim with --wait (default: 300)
  -h, --help                   Show this help

Required environment variables:
  L1_RPC_URL                  L1 JSON-RPC URL
  BRIDGE_ADDR                 Bridge contract address on L1
  L2_RPC_URL                  L2 JSON-RPC URL (only required with --wait)
  Tip: source <(tools/exit_certificate/scripts/export_kurtosis_env.sh NETWORK_INDEX)

Optional environment variables (override defaults):
  PRIVATE_KEY                 Sender private key
  DEST_ADDRESS                Destination address on L2
  TOKEN_ADDRESS               ERC-20 token address (0x0 for ETH)

Examples:
  source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
  $0                              # Bridge 0.01 ETH to network 1
  $0 2                            # Bridge to network 2
  $0 --amount 1000000000000000000 # Bridge 1 ETH
  $0 --dest 0xABCD... --wait      # Bridge to specific address and wait for claim
EOF
    exit 1
}

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------

# Default sender is the Kurtosis L1 faucet (1,000,000,000 ETH on L1 in local
# enclaves). Override with PRIVATE_KEY env var or --key flag for other environments.
PRIVATE_KEY="${PRIVATE_KEY:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"

# Default amount in wei; override with --amount on the command line.
BRIDGE_AMOUNT="1234567890"

# address(0) = native ETH
TOKEN_ADDRESS="${TOKEN_ADDRESS:-0x0000000000000000000000000000000000000000}"

DEST_ADDRESS="${DEST_ADDRESS:-}"
NETWORK_INDEX=1
WAIT_FOR_CLAIM=false
CLAIM_TIMEOUT_SECS=300

# Connection info: provided by the environment (e.g. via export_kurtosis_env.sh).
L1_RPC_URL="${L1_RPC_URL:-}"
L2_RPC_URL="${L2_RPC_URL:-}"
BRIDGE_ADDR="${BRIDGE_ADDR:-}"

# ---------------------------------------------------------------------------
# Parse flags and positional args
# ---------------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
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
        -T|--timeout)
            [[ $# -lt 2 ]] && { log_error "--timeout requires a value"; usage; }
            [[ "$2" =~ ^[0-9]+$ ]] || { log_error "--timeout must be a positive integer (seconds)"; usage; }
            CLAIM_TIMEOUT_SECS="$2"; shift 2 ;;
        [0-9]*)
            NETWORK_INDEX="$1"; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

# ---------------------------------------------------------------------------
# Dependency and environment checks
# ---------------------------------------------------------------------------

check_deps() {
    if ! command -v cast &>/dev/null; then
        log_error "Missing required tool: cast (foundry)"
        log_error "Install Foundry: https://getfoundry.sh"
        exit 1
    fi
}

check_env() {
    local missing=()
    [[ -z "$L1_RPC_URL" ]]  && missing+=("L1_RPC_URL")
    [[ -z "$BRIDGE_ADDR" ]] && missing+=("BRIDGE_ADDR")
    [[ "$WAIT_FOR_CLAIM" == "true" && -z "$L2_RPC_URL" ]] && missing+=("L2_RPC_URL (required with --wait)")
    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required environment variables: ${missing[*]}"
        log_error "Populate them with: source <(tools/exit_certificate/scripts/export_kurtosis_env.sh $NETWORK_INDEX)"
        exit 1
    fi
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
        log_info "  Still waiting auto-claim (depositCount=$deposit_count)... ${elapsed}s / ${timeout_secs}s"
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
check_env

log_info "Network index:    $NETWORK_INDEX"
log_info "Amount:           $BRIDGE_AMOUNT wei"
log_info "Token:            $TOKEN_ADDRESS"
log_info "L1 RPC URL:       $L1_RPC_URL"
log_info "Bridge address:   $BRIDGE_ADDR"

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

# The sender always needs some L1 gas-token balance to pay fees, regardless of
# whether it bridges native ETH or an ERC-20. A balance of 0 is the usual cause
# of "gas required exceeds allowance (0)".
if [[ "$SENDER_BALANCE" == "0" ]]; then
    log_error "Sender has 0 balance on L1 — cannot pay transaction fees."
    log_error "  Sender: $SENDER_ADDR"
    log_error "This causes 'gas required exceeds allowance (0)'. Use an L1-funded"
    log_error "account with --key (or \$PRIVATE_KEY), or fund this address on L1 first."
    exit 1
fi

# Estimate the gas headroom the tx needs so we can fail early with a clear message
# instead of a raw 'gas required exceeds allowance (0)' from the node. bridgeAsset
# (plus an approve for ERC-20) fits comfortably under ~300k gas.
GAS_PRICE=$(cast gas-price --rpc-url "$L1_RPC_URL" 2>/dev/null || echo "0")
GAS_RESERVE=$(python3 -c "print($GAS_PRICE * 300000)" 2>/dev/null || echo "0")

# bash integer arithmetic overflows for wei values > 2^63; use python3 for comparisons.
if [[ "$IS_ETH_BRIDGE" == "true" ]]; then
    # Native bridge: balance must cover amount AND leave gas headroom. balance == amount
    # passes a naive `< amount` check but leaves nothing for gas → allowance (0).
    if python3 -c "import sys; sys.exit(0 if int('$SENDER_BALANCE') < int('$BRIDGE_AMOUNT') + int('$GAS_RESERVE') else 1)"; then
        BRIDGE_AMOUNT_ETH=$(cast --from-wei "$BRIDGE_AMOUNT" ether)
        GAS_RESERVE_ETH=$(cast --from-wei "$GAS_RESERVE" ether)
        log_error "Insufficient balance: sender has $SENDER_BALANCE_ETH ETH, needs $BRIDGE_AMOUNT_ETH ETH"
        log_error "  plus ~$GAS_RESERVE_ETH ETH for gas (amount + gas must fit in the balance)."
        exit 1
    fi
elif python3 -c "import sys; sys.exit(0 if int('$SENDER_BALANCE') < int('$GAS_RESERVE') else 1)"; then
    # ERC-20 bridge: the token amount is spent from the token contract, but the
    # sender still needs native gas-token balance for approve + bridgeAsset.
    GAS_RESERVE_ETH=$(cast --from-wei "$GAS_RESERVE" ether)
    log_error "Insufficient gas-token balance: sender has $SENDER_BALANCE_ETH ETH,"
    log_error "  needs ~$GAS_RESERVE_ETH ETH to cover approve + bridgeAsset gas on L1."
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
    echo "---- execution ---- " 
    cast run --rpc-url "$L1_RPC_URL" "$TX_HASH" 2>&1
    echo "---- revert reason ---- "  
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
    log_info "L2 RPC URL: $L2_RPC_URL"

    DEPOSIT_COUNT=$(extract_deposit_count "$TX_HASH" "$L1_RPC_URL")
    if [[ -n "$DEPOSIT_COUNT" ]]; then
        wait_for_claim "$L2_RPC_URL" "$BRIDGE_ADDR" "$DEPOSIT_COUNT" "$CLAIM_TIMEOUT_SECS"
    else
        log_warn "Could not determine depositCount — skipping claim check"
    fi
fi
