#!/usr/bin/env bash
# Reproduces Circle's USDC.e on the Kurtosis network, wired as on Polygon zkEVM mainnet
# (BuildOnPolygon/usdc-lxly, scripts/DeployInit.s.sol). Called by 20-prepare_network.sh with DELTA_SCENARIO=1,
# while the L2 still produces blocks.
#
#   L1: USDC (Circle FiatToken v2.1 behind FiatTokenProxy) and L1Escrow (UUPS proxy).
#   L2: USDC.e (FiatToken v2.1 behind FiatTokenProxy), ZkMinterBurner and NativeConverter (UUPS proxies), and a
#       Safe v1.3.0 (GnosisSafeL2, 2 of 3) that owns ZkMinterBurner and NativeConverter and is the proxy admin,
#       owner and masterMinter of USDC.e.
#
# USDC.e is native to the L2 (it is not a token wrapped by the bridge): L1Escrow.bridgeToken locks L1 USDC
# and sends a bridge MESSAGE, and claimMessage on the L2 makes ZkMinterBurner mint USDC.e. So the LBT does
# not track it and the first exit certificate does not pay it. The script deposits through L1Escrow for
# three EOAs and one contract, and leaves the backing L1 USDC in L1Escrow. Delta 3
# (53-generate_circle_usdc_delta.sh) pays the EOAs back with Message exits.
#
# The contracts are deployed from the creation bytecode in contracts/circle-usdc/ (see its README for the
# upstream commits). Every key below is a public test key, worthless.
#
# Requires the environment of export_kurtosis_env.sh: L1_RPC_URL, L2_RPC_URL, BRIDGE_ADDR, BRIDGE_SERVICE_URL.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

PROJECT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CERTIFICATE_SCRIPT_DIR="${PROJECT_ROOT}/tools/exit_certificate/scripts"
BIN_DIR="${SCRIPT_DIR}/contracts/circle-usdc"
VAULT_BIN_FILE="${SCRIPT_DIR}/contracts/Vault.bin"
CIRCLE_STATE_FILE="${CIRCLE_STATE_FILE:-${PROJECT_ROOT}/tmp/exit_certificate-e2e-circle-usdc.env}"
ZERO="0x0000000000000000000000000000000000000000"

# Deployer of everything and depositor: the Kurtosis L1 faucet key (20-prepare_network.sh funded it on L2).
PRIVATE_KEY="${PRIVATE_KEY:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"

# Safe owners: accounts #1-#3 of the well-known test mnemonic "test test test test test test test test test
# test test junk" (the default accounts of anvil and hardhat). Threshold 2.
CIRCLE_SAFE_OWNER_KEY_1="0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d"
CIRCLE_SAFE_OWNER_KEY_2="0x5de4111afa1a4b94908f83103eb1f1706367c2e68ca870fc3fb9a804cdab365a"
CIRCLE_SAFE_OWNER_KEY_3="0x7c852118294e51e653712a81e05800f419141751be58f605c371e15141b007a6"
CIRCLE_SAFE_THRESHOLD=2

# USDC.e holders. Three EOAs (the payout list of the recovery) and one contract (left out of it, as on
# mainnet). Distinct odd amounts (6 decimals) so a mixup between two payouts cannot go unnoticed.
CIRCLE_HOLDER_KEY_1="0x0000000000000000000000000000000000000000000000000000000000000011"
CIRCLE_HOLDER_KEY_2="0x0000000000000000000000000000000000000000000000000000000000000012"
CIRCLE_HOLDER_KEY_3="0x0000000000000000000000000000000000000000000000000000000000000013"
CIRCLE_HOLDER_AMOUNT_1="150000001"
CIRCLE_HOLDER_AMOUNT_2="75500003"
CIRCLE_HOLDER_AMOUNT_3="1000007"
CIRCLE_CONTRACT_HOLDER_AMOUNT="20000009"
# L1 USDC bridged with bridgeAsset, only to create the bridge-wrapped USDC NativeConverter is initialized
# with (zkBWUSDC on mainnet). It is a regular LBT token: the first certificate pays it.
CIRCLE_BW_USDC_AMOUNT="5000003"
# Minter allowance of ZkMinterBurner and NativeConverter on USDC.e.
CIRCLE_MINTER_ALLOWANCE="1000000000000000000"

