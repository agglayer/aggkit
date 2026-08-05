#!/usr/bin/env bash
# Claims a pending L2 -> L1 bridge withdrawal by calling claimAsset/claimMessage on the
# L1 bridge contract, using the Merkle proof served by the source network's aggkit
# bridge service (the same data source autoclaim uses to claim automatically).
# Useful when auto-claim is disabled/not running and a withdrawal made with
# bridge_l2_to_l1.sh never shows up as claimed on L1.
#
# A L2 -> L1 withdrawal can only be claimed once the certificate covering the L2
# block it was included in has SETTLED on L1 (VerifyBatchesTrustedAggregator) and
# the resulting rollup exit root has been injected into the L1 Info Tree — unlike
# an L1 -> L2 deposit, which only needs a GER injection.
#
# Connection info is taken entirely from the environment. Populate it with:
#   source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
#   source <(tools/exit_certificate/scripts/export_e2e_env.sh 1)
# BRIDGE_SERVICE_URL is not covered by those helpers; set it to the source L2
# network's aggkit bridge service REST API (e.g. http://localhost:11577) — the
# same instance also syncs the L1 Info Tree, so it serves L2 -> L1 proofs too.
#
# Requires: cast (Foundry), python3.
#
# Required environment variables:
#   L1_RPC_URL          L1 JSON-RPC URL (where claimAsset/claimMessage is sent)
#   BRIDGE_ADDR         Bridge contract address (same address on L1 and L2)
#   BRIDGE_SERVICE_URL  aggkit bridge service REST API for the source L2 network
#   L2_RPC_URL          L2 JSON-RPC URL (only required with --tx-hash)
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
Usage: $0 (-c DEPOSIT_COUNT | -x TX_HASH) [OPTIONS] [L2_NETWORK_INDEX]

Claims a pending L2 -> L1 bridge withdrawal on the L1 bridge contract, fetching
the Merkle proof from the source L2 network's aggkit bridge service.

Arguments:
  L2_NETWORK_INDEX             Source L2 network index (default: 1). Used as
                                 network_id when querying the bridge service and
                                 as sourceBridgeNetwork when checking isClaimed.

Options:
  -c, --deposit-count N        L2 deposit count to claim (mutually exclusive with --tx-hash)
  -x, --tx-hash       HASH     L2 bridge tx hash to extract the deposit count from
                                (requires L2_RPC_URL; typically the hash printed by
                                bridge_l2_to_l1.sh)
  -k, --key           PRIVATE_KEY  Sender private key used to submit the claim tx
                                    (default: \$PRIVATE_KEY or Foundry test key — claimAsset/
                                    claimMessage can be called by anyone, not just the depositor)
  -h, --help                   Show this help

Required environment variables:
  L1_RPC_URL                  L1 JSON-RPC URL (where the claim tx is sent)
  BRIDGE_ADDR                 Bridge contract address (same on L1 and L2)
  BRIDGE_SERVICE_URL           aggkit bridge service REST API for the source L2 network
  L2_RPC_URL                   L2 JSON-RPC URL (only required with --tx-hash)
  Tip: source <(tools/exit_certificate/scripts/export_kurtosis_env.sh NETWORK_INDEX)
  Tip: source <(tools/exit_certificate/scripts/export_e2e_env.sh NETWORK_INDEX)

Optional environment variables (override defaults):
  PRIVATE_KEY                  Sender private key
  GAS_PRICE_WEI                 Max fee per gas for the claim tx (default: 5000000000)
  PRIORITY_GAS_PRICE_WEI        Max priority fee per gas for the claim tx (default: 5000000000)

Examples:
  source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
  export BRIDGE_SERVICE_URL=http://localhost:11577
  $0 --tx-hash 0xabc...           # claim the withdrawal made by that L2 tx
  $0 --deposit-count 2            # claim L2 deposit_count=2 directly
  $0 --deposit-count 2 2          # same, but source network is L2 network 2

Note: this only works once the certificate covering the withdrawal has settled
on L1 and its rollup exit root has been injected into the L1 Info Tree. If it
hasn't, the bridge service reports "has not been included on the L1 Info Tree yet".
EOF
    exit 1
}

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------

# Default sender is the Kurtosis L1 faucet. Override with PRIVATE_KEY env var or
# --key flag for other environments. claimAsset/claimMessage have no access
# control, so this key only pays for gas — it need not be the depositor's.
PRIVATE_KEY="${PRIVATE_KEY:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"

DEPOSIT_COUNT=""
TX_HASH=""

# Source is the L2 network the withdrawal came from; destination is always L1.
SOURCE_NETWORK=1
DEST_NETWORK=0

