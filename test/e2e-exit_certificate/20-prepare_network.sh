#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CETIFICATE_SCRIPT_DIR="${PROJECT_ROOT}/tools/exit_certificate/scripts"

# Sender used for every prepared tx: the Kurtosis L1 faucet key (same default as
# bridge_l1_to_l2.sh). It bridges ETH and the ERC-20 to itself on L2, so it has
# L2 gas to send the ERC-20 transfer below.
PRIVATE_KEY="${PRIVATE_KEY:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"

# Passive ERC-20 recipient (AET-02): an account that only ever RECEIVES an
# ERC-20 transfer on L2 — no nonce, no native balance, no state change of its
# own — so it is invisible to state dumps and prestateTracer diffs and can only
# be discovered through Transfer logs. Fixed key so the funds stay recoverable.
ERC20_PASSIVE_PRIVATE_KEY="0x0000000000000000000000000000000000000000000000000000000000ae7002"
ERC20_PASSIVE_ADDRESS="0x00F6BE813A18e02Da75D1ceCA33a67507Bea8510"

# ERC-20 amounts (18 decimals). Distinct odd numbers so a mixup between the two
# certificate exits cannot go unnoticed in the check step.
ERC20_BRIDGE_AMOUNT="6000000000000000019"
ERC20_TRANSFER_AMOUNT="2000000000000000007"

ERC20_BIN_FILE="${PROJECT_ROOT}/test/contracts/bin/mintableerc20.bin"
ERC20_STATE_FILE="${PROJECT_ROOT}/tmp/exit_certificate-e2e-erc20.env"

#
# First we need a settled certificate, for that we need
# a bridge L2 -> L1 to produce this certificate.
# for that we need first a L1 -> L2 to don't run underbalance tree error

log_info "🛠️ Set kurtosis env variables"
source <("${EXIT_CETIFICATE_SCRIPT_DIR}/export_kurtosis_env.sh" 1)

log_info "🔗 Run L1 -> L2 bridge to have funds in L2 (autoclaimed by zkevm-bridge-service)"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/bridge_l1_to_l2.sh" --amount 98476876062940103 --wait

#
# ERC-20 case (AET-02): mint an ERC-20 on L1, bridge it to L2 and then move part
# of the wrapped balance to an account that never sends any transaction. Both
# holders must end up in the exit certificate.

log_info "🪙 Deploy MintableERC20 on L1 and mint ${ERC20_BRIDGE_AMOUNT} to the sender"
HOLDER_ADDRESS=$(cast wallet address --private-key "$PRIVATE_KEY")
CONSTRUCTOR_ARGS=$(cast abi-encode "constructor(string,string)" "AET02 Exit Test Token" "AET02")
ERC20_L1_ADDRESS=$(cast send \
    --rpc-url "$L1_RPC_URL" \
    --private-key "$PRIVATE_KEY" \
    --json \
    --create "0x$(cat "$ERC20_BIN_FILE")${CONSTRUCTOR_ARGS#0x}" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['contractAddress'])")
log_info "MintableERC20 deployed on L1 at: $ERC20_L1_ADDRESS"
run_quiet cast send \
    --rpc-url "$L1_RPC_URL" \
    --private-key "$PRIVATE_KEY" \
    "$ERC20_L1_ADDRESS" \
    "mint(address,uint256)" "$HOLDER_ADDRESS" "$ERC20_BRIDGE_AMOUNT"

log_info "🔗 Run L1 -> L2 ERC-20 bridge (autoclaimed by zkevm-bridge-service)"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/bridge_l1_to_l2.sh" \
    --token "$ERC20_L1_ADDRESS" --amount "$ERC20_BRIDGE_AMOUNT" --key "$PRIVATE_KEY" --wait

log_info "🔍 Resolve the L2 wrapped token address"
ERC20_L2_WRAPPED_ADDRESS=$(cast call \
    --rpc-url "$L2_RPC_URL" \
    "$BRIDGE_ADDR" \
    "getTokenWrappedAddress(uint32,address)(address)" 0 "$ERC20_L1_ADDRESS")
if [[ -z "$ERC20_L2_WRAPPED_ADDRESS" \
   || "$ERC20_L2_WRAPPED_ADDRESS" == "0x0000000000000000000000000000000000000000" ]]; then
    log_error "Wrapped token for $ERC20_L1_ADDRESS not found on the L2 bridge"
    exit 1