for bin in cast jq bc; do
    command -v "$bin" >/dev/null || { log_error "$bin is required"; exit 1; }
done
for var in L1_RPC_URL L2_RPC_URL BRIDGE_ADDR BRIDGE_SERVICE_URL; do
    [[ -n "${!var:-}" ]] || { log_error "$var is not set (source export_kurtosis_env.sh first)"; exit 1; }
done

bin() {
    local f="${BIN_DIR}/$1.bin"
    [[ -f "$f" ]] || { log_error "creation bytecode not found: $f"; exit 1; }
    printf '0x%s' "$(cat "$f")"
}
# ctor <signature> <args...>: abi-encoded constructor arguments, without 0x.
ctor() {
    local encoded
    encoded="$(cast abi-encode "$@")"
    printf '%s' "${encoded#0x}"
}
# deploy <rpc> <name> <creation code>: sends the creation transaction and prints the new address.
deploy() {
    local rpc="$1" name="$2" code="$3" receipt addr
    receipt="$(cast send --rpc-url "$rpc" --private-key "$PRIVATE_KEY" --json --create "$code")"
    [[ "$(jq -r '.status' <<<"$receipt")" == "0x1" ]] || { log_error "deployment of $name reverted"; exit 1; }
    addr="$(jq -r '.contractAddress' <<<"$receipt")"
    [[ "$addr" =~ ^0x[0-9a-fA-F]{40}$ ]] || { log_error "deployment of $name returned no address"; exit 1; }
    log_info "  $name: $addr"
    printf '%s' "$addr"
}
# send <rpc> <to> <signature> <args...>: a transaction from the deployer; fails if it reverts.
send() {
    local rpc="$1" receipt
    shift
    receipt="$(cast send --rpc-url "$rpc" --private-key "$PRIVATE_KEY" --json "$@")"
    [[ "$(jq -r '.status' <<<"$receipt")" == "0x1" ]] || { log_error "transaction reverted: $*"; exit 1; }
    printf '%s' "$receipt"
}
# call <rpc> <to> <signature> <args...>: the first word of the result (cast appends "[1e6]" to numbers).
call() {
    local rpc="$1" out
    shift
    out="$(cast call --rpc-url "$rpc" "$@")"
    printf '%s' "${out%% *}"
}
slot_address() { cast parse-bytes32-address "$(cast storage --rpc-url "$1" "$2" "$3")"; }
same_address() { [[ "${1,,}" == "${2,,}" ]]; }
expect_address() {
    same_address "$2" "$3" || { log_error "$1 is $2, expected $3"; exit 1; }
}
expect_value() {
    [[ "$2" == "$3" ]] || { log_error "$1 is $2, expected $3"; exit 1; }
}

EIP1967_IMPL_SLOT="0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
EIP1967_ADMIN_SLOT="0xb53127684a568b3173ae13b9f8a6016e243e63b6e8ee1178d6a717850b5d6103"
ZOS_IMPL_SLOT="0x7050c9e0f4ca769c69bd3a8ef740bc37934f8e2c036e5a723fd8ee048ed3f8c3"   # org.zeppelinos.proxy.implementation
ZOS_ADMIN_SLOT="0x10d6a54a4754c8869d6886b5f5d7fbfa5b4522237ea5c60d11bc4e7a1ff9390b"  # org.zeppelinos.proxy.admin

DEPLOYER="$(cast wallet address --private-key "$PRIVATE_KEY")"
CIRCLE_SAFE_OWNER_1="$(cast wallet address --private-key "$CIRCLE_SAFE_OWNER_KEY_1")"
CIRCLE_SAFE_OWNER_2="$(cast wallet address --private-key "$CIRCLE_SAFE_OWNER_KEY_2")"
CIRCLE_SAFE_OWNER_3="$(cast wallet address --private-key "$CIRCLE_SAFE_OWNER_KEY_3")"
CIRCLE_HOLDER_1="$(cast wallet address --private-key "$CIRCLE_HOLDER_KEY_1")"
CIRCLE_HOLDER_2="$(cast wallet address --private-key "$CIRCLE_HOLDER_KEY_2")"
CIRCLE_HOLDER_3="$(cast wallet address --private-key "$CIRCLE_HOLDER_KEY_3")"
L2_NETWORK_ID="$(call "$L2_RPC_URL" "$BRIDGE_ADDR" "networkID()(uint32)")"
expect_value "the network ID of the L1 bridge" "$(call "$L1_RPC_URL" "$BRIDGE_ADDR" "networkID()(uint32)")" "0"