# Same rationale as bridge_l1_to_l2.sh / bridge_l2_to_l1.sh: a freshly started local
# node's gas price oracle answers near-zero values until it has seen a few blocks,
# which can leave the claim tx permanently stuck in the mempool ("already known" on
# retry). Override for environments with a real fee market.
GAS_PRICE_WEI="${GAS_PRICE_WEI:-5000000000}"
PRIORITY_GAS_PRICE_WEI="${PRIORITY_GAS_PRICE_WEI:-5000000000}"

# Connection info: provided by the environment (e.g. via export_kurtosis_env.sh / export_e2e_env.sh).
L1_RPC_URL="${L1_RPC_URL:-}"
L2_RPC_URL="${L2_RPC_URL:-}"
BRIDGE_ADDR="${BRIDGE_ADDR:-}"
BRIDGE_SERVICE_URL="${BRIDGE_SERVICE_URL:-}"

# ---------------------------------------------------------------------------
# Parse flags and positional args
# ---------------------------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -c|--deposit-count)
            [[ $# -lt 2 ]] && { log_error "--deposit-count requires a value"; usage; }
            DEPOSIT_COUNT="$2"; shift 2 ;;
        -x|--tx-hash)
            [[ $# -lt 2 ]] && { log_error "--tx-hash requires a value"; usage; }
            TX_HASH="$2"; shift 2 ;;
        -k|--key)
            [[ $# -lt 2 ]] && { log_error "--key requires a value"; usage; }
            PRIVATE_KEY="$2"; shift 2 ;;
        [0-9]*)
            SOURCE_NETWORK="$1"; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

if [[ -n "$DEPOSIT_COUNT" && -n "$TX_HASH" ]]; then
    log_error "--deposit-count and --tx-hash are mutually exclusive"
    usage
fi
if [[ -z "$DEPOSIT_COUNT" && -z "$TX_HASH" ]]; then
    log_error "One of --deposit-count or --tx-hash is required"
    usage
fi

# ---------------------------------------------------------------------------
# Dependency and environment checks
# ---------------------------------------------------------------------------

check_deps() {
    if ! command -v cast &>/dev/null; then
        log_error "Missing required tool: cast (foundry)"
        log_error "Install Foundry: https://getfoundry.sh"
        exit 1
    fi
    if ! command -v python3 &>/dev/null; then
        log_error "Missing required tool: python3"
        exit 1
    fi
}

check_env() {
    local missing=()
    [[ -z "$L1_RPC_URL" ]]          && missing+=("L1_RPC_URL")
    [[ -z "$BRIDGE_ADDR" ]]         && missing+=("BRIDGE_ADDR")
    [[ -z "$BRIDGE_SERVICE_URL" ]]  && missing+=("BRIDGE_SERVICE_URL")
    [[ -n "$TX_HASH" && -z "$L2_RPC_URL" ]] && missing+=("L2_RPC_URL (required with --tx-hash)")
    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required environment variables: ${missing[*]}"
        log_error "Populate them with: source <(tools/exit_certificate/scripts/export_kurtosis_env.sh $SOURCE_NETWORK)"
        log_error "                or: source <(tools/exit_certificate/scripts/export_e2e_env.sh $SOURCE_NETWORK)"
        log_error "BRIDGE_SERVICE_URL is not covered by those helpers — set it to the source L2"
        log_error "network's aggkit bridge service REST API (e.g. http://localhost:11577)."
        exit 1
    fi
}

# ---------------------------------------------------------------------------
# extract_deposit_count parses the DepositCount from a BridgeEvent log in a tx receipt.
# BridgeEvent topic: keccak256("BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32)")
# ---------------------------------------------------------------------------

extract_deposit_count() {
    local tx_hash="$1"
    local rpc="$2"

    local bridge_event_topic="0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b"

    local receipt
    receipt=$(cast receipt --rpc-url "$rpc" "$tx_hash" --json 2>/dev/null || true)
    if [[ -z "$receipt" ]]; then
        log_error "Could not fetch receipt for $tx_hash"
        exit 1
    fi

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
        log_error "BridgeEvent log not found in tx $tx_hash"
        exit 1
    fi

    # depositCount is the 8th 32-byte word (offset 7*32=224 bytes, 0x prefix stripped)
    local hex_data="${data#0x}"
    local deposit_count_hex="${hex_data:448:64}"
    python3 -c "print(int('$deposit_count_hex', 16))"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

check_deps
check_env

if [[ -n "$TX_HASH" ]]; then
    log_info "Extracting deposit count from L2 tx $TX_HASH..."
    DEPOSIT_COUNT=$(extract_deposit_count "$TX_HASH" "$L2_RPC_URL")
fi

log_info "Source network:      L2 network $SOURCE_NETWORK"
log_info "Deposit count:       $DEPOSIT_COUNT"
log_info "L1 RPC URL:          $L1_RPC_URL"
log_info "Bridge address:      $BRIDGE_ADDR"
log_info "Bridge service URL:  $BRIDGE_SERVICE_URL"

# ---------------------------------------------------------------------------
# Fetch the bridge details from the source bridge service
# ---------------------------------------------------------------------------

log_info "Fetching bridge details..."
BRIDGE_HTTP_CODE=$(curl -s -o /tmp/claim_l2_to_l1_bridge.json -w "%{http_code}" \
    "${BRIDGE_SERVICE_URL}/bridge/v1/bridge-by-deposit-count?network_id=${SOURCE_NETWORK}&deposit_count=${DEPOSIT_COUNT}")
BRIDGE_JSON=$(cat /tmp/claim_l2_to_l1_bridge.json)
rm -f /tmp/claim_l2_to_l1_bridge.json

if [[ "$BRIDGE_HTTP_CODE" != "200" ]]; then
    log_error "Failed to fetch bridge (network_id=$SOURCE_NETWORK, deposit_count=$DEPOSIT_COUNT): HTTP $BRIDGE_HTTP_CODE"
    log_error "$BRIDGE_JSON"
    exit 1
fi

read -r LEAF_TYPE ORIGIN_NETWORK ORIGIN_ADDRESS DESTINATION_NETWORK DESTINATION_ADDRESS AMOUNT METADATA GLOBAL_INDEX <<<"$(
    echo "$BRIDGE_JSON" | python3 -c "
import sys, json
b = json.load(sys.stdin)
print(b['leaf_type'], b['origin_network'], b['origin_address'], b['destination_network'],
      b['destination_address'], b['amount'], b['metadata'] or '0x', b['global_index'])
"
)"

if [[ "$LEAF_TYPE" != "0" && "$LEAF_TYPE" != "1" ]]; then
    log_error "Unsupported leaf_type=$LEAF_TYPE (only asset=0 and message=1 are supported)"
    exit 1
fi
METHOD="claimAsset"
[[ "$LEAF_TYPE" == "1" ]] && METHOD="claimMessage"

log_info "  Leaf type:           $LEAF_TYPE ($METHOD)"
log_info "  Origin network:      $ORIGIN_NETWORK"
log_info "  Origin address:      $ORIGIN_ADDRESS"
log_info "  Destination network: $DESTINATION_NETWORK"
log_info "  Destination address: $DESTINATION_ADDRESS"
log_info "  Amount:              $AMOUNT wei"
log_info "  Global index:        $GLOBAL_INDEX"

# ---------------------------------------------------------------------------
# Check whether it is already claimed before doing any proof/RPC work
# ---------------------------------------------------------------------------

# isClaimed(uint32 leafIndex, uint32 sourceBridgeNetwork) — selector: 0xcc461632
LEAF_IDX_HEX=$(printf '%064x' "$DEPOSIT_COUNT")
SRC_NET_HEX=$(printf '%064x' "$SOURCE_NETWORK")
IS_CLAIMED_CALLDATA="0xcc461632${LEAF_IDX_HEX}${SRC_NET_HEX}"
IS_CLAIMED_RESULT=$(cast call --rpc-url "$L1_RPC_URL" "$BRIDGE_ADDR" "$IS_CLAIMED_CALLDATA" 2>/dev/null || echo "0x0")
IS_CLAIMED_VAL=$(cast --to-dec "${IS_CLAIMED_RESULT:-0x0}" 2>/dev/null || echo "0")
if [[ "$IS_CLAIMED_VAL" != "0" ]]; then
    log_info "Deposit count=$DEPOSIT_COUNT is already claimed on L1 — nothing to do."
    exit 0
fi

# ---------------------------------------------------------------------------
# Resolve the L1 info tree index covering this withdrawal, then fetch its proof
# ---------------------------------------------------------------------------

log_info "Resolving covering L1 info tree index..."
L1_INDEX_HTTP_CODE=$(curl -s -o /tmp/claim_l2_to_l1_index.json -w "%{http_code}" \
    "${BRIDGE_SERVICE_URL}/bridge/v1/l1-info-tree-index?network_id=${SOURCE_NETWORK}&deposit_count=${DEPOSIT_COUNT}")
L1_INDEX_BODY=$(cat /tmp/claim_l2_to_l1_index.json)
rm -f /tmp/claim_l2_to_l1_index.json

if [[ "$L1_INDEX_HTTP_CODE" != "200" ]]; then
    log_error "Failed to resolve the L1 info tree index for deposit_count=$DEPOSIT_COUNT: HTTP $L1_INDEX_HTTP_CODE"
    log_error "$L1_INDEX_BODY"
    if [[ "$L1_INDEX_BODY" == *"has not been included"* ]]; then
        log_error "The certificate covering this withdrawal has not settled on L1 yet (or its"
        log_error "rollup exit root has not been injected into the L1 Info Tree yet)."
        log_error "Wait for the covering certificate to settle (VerifyBatchesTrustedAggregator"
        log_error "on L1) and retry."
    fi
    exit 1
fi
LEAF_INDEX="$L1_INDEX_BODY"
log_info "  L1 info tree index: $LEAF_INDEX"

log_info "Fetching claim proof..."
PROOF_HTTP_CODE=$(curl -s -o /tmp/claim_l2_to_l1_proof.json -w "%{http_code}" \
    "${BRIDGE_SERVICE_URL}/bridge/v1/claim-proof?network_id=${SOURCE_NETWORK}&leaf_index=${LEAF_INDEX}&deposit_count=${DEPOSIT_COUNT}")
PROOF_JSON=$(cat /tmp/claim_l2_to_l1_proof.json)
rm -f /tmp/claim_l2_to_l1_proof.json

if [[ "$PROOF_HTTP_CODE" != "200" ]]; then
    log_error "Failed to fetch the claim proof: HTTP $PROOF_HTTP_CODE"
    log_error "$PROOF_JSON"
    exit 1
fi

read -r LOCAL_PROOF ROLLUP_PROOF MAINNET_EXIT_ROOT ROLLUP_EXIT_ROOT <<<"$(
    echo "$PROOF_JSON" | python3 -c "
import sys, json
p = json.load(sys.stdin)
print('[' + ','.join(p['proof_local_exit_root']) + ']',
      '[' + ','.join(p['proof_rollup_exit_root']) + ']',
      p['l1_info_tree_leaf']['mainnet_exit_root'],
      p['l1_info_tree_leaf']['rollup_exit_root'])
"
)"

# ---------------------------------------------------------------------------
# Submit the claim
# ---------------------------------------------------------------------------

log_info "Calling $METHOD on L1 bridge..."
CAST_OUTPUT=$(cast send \
    --rpc-url "$L1_RPC_URL" \
    --private-key "$PRIVATE_KEY" \
    --gas-price "$GAS_PRICE_WEI" \
    --priority-gas-price "$PRIORITY_GAS_PRICE_WEI" \
    --json \
    "$BRIDGE_ADDR" \
    "${METHOD}(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)" \
    "$LOCAL_PROOF" \
    "$ROLLUP_PROOF" \
    "$GLOBAL_INDEX" \
    "$MAINNET_EXIT_ROOT" \
    "$ROLLUP_EXIT_ROOT" \
    "$ORIGIN_NETWORK" \
    "$ORIGIN_ADDRESS" \
    "$DESTINATION_NETWORK" \
    "$DESTINATION_ADDRESS" \
    "$AMOUNT" \
    "$METADATA" 2>&1) || {
    log_error "$METHOD failed to submit:"
    log_error "$CAST_OUTPUT"
    exit 1
}
TX_HASH_OUT=$(echo "$CAST_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['transactionHash'])")

log_info "Claim tx hash: $TX_HASH_OUT"

log_info "Waiting for receipt..."
TX_STATUS=$(cast receipt --rpc-url "$L1_RPC_URL" "$TX_HASH_OUT" status 2>/dev/null || true)
TX_BLOCK=$(cast receipt --rpc-url "$L1_RPC_URL" "$TX_HASH_OUT" blockNumber 2>/dev/null || true)
if [[ -z "$TX_STATUS" ]]; then
    log_warn "Could not fetch receipt for $TX_HASH_OUT"
elif [[ "$TX_STATUS" == *"success"* || "$TX_STATUS" == "true" \
     || "$TX_STATUS" == "1" || "$TX_STATUS" == "0x1" ]]; then
    log_info "Receipt: status=success blockNumber=$TX_BLOCK"
    log_info "Deposit count=$DEPOSIT_COUNT claimed successfully on L1 (network $DEST_NETWORK)."
else
    log_error "Receipt: status=REVERTED blockNumber=$TX_BLOCK"
    log_error "Replaying transaction to get revert reason..."
    cast run --rpc-url "$L1_RPC_URL" "$TX_HASH_OUT" 2>&1 | grep -E "revert|Revert|error|Error|←" | head -20 >&2
    exit 1
fi