fi
log_info "L2 wrapped token: $ERC20_L2_WRAPPED_ADDRESS"

# The passive account must be truly untouched on L2 (no nonce, no native
# balance, e.g. not part of the enclave's genesis premint) — otherwise it would
# show up in the state dump anyway and this scenario would stop covering AET-02.
PASSIVE_NONCE=$(cast nonce --rpc-url "$L2_RPC_URL" "$ERC20_PASSIVE_ADDRESS")
PASSIVE_NATIVE_BALANCE=$(cast balance --rpc-url "$L2_RPC_URL" "$ERC20_PASSIVE_ADDRESS")
if [[ "$PASSIVE_NONCE" != "0" || "$PASSIVE_NATIVE_BALANCE" != "0" ]]; then
    log_error "Passive account $ERC20_PASSIVE_ADDRESS is not untouched on L2"
    log_error "  nonce=$PASSIVE_NONCE nativeBalance=$PASSIVE_NATIVE_BALANCE (both must be 0)"
    exit 1
fi

log_info "💸 Transfer ${ERC20_TRANSFER_AMOUNT} wrapped ERC-20 on L2 to the passive account ${ERC20_PASSIVE_ADDRESS}"
run_quiet cast send \
    --rpc-url "$L2_RPC_URL" \
    --private-key "$PRIVATE_KEY" \
    "$ERC20_L2_WRAPPED_ADDRESS" \
    "transfer(address,uint256)" "$ERC20_PASSIVE_ADDRESS" "$ERC20_TRANSFER_AMOUNT"

HOLDER_L2_BALANCE=$(cast call --rpc-url "$L2_RPC_URL" "$ERC20_L2_WRAPPED_ADDRESS" \
    "balanceOf(address)(uint256)" "$HOLDER_ADDRESS" | cut -f1 -d' ')
PASSIVE_L2_BALANCE=$(cast call --rpc-url "$L2_RPC_URL" "$ERC20_L2_WRAPPED_ADDRESS" \
    "balanceOf(address)(uint256)" "$ERC20_PASSIVE_ADDRESS" | cut -f1 -d' ')
log_info "L2 wrapped balances — holder: $HOLDER_L2_BALANCE, passive: $PASSIVE_L2_BALANCE"

mkdir -p "$(dirname "$ERC20_STATE_FILE")"
cat > "$ERC20_STATE_FILE" <<EOF
# Generated by 20-prepare_network.sh — consumed by 45-check_exit_certificate.sh
ERC20_L1_ADDRESS="$ERC20_L1_ADDRESS"
ERC20_L2_WRAPPED_ADDRESS="$ERC20_L2_WRAPPED_ADDRESS"
ERC20_HOLDER_ADDRESS="$HOLDER_ADDRESS"
ERC20_PASSIVE_ADDRESS="$ERC20_PASSIVE_ADDRESS"
ERC20_PASSIVE_PRIVATE_KEY="$ERC20_PASSIVE_PRIVATE_KEY"
ERC20_BRIDGE_AMOUNT="$ERC20_BRIDGE_AMOUNT"
ERC20_TRANSFER_AMOUNT="$ERC20_TRANSFER_AMOUNT"
EOF
log_info "ERC-20 scenario state saved to: $ERC20_STATE_FILE"

log_info "🔗 Run L2 -> L1 bridge to produce a certificate"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/bridge_l2_to_l1.sh" --amount 1

# The L2 bridge's current root is the exact local exit root the agglayer must settle before the
# exit certificate can be generated (its AET-11 guard requires L2 bridge getRoot() == settled LER).
# No more L2 bridge exits happen after this point, so the root is stable. Waiting for this specific
# root closes the race where the aggsender has not yet submitted the new certificate — a plain
# "no pending certificate == settled" check would return prematurely and leave the bridge LER ahead.
EXPECTED_LER=$(cast call --rpc-url "$L2_RPC_URL" "$BRIDGE_ADDR" "getRoot()(bytes32)" | cut -f1 -d' ')
log_info "⏳ Wait for the certificate settling L2 bridge root ${EXPECTED_LER}"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/agglayer_certificate_status.sh" --wait --expected-ler "$EXPECTED_LER"

log_info "✅ Done, the network is ready for exit-certificate tests"