# deploy_fiat_token <rpc> <label> <proxy admin> <masterMinter and owner>: FiatTokenV2_1 behind FiatTokenProxy.
# The proxy admin cannot call the implementation through the proxy, so the admin is moved away from the
# deployer before the deployer initializes the token. Sets FIAT_PROXY and FIAT_IMPL.
deploy_fiat_token() {
    local rpc="$1" label="$2" admin="$3" owner="$4"
    FIAT_IMPL="$(deploy "$rpc" "${label} implementation (FiatTokenV2_1)" "$(bin FiatTokenV2_1)")"
    FIAT_PROXY="$(deploy "$rpc" "${label} (FiatTokenProxy)" "$(bin FiatTokenProxy)$(ctor 'c(address)' "$FIAT_IMPL")")"
    send "$rpc" "$FIAT_PROXY" "changeAdmin(address)" "$admin" >/dev/null
    # initialize(name, symbol, currency, decimals, masterMinter, pauser, blacklister, owner)
    send "$rpc" "$FIAT_PROXY" "initialize(string,string,string,uint8,address,address,address,address)" \
        "USD Coin" "USDC" "USD" 6 "$owner" "$admin" "$admin" "$owner" >/dev/null
    send "$rpc" "$FIAT_PROXY" "initializeV2(string)" "USD Coin" >/dev/null
    send "$rpc" "$FIAT_PROXY" "initializeV2_1(address)" "$admin" >/dev/null   # lostAndFound
    expect_address "the implementation of ${label}" "$(slot_address "$rpc" "$FIAT_PROXY" "$ZOS_IMPL_SLOT")" "$FIAT_IMPL"
    expect_address "the proxy admin of ${label}" "$(slot_address "$rpc" "$FIAT_PROXY" "$ZOS_ADMIN_SLOT")" "$admin"
}
# deploy_uups <rpc> <name>: a usdc-lxly contract behind its ERC1967 proxy, not initialized yet. Sets
# UUPS_PROXY and UUPS_IMPL.
deploy_uups() {
    local rpc="$1" name="$2"
    UUPS_IMPL="$(deploy "$rpc" "${name} implementation" "$(bin "$name")")"
    UUPS_PROXY="$(deploy "$rpc" "${name} (proxy)" "$(bin "${name}Proxy")$(ctor 'c(address,bytes)' "$UUPS_IMPL" 0x)")"
    expect_address "the implementation of ${name}" "$(slot_address "$rpc" "$UUPS_PROXY" "$EIP1967_IMPL_SLOT")" "$UUPS_IMPL"
}

# --- L1: USDC and the bridge-wrapped USDC on L2 ---
log_info "🪙 Deploy USDC (FiatToken) on L1"
# The proxy admin only has to differ from the deployer, who is the owner and masterMinter here.
deploy_fiat_token "$L1_RPC_URL" "L1 USDC" "$CIRCLE_SAFE_OWNER_1" "$DEPLOYER"
CIRCLE_L1_USDC="$FIAT_PROXY"
CIRCLE_TOTAL_DEPOSITED="$(echo "$CIRCLE_HOLDER_AMOUNT_1 + $CIRCLE_HOLDER_AMOUNT_2 + $CIRCLE_HOLDER_AMOUNT_3 + $CIRCLE_CONTRACT_HOLDER_AMOUNT" | bc)"
CIRCLE_PAYOUT_TOTAL="$(echo "$CIRCLE_HOLDER_AMOUNT_1 + $CIRCLE_HOLDER_AMOUNT_2 + $CIRCLE_HOLDER_AMOUNT_3" | bc)"
MINT_AMOUNT="$(echo "$CIRCLE_TOTAL_DEPOSITED + $CIRCLE_BW_USDC_AMOUNT" | bc)"
send "$L1_RPC_URL" "$CIRCLE_L1_USDC" "configureMinter(address,uint256)" "$DEPLOYER" "$MINT_AMOUNT" >/dev/null
send "$L1_RPC_URL" "$CIRCLE_L1_USDC" "mint(address,uint256)" "$DEPLOYER" "$MINT_AMOUNT" >/dev/null

log_info "🔗 Bridge ${CIRCLE_BW_USDC_AMOUNT} L1 USDC with bridgeAsset: creates the bridge-wrapped USDC on L2"
run_quiet "${EXIT_CERTIFICATE_SCRIPT_DIR}/bridge_l1_to_l2.sh" \
    --token "$CIRCLE_L1_USDC" --amount "$CIRCLE_BW_USDC_AMOUNT" --key "$PRIVATE_KEY" --wait
CIRCLE_BW_USDC="$(call "$L2_RPC_URL" "$BRIDGE_ADDR" "getTokenWrappedAddress(uint32,address)(address)" 0 "$CIRCLE_L1_USDC")"
if same_address "$CIRCLE_BW_USDC" "$ZERO"; then
    log_error "the bridge-wrapped USDC was not created on L2"
    exit 1
fi
log_info "  bridge-wrapped USDC on L2: $CIRCLE_BW_USDC"

# --- L2: the Safe ---
log_info "🔐 Deploy Safe v1.3.0 on L2 (GnosisSafeL2, ${CIRCLE_SAFE_THRESHOLD} of 3)"
SAFE_SINGLETON="$(deploy "$L2_RPC_URL" "GnosisSafeL2 (singleton)" "$(bin GnosisSafeL2)")"
SAFE_FACTORY="$(deploy "$L2_RPC_URL" "GnosisSafeProxyFactory" "$(bin GnosisSafeProxyFactory)")"
CIRCLE_MULTISEND="$(deploy "$L2_RPC_URL" "MultiSendCallOnly" "$(bin MultiSendCallOnly)")"
SAFE_SETUP="$(cast calldata "setup(address[],uint256,address,bytes,address,address,uint256,address)" \
    "[${CIRCLE_SAFE_OWNER_1},${CIRCLE_SAFE_OWNER_2},${CIRCLE_SAFE_OWNER_3}]" "$CIRCLE_SAFE_THRESHOLD" \
    "$ZERO" 0x "$ZERO" "$ZERO" 0 "$ZERO")"
SAFE_RECEIPT="$(send "$L2_RPC_URL" "$SAFE_FACTORY" "createProxy(address,bytes)" "$SAFE_SINGLETON" "$SAFE_SETUP")"
# ProxyCreation(address proxy, address singleton): the proxy is the first word of the data.
CIRCLE_SAFE="$(jq -r --arg f "${SAFE_FACTORY,,}" \
    '[.logs[] | select((.address | ascii_downcase) == $f)][0] | "0x" + .data[26:66]' <<<"$SAFE_RECEIPT")"
[[ "$CIRCLE_SAFE" =~ ^0x[0-9a-fA-F]{40}$ ]] || { log_error "could not read the Safe address from ProxyCreation"; exit 1; }
CIRCLE_SAFE="$(cast to-check-sum-address "$CIRCLE_SAFE")"
log_info "  Safe: $CIRCLE_SAFE"
expect_value "the Safe threshold" "$(call "$L2_RPC_URL" "$CIRCLE_SAFE" "getThreshold()(uint256)")" "$CIRCLE_SAFE_THRESHOLD"
for owner in "$CIRCLE_SAFE_OWNER_1" "$CIRCLE_SAFE_OWNER_2" "$CIRCLE_SAFE_OWNER_3"; do
    expect_value "isOwner($owner)" "$(call "$L2_RPC_URL" "$CIRCLE_SAFE" "isOwner(address)(bool)" "$owner")" "true"
done

# --- L2: USDC.e. The Safe is its proxy admin; the deployer stays owner and masterMinter until the minters
# are configured, then hands both roles to the Safe. ---
log_info "🪙 Deploy USDC.e (FiatToken) on L2"
# The same deployer on both chains can give USDC.e the L2 address that USDC has on L1. Mainnet has two
# different addresses, and the checks are clearer with two: skip a nonce when they would coincide.
L2_NONCE="$(cast nonce --rpc-url "$L2_RPC_URL" "$DEPLOYER")"
if same_address "$(cast compute-address --nonce "$((L2_NONCE + 1))" "$DEPLOYER" | awk '{print $NF}')" "$CIRCLE_L1_USDC"; then
    send "$L2_RPC_URL" "$DEPLOYER" --value 0 >/dev/null
fi
deploy_fiat_token "$L2_RPC_URL" "USDC.e" "$CIRCLE_SAFE" "$DEPLOYER"
CIRCLE_USDCE="$FIAT_PROXY"
CIRCLE_USDCE_IMPL="$FIAT_IMPL"
if same_address "$CIRCLE_USDCE" "$CIRCLE_L1_USDC"; then
    log_error "USDC.e on L2 and USDC on L1 ended up with the same address ($CIRCLE_USDCE)"
    exit 1
fi

# --- usdc-lxly, as DeployInit.s.sol: the proxies first (each side needs the address of the other), then
# initialize(owner, admin, bridge, peer network, peer contract, token[, bridge-wrapped USDC]). ---
log_info "🏦 Deploy L1Escrow on L1, ZkMinterBurner and NativeConverter on L2"
deploy_uups "$L1_RPC_URL" L1Escrow
CIRCLE_L1_ESCROW="$UUPS_PROXY"
deploy_uups "$L2_RPC_URL" ZkMinterBurner
CIRCLE_ZK_MINTER_BURNER="$UUPS_PROXY"
CIRCLE_ZK_MINTER_BURNER_IMPL="$UUPS_IMPL"
deploy_uups "$L2_RPC_URL" NativeConverter
CIRCLE_NATIVE_CONVERTER="$UUPS_PROXY"

send "$L1_RPC_URL" "$CIRCLE_L1_ESCROW" "initialize(address,address,address,uint32,address,address)" \
    "$DEPLOYER" "$DEPLOYER" "$BRIDGE_ADDR" "$L2_NETWORK_ID" "$CIRCLE_ZK_MINTER_BURNER" "$CIRCLE_L1_USDC" >/dev/null
send "$L2_RPC_URL" "$CIRCLE_ZK_MINTER_BURNER" "initialize(address,address,address,uint32,address,address)" \
    "$CIRCLE_SAFE" "$CIRCLE_SAFE" "$BRIDGE_ADDR" 0 "$CIRCLE_L1_ESCROW" "$CIRCLE_USDCE" >/dev/null
send "$L2_RPC_URL" "$CIRCLE_NATIVE_CONVERTER" "initialize(address,address,address,uint32,address,address,address)" \
    "$CIRCLE_SAFE" "$CIRCLE_SAFE" "$BRIDGE_ADDR" 0 "$CIRCLE_L1_ESCROW" "$CIRCLE_USDCE" "$CIRCLE_BW_USDC" >/dev/null

log_info "🔑 USDC.e roles: ZkMinterBurner and NativeConverter mint; the Safe is owner and masterMinter"
send "$L2_RPC_URL" "$CIRCLE_USDCE" "configureMinter(address,uint256)" "$CIRCLE_ZK_MINTER_BURNER" "$CIRCLE_MINTER_ALLOWANCE" >/dev/null
send "$L2_RPC_URL" "$CIRCLE_USDCE" "configureMinter(address,uint256)" "$CIRCLE_NATIVE_CONVERTER" "$CIRCLE_MINTER_ALLOWANCE" >/dev/null
send "$L2_RPC_URL" "$CIRCLE_USDCE" "updateMasterMinter(address)" "$CIRCLE_SAFE" >/dev/null
send "$L2_RPC_URL" "$CIRCLE_USDCE" "transferOwnership(address)" "$CIRCLE_SAFE" >/dev/null

expect_address "the owner of USDC.e" "$(call "$L2_RPC_URL" "$CIRCLE_USDCE" "owner()(address)")" "$CIRCLE_SAFE"
expect_address "the masterMinter of USDC.e" "$(call "$L2_RPC_URL" "$CIRCLE_USDCE" "masterMinter()(address)")" "$CIRCLE_SAFE"
expect_address "the proxy admin of USDC.e" "$(slot_address "$L2_RPC_URL" "$CIRCLE_USDCE" "$ZOS_ADMIN_SLOT")" "$CIRCLE_SAFE"
expect_value "isMinter(ZkMinterBurner)" "$(call "$L2_RPC_URL" "$CIRCLE_USDCE" "isMinter(address)(bool)" "$CIRCLE_ZK_MINTER_BURNER")" "true"
for c in "$CIRCLE_ZK_MINTER_BURNER" "$CIRCLE_NATIVE_CONVERTER"; do
    expect_address "the owner of $c" "$(call "$L2_RPC_URL" "$c" "owner()(address)")" "$CIRCLE_SAFE"
    expect_address "the admin of $c" "$(slot_address "$L2_RPC_URL" "$c" "$EIP1967_ADMIN_SLOT")" "$CIRCLE_SAFE"
done

# --- The contract holder: any contract will do, it only has to hold USDC.e ---
log_info "📦 Deploy the contract that will hold USDC.e (a Vault)"
CIRCLE_CONTRACT_HOLDER="$(deploy "$L2_RPC_URL" "contract holder (Vault)" "0x$(cat "$VAULT_BIN_FILE")")"

# The final checks of the e2e expect each holder to end with exactly its amount of L1 USDC, and the delta
# to leave the contract holder untouched.
for holder in "$CIRCLE_HOLDER_1" "$CIRCLE_HOLDER_2" "$CIRCLE_HOLDER_3"; do
    if [[ "$(cast code --rpc-url "$L2_RPC_URL" "$holder")" != "0x" ]]; then
        log_error "USDC.e holder $holder has code on L2: it must be an EOA"
        exit 1
    fi
done

# --- Deposits L1 → L2 through L1Escrow ---
log_info "💵 Deposit ${CIRCLE_TOTAL_DEPOSITED} USDC through L1Escrow.bridgeToken (3 EOAs and 1 contract)"
send "$L1_RPC_URL" "$CIRCLE_L1_USDC" "approve(address,uint256)" "$CIRCLE_L1_ESCROW" "$CIRCLE_TOTAL_DEPOSITED" >/dev/null
DEPOSIT_RECEIPTS=()
deposit() {
    DEPOSIT_RECEIPTS+=("$(send "$L1_RPC_URL" "$CIRCLE_L1_ESCROW" "bridgeToken(address,uint256,bool)" "$1" "$2" true)")
    log_info "  $2 → $1 (L1 tx $(jq -r '.transactionHash' <<<"${DEPOSIT_RECEIPTS[-1]}"))"
}
deposit "$CIRCLE_HOLDER_1" "$CIRCLE_HOLDER_AMOUNT_1"
deposit "$CIRCLE_HOLDER_2" "$CIRCLE_HOLDER_AMOUNT_2"
deposit "$CIRCLE_HOLDER_3" "$CIRCLE_HOLDER_AMOUNT_3"
deposit "$CIRCLE_CONTRACT_HOLDER" "$CIRCLE_CONTRACT_HOLDER_AMOUNT"

# claimMessage on the L2 makes ZkMinterBurner mint. The claim needs a global exit root that is already
# injected on the L2 and covers the deposit; the oracle does not inject every L1 info tree leaf, so the proof
# is built against the first INJECTED leaf at or after the one that covers the deposit (bridge service:
# l1-info-tree-index → injected-l1-info-leaf → claim-proof). Retried until that leaf is injected.
CLAIM_SIG="claimMessage(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"
BRIDGE_EVENT_TOPIC="0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b"
bridge_api() { curl -sf "${BRIDGE_SERVICE_URL}/bridge/v1/$1"; }
# try_claim_deposit <deposit count>: 0 when the deposit is claimed on L2 (now or already), 1 to retry later.
try_claim_deposit() {
    local dc="$1" bridge covering injected proof
    if [[ "$(call "$L2_RPC_URL" "$BRIDGE_ADDR" "isClaimed(uint32,uint32)(bool)" "$dc" 0)" == "true" ]]; then
        return 0
    fi
    bridge="$(bridge_api "bridge-by-deposit-count?network_id=0&deposit_count=${dc}")" || return 1
    covering="$(bridge_api "l1-info-tree-index?network_id=0&deposit_count=${dc}")" || return 1
    [[ "$covering" =~ ^[0-9]+$ ]] || return 1
    injected="$(bridge_api "injected-l1-info-leaf?network_id=${L2_NETWORK_ID}&leaf_index=${covering}" | jq -r '.l1_info_tree_index // empty')" || return 1
    [[ "$injected" =~ ^[0-9]+$ ]] || return 1
    proof="$(bridge_api "claim-proof?network_id=0&leaf_index=${injected}&deposit_count=${dc}")" || return 1
    cast send --rpc-url "$L2_RPC_URL" --private-key "$PRIVATE_KEY" --json "$BRIDGE_ADDR" "$CLAIM_SIG" \
        "$(jq -r '"[" + (.proof_local_exit_root | join(",")) + "]"' <<<"$proof")" \
        "$(jq -r '"[" + (.proof_rollup_exit_root | join(",")) + "]"' <<<"$proof")" \
        "$(jq -r '.global_index' <<<"$bridge")" \
        "$(jq -r '.l1_info_tree_leaf.mainnet_exit_root' <<<"$proof")" \
        "$(jq -r '.l1_info_tree_leaf.rollup_exit_root' <<<"$proof")" \
        "$(jq -r '.origin_network' <<<"$bridge")" "$(jq -r '.origin_address' <<<"$bridge")" \
        "$(jq -r '.destination_network' <<<"$bridge")" "$(jq -r '.destination_address' <<<"$bridge")" \
        "$(jq -r '.amount' <<<"$bridge")" "$(jq -r '.metadata' <<<"$bridge")" >/dev/null 2>&1 || return 1
    [[ "$(call "$L2_RPC_URL" "$BRIDGE_ADDR" "isClaimed(uint32,uint32)(bool)" "$dc" 0)" == "true" ]]
}
CIRCLE_CLAIM_TIMEOUT="${CIRCLE_CLAIM_TIMEOUT:-600}"
for receipt in "${DEPOSIT_RECEIPTS[@]}"; do
    # depositCount is the last static word of the BridgeEvent data (8 words before the dynamic metadata).
    dc="$(jq -r --arg t "$BRIDGE_EVENT_TOPIC" '[.logs[] | select(.topics[0] == $t)][0].data' <<<"$receipt")"
    dc="$(cast to-dec "0x${dc:450:64}")"
    log_info "⏳ Claim deposit ${dc} on L2 (claimMessage → ZkMinterBurner mints)"
    deadline=$(( $(date +%s) + CIRCLE_CLAIM_TIMEOUT ))
    until try_claim_deposit "$dc"; do
        if (( $(date +%s) >= deadline )); then
            log_error "deposit ${dc} could not be claimed on L2 within ${CIRCLE_CLAIM_TIMEOUT}s (no global exit root covering it was injected, or the claim reverts)"
            exit 1
        fi
        sleep 5
    done
done

balance_of() { call "$1" "$2" "balanceOf(address)(uint256)" "$3"; }
expect_value "USDC.e of holder 1" "$(balance_of "$L2_RPC_URL" "$CIRCLE_USDCE" "$CIRCLE_HOLDER_1")" "$CIRCLE_HOLDER_AMOUNT_1"
expect_value "USDC.e of holder 2" "$(balance_of "$L2_RPC_URL" "$CIRCLE_USDCE" "$CIRCLE_HOLDER_2")" "$CIRCLE_HOLDER_AMOUNT_2"
expect_value "USDC.e of holder 3" "$(balance_of "$L2_RPC_URL" "$CIRCLE_USDCE" "$CIRCLE_HOLDER_3")" "$CIRCLE_HOLDER_AMOUNT_3"
expect_value "USDC.e of the contract holder" "$(balance_of "$L2_RPC_URL" "$CIRCLE_USDCE" "$CIRCLE_CONTRACT_HOLDER")" "$CIRCLE_CONTRACT_HOLDER_AMOUNT"
expect_value "the total supply of USDC.e" "$(call "$L2_RPC_URL" "$CIRCLE_USDCE" "totalSupply()(uint256)")" "$CIRCLE_TOTAL_DEPOSITED"
expect_value "the L1 USDC held by L1Escrow" "$(balance_of "$L1_RPC_URL" "$CIRCLE_L1_USDC" "$CIRCLE_L1_ESCROW")" "$CIRCLE_TOTAL_DEPOSITED"
for holder in "$CIRCLE_HOLDER_1" "$CIRCLE_HOLDER_2" "$CIRCLE_HOLDER_3"; do
    expect_value "the L1 USDC of holder $holder" "$(balance_of "$L1_RPC_URL" "$CIRCLE_L1_USDC" "$holder")" "0"
done
log_info "  ✅ USDC.e minted to the 4 holders; L1Escrow holds the ${CIRCLE_TOTAL_DEPOSITED} L1 USDC backing it"

mkdir -p "$(dirname "$CIRCLE_STATE_FILE")"
cat > "$CIRCLE_STATE_FILE" <<EOF
# Generated by 21-prepare_circle_usdc.sh — consumed by 47-check_circle_usdc_not_paid.sh,
# 53-generate_circle_usdc_delta.sh, 61-claimer_follow.sh and 62-check_circle_usdc_claims.sh.
# The keys are public test keys, worthless.
CIRCLE_L2_NETWORK_ID="$L2_NETWORK_ID"
CIRCLE_DEPLOYER="$DEPLOYER"
CIRCLE_L1_USDC="$CIRCLE_L1_USDC"
CIRCLE_L1_ESCROW="$CIRCLE_L1_ESCROW"
CIRCLE_BW_USDC="$CIRCLE_BW_USDC"
CIRCLE_BW_USDC_AMOUNT="$CIRCLE_BW_USDC_AMOUNT"
CIRCLE_USDCE="$CIRCLE_USDCE"
CIRCLE_USDCE_IMPL="$CIRCLE_USDCE_IMPL"
CIRCLE_ZK_MINTER_BURNER="$CIRCLE_ZK_MINTER_BURNER"
CIRCLE_ZK_MINTER_BURNER_IMPL="$CIRCLE_ZK_MINTER_BURNER_IMPL"
CIRCLE_NATIVE_CONVERTER="$CIRCLE_NATIVE_CONVERTER"
CIRCLE_MULTISEND="$CIRCLE_MULTISEND"
CIRCLE_SAFE="$CIRCLE_SAFE"
CIRCLE_SAFE_THRESHOLD="$CIRCLE_SAFE_THRESHOLD"
CIRCLE_SAFE_OWNER_1="$CIRCLE_SAFE_OWNER_1"
CIRCLE_SAFE_OWNER_KEY_1="$CIRCLE_SAFE_OWNER_KEY_1"
CIRCLE_SAFE_OWNER_2="$CIRCLE_SAFE_OWNER_2"
CIRCLE_SAFE_OWNER_KEY_2="$CIRCLE_SAFE_OWNER_KEY_2"
CIRCLE_SAFE_OWNER_3="$CIRCLE_SAFE_OWNER_3"
CIRCLE_SAFE_OWNER_KEY_3="$CIRCLE_SAFE_OWNER_KEY_3"
CIRCLE_HOLDER_1="$CIRCLE_HOLDER_1"
CIRCLE_HOLDER_AMOUNT_1="$CIRCLE_HOLDER_AMOUNT_1"
CIRCLE_HOLDER_2="$CIRCLE_HOLDER_2"
CIRCLE_HOLDER_AMOUNT_2="$CIRCLE_HOLDER_AMOUNT_2"
CIRCLE_HOLDER_3="$CIRCLE_HOLDER_3"
CIRCLE_HOLDER_AMOUNT_3="$CIRCLE_HOLDER_AMOUNT_3"
CIRCLE_CONTRACT_HOLDER="$CIRCLE_CONTRACT_HOLDER"
CIRCLE_CONTRACT_HOLDER_AMOUNT="$CIRCLE_CONTRACT_HOLDER_AMOUNT"
CIRCLE_PAYOUT_TOTAL="$CIRCLE_PAYOUT_TOTAL"
CIRCLE_TOTAL_DEPOSITED="$CIRCLE_TOTAL_DEPOSITED"
EOF
log_info "Circle USDC scenario state saved to: $CIRCLE_STATE_FILE"
