#!/bin/bash
# zkEVM Circle USDC (USDC.e, usdc-lxly): payout of the listed EOA holders through an upgrade of
# ZkMinterBurner (exit_certificate delta e2e). Adapted from the move.sh.tpl of the Circle USDC recovery package
# (v2, not publicly available): same steps and checks, with every network-specific value as a placeholder
# (README.md, "move.sh.tpl"). Differences from the original: the addresses, the Safe nonce and
# threshold, the executor and approvers come from the e2e network; gas limits are estimated by
# cast instead of fixed; each transaction is sent with --async and its receipt polled; after
# deploying the two contracts it checks that the addresses substituted into their bytecode are the
# e2e ones.
#
# What it does on the halted L2:
#   1. Safe owner EXECUTOR deploys the payout list (data contracts: holder ‖ amount entries), the new
#      ZkMinterBurner implementation ZkMinterBurnerPayout and the temporary USDC.e implementation
#      UsdcRecoveryBurn (the recovery package's ZkMinterBurnerPayout.sol and UsdcRecoveryBurn.sol, compiled).
#   2. The Safe (owner and admin of ZkMinterBurner; proxy admin, owner and masterMinter of USDC.e)
#      executes ONE Safe transaction, a MultiSendCallOnly batch of:
#        a. USDC.e.upgradeTo(UsdcRecoveryBurn)
#        b. ZkMinterBurner.upgradeToAndCall(ZkMinterBurnerPayout, payout())
#        c. USDC.e.upgradeTo(<original USDC.e implementation>)
#      The APPROVERS approve its hash (approveHash) and EXECUTOR executes it: threshold @@THRESHOLD@@.
#   3. payout() runs in the ZkMinterBurner proxy. Per holder, exactly what the old
#      bridgeToken(holder, amount, _) did: burn the holder's USDC.e (Transfer holder → ZkMinterBurner,
#      Burn, Transfer → 0x0), then bridge.bridgeMessage(0, L1Escrow, _, abi.encode(holder, amount)).
#      @@COUNT@@ Message leaves, origin (L2, ZkMinterBurner), destination (0, L1Escrow), amount 0.
#      On L1, claimMessage of each leaf makes L1Escrow pay the holder.
#
# Holders sign nothing. The only senders are the Safe owners below. Runs where only cast, jq and
# "${RPC_URL}" exist; senders must be unlocked or impersonated (cast send --unlocked --from).
set -euo pipefail

RPC_URL="${RPC_URL:-http://127.0.0.1:8545}"

BRIDGE="@@BRIDGE@@"                         # L2 bridge
ZK_MINTER_BURNER="@@ZK_MINTER_BURNER@@"     # UUPS proxy, the locked contract
OLD_IMPL="@@OLD_IMPL@@"                     # its implementation at the halt
USDCE="@@USDCE@@"                           # USDC.e, FiatToken behind a zos AdminUpgradeabilityProxy
USDCE_IMPL="@@USDCE_IMPL@@"                 # its implementation at the halt (restored at the end)
MULTISEND="@@MULTISEND@@"                   # Safe v1.3.0 MultiSendCallOnly
SAFE="@@SAFE@@"                             # ZkMinterBurner owner and admin, Safe v1.3.0
SAFE_NONCE="@@SAFE_NONCE@@"                 # the Safe's nonce at the halt
THRESHOLD="@@THRESHOLD@@"                   # the Safe's threshold
L1_ESCROW="@@L1_ESCROW@@"                   # destination of the messages (network 0)
EXECUTOR="@@EXECUTOR@@"                     # Safe owner: deploys and executes
APPROVERS="@@APPROVERS@@"                   # THRESHOLD - 1 other Safe owners (approveHash), space separated
PAYOUT_COUNT="@@COUNT@@"
PAYOUT_TOTAL="@@TOTAL@@"   # USDC base units (6 decimals)
IMPL_SLOT="0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc"
ZOS_IMPL_SLOT="0x7050c9e0f4ca769c69bd3a8ef740bc37934f8e2c036e5a723fd8ee048ed3f8c3"   # keccak256("org.zeppelinos.proxy.implementation")
ZOS_ADMIN_SLOT="0x10d6a54a4754c8869d6886b5f5d7fbfa5b4522237ea5c60d11bc4e7a1ff9390b"  # keccak256("org.zeppelinos.proxy.admin")
BRIDGE_EVENT="0x501781209a1f8899323b96b4ef08b168df93e0a90c673d1e4cce39366cb62f9b"
EXECUTION_SUCCESS="0x442e715f626346e8c54381002da614f62bee8d27386535b2521ec8540898556e"  # ExecutionSuccess(bytes32,uint256)

# Payout list: init code of each data contract (runtime = 0x00 ‖ entries of 20-byte holder ‖ 6-byte amount).
CHUNKS=(
@@CHUNKS@@
)
IMPL_BYTECODE="@@IMPL_BYTECODE@@"   # ZkMinterBurnerPayout.bin with BRIDGE, USDCE and L1_ESCROW substituted
BURN_BYTECODE="@@BURN_BYTECODE@@"   # UsdcRecoveryBurn.bin with ZK_MINTER_BURNER substituted

for bin in cast jq; do
    if ! command -v "$bin" >/dev/null 2>&1; then
        echo "Error: '$bin' not found in \$PATH." >&2
        exit 1
    fi
done
die() {
    echo "Error: $*" >&2
    exit 1
}
lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }
word() { local o; o="$(cast "$@" --rpc-url "${RPC_URL}")"; printf '%s' "${o%% *}"; }
slot_addr() { lower "$(cast parse-bytes32-address "$(cast storage "$1" "$2" --rpc-url "${RPC_URL}")")"; }

# --- Pre-checks: the state at the halt -------------------------------------------------------
[ "$(lower "$(word call "${ZK_MINTER_BURNER}" "owner()(address)")")" = "$(lower "${SAFE}")" ] || die "ZkMinterBurner owner is not ${SAFE}."
[ "$(slot_addr "${ZK_MINTER_BURNER}" "${IMPL_SLOT}")" = "$(lower "${OLD_IMPL}")" ] || die "ZkMinterBurner implementation is not ${OLD_IMPL}."
[ "$(slot_addr "${USDCE}" "${ZOS_IMPL_SLOT}")" = "$(lower "${USDCE_IMPL}")" ] || die "USDC.e implementation is not ${USDCE_IMPL}."
[ "$(slot_addr "${USDCE}" "${ZOS_ADMIN_SLOT}")" = "$(lower "${SAFE}")" ] || die "USDC.e proxy admin is not ${SAFE}."
[ "$(word call "${USDCE}" "paused()(bool)")" = "false" ] || die "USDC.e is paused."
[ "$(cast code "${MULTISEND}" --rpc-url "${RPC_URL}")" != "0x" ] || die "MultiSendCallOnly not deployed at ${MULTISEND}."
supply_before="$(word call "${USDCE}" "totalSupply()(uint256)")"
[ "$(word call "${SAFE}" "getThreshold()(uint256)")" = "${THRESHOLD}" ] || die "Safe threshold is not ${THRESHOLD}."
[ "$(word call "${SAFE}" "nonce()(uint256)")" = "${SAFE_NONCE}" ] || die "Safe nonce is not ${SAFE_NONCE} (fresh fork?)."
signers=0
for o in ${EXECUTOR} ${APPROVERS}; do
    [ "$(word call "${SAFE}" "isOwner(address)(bool)" "${o}")" = "true" ] || die "${o} is not a Safe owner."
    signers=$(( signers + 1 ))
done
[ "${signers}" -ge "${THRESHOLD}" ] || die "${signers} signer(s), the Safe threshold is ${THRESHOLD}."

# send <from> <cast send args...>: sends, waits for the receipt, requires status 1, prints a one-line
# summary; the receipt goes to the global variable RECEIPT. The transaction is sent with --async and
# the receipt is then polled until it is a JSON object with a status: an answer that is not one
# (seen once from `cast send --json` through the sandbox proxy) is retried instead of trusted.
RECEIPT=""
RECEIPT_TIMEOUT="${RECEIPT_TIMEOUT:-600}"
send() {
    local from="$1" hash tries=0
    shift
    hash="$(cast send --unlocked --from "${from}" --async --rpc-url "${RPC_URL}" "$@")"
    case "${hash}" in 0x[0-9a-fA-F]*) ;; *) die "unexpected answer to the transaction: ${hash}" ;; esac
    while :; do
        RECEIPT="$(cast receipt "${hash}" --json --rpc-url "${RPC_URL}" 2>/dev/null || true)"
        if jq -e 'type == "object" and has("status")' <<<"${RECEIPT}" >/dev/null 2>&1; then
            break
        fi
        tries=$((tries + 1))
        if [ "${tries}" -ge "${RECEIPT_TIMEOUT}" ]; then
            echo "last answer for ${hash}: ${RECEIPT}" >&2
            die "no receipt for ${hash} after ${RECEIPT_TIMEOUT}s."
        fi
        sleep 1
    done
    jq -c '{transactionHash, from, to, contractAddress, status, gasUsed, logs: (.logs | length)}' <<<"${RECEIPT}"
    [ "$(jq -r '.status' <<<"${RECEIPT}")" = "0x1" ] || die "transaction ${hash} reverted."
}

# --- 1. Deploy the payout list and the new implementation (EXECUTOR) -------------------------
echo "1. Deploying the payout list (${#CHUNKS[@]} data contracts) and ZkMinterBurnerPayout from ${EXECUTOR} ..."
pointers=""
for c in "${CHUNKS[@]}"; do
    send "${EXECUTOR}" --create "${c}"
    pointers="${pointers:+${pointers},}$(jq -r '.contractAddress' <<<"${RECEIPT}")"
done
args="$(cast abi-encode "constructor(address[],uint256,uint256)" "[${pointers}]" "${PAYOUT_COUNT}" "${PAYOUT_TOTAL}")"
send "${EXECUTOR}" --create "${IMPL_BYTECODE}${args#0x}"
impl="$(jq -r '.contractAddress' <<<"${RECEIPT}")"
send "${EXECUTOR}" --create "${BURN_BYTECODE}"
burn_impl="$(jq -r '.contractAddress' <<<"${RECEIPT}")"
[ "$(word call "${impl}" "payoutCount()(uint256)")" = "${PAYOUT_COUNT}" ] || die "payoutCount mismatch."
[ "$(word call "${impl}" "payoutTotal()(uint256)")" = "${PAYOUT_TOTAL}" ] || die "payoutTotal mismatch."
# The addresses substituted into the mainnet bytecode must be the ones of this network.
[ "$(lower "$(word call "${impl}" "BRIDGE()(address)")")" = "$(lower "${BRIDGE}")" ] || die "ZkMinterBurnerPayout.BRIDGE is not ${BRIDGE}."
[ "$(lower "$(word call "${impl}" "USDCE()(address)")")" = "$(lower "${USDCE}")" ] || die "ZkMinterBurnerPayout.USDCE is not ${USDCE}."
[ "$(lower "$(word call "${impl}" "L1_ESCROW()(address)")")" = "$(lower "${L1_ESCROW}")" ] || die "ZkMinterBurnerPayout.L1_ESCROW is not ${L1_ESCROW}."
[ "$(lower "$(word call "${burn_impl}" "ZK_MINTER_BURNER()(address)")")" = "$(lower "${ZK_MINTER_BURNER}")" ] || die "UsdcRecoveryBurn.ZK_MINTER_BURNER is not ${ZK_MINTER_BURNER}."
echo "   ZkMinterBurnerPayout: ${impl}"
echo "   UsdcRecoveryBurn:     ${burn_impl}"

# --- 2. The Safe transaction: a MultiSendCallOnly batch (delegatecall) -----------------------
# packed <to> <calldata>: one MultiSend entry (operation 0 = call, value 0).
# Layout: uint8 operation ‖ address to ‖ uint256 value ‖ uint256 data length ‖ data.
packed() {
    local to data
    to="$(lower "${1#0x}")"
    data="${2#0x}"
    printf '00%s%064x%064x%s' "${to}" 0 "$(( ${#data} / 2 ))" "${data}"
}
tx_a="$(packed "${USDCE}" "$(cast calldata "upgradeTo(address)" "${burn_impl}")")"
tx_b="$(packed "${ZK_MINTER_BURNER}" "$(cast calldata "upgradeToAndCall(address,bytes)" "${impl}" "$(cast calldata "payout()")")")"
tx_c="$(packed "${USDCE}" "$(cast calldata "upgradeTo(address)" "${USDCE_IMPL}")")"
data="$(cast calldata "multiSend(bytes)" "0x${tx_a#0x}${tx_b#0x}${tx_c#0x}")"
zero="0x0000000000000000000000000000000000000000"
safe_tx_hash="$(word call "${SAFE}" \
    "getTransactionHash(address,uint256,bytes,uint8,uint256,uint256,uint256,address,address,uint256)(bytes32)" \
    "${MULTISEND}" 0 "${data}" 1 0 0 0 "${zero}" "${zero}" "${SAFE_NONCE}")"
echo "2. Safe transaction ${safe_tx_hash} (MultiSendCallOnly, delegatecall):"
echo "   a. USDC.e.upgradeTo(${burn_impl})"
echo "   b. ZkMinterBurner.upgradeToAndCall(${impl}, payout())"
echo "   c. USDC.e.upgradeTo(${USDCE_IMPL})"
for o in ${APPROVERS}; do
    send "${o}" "${SAFE}" "approveHash(bytes32)" "${safe_tx_hash}"
done
# Signatures: one per owner, ascending by address; v = 1 (approved hash, or the executor itself).
sigs="0x"
for o in $(for a in ${EXECUTOR} ${APPROVERS}; do lower "${a#0x}"; echo; done | sort); do
    sigs="${sigs}000000000000000000000000${o}000000000000000000000000000000000000000000000000000000000000000001"
done

# --- 3. Execute: upgrade + one bridgeMessage per holder --------------------------------------
echo "3. Executing the Safe transaction from ${EXECUTOR} (${PAYOUT_COUNT} burns + bridgeMessages) ..."
send "${EXECUTOR}" "${SAFE}" \
    "execTransaction(address,uint256,bytes,uint8,uint256,uint256,uint256,address,address,bytes)(bool)" \
    "${MULTISEND}" 0 "${data}" 1 0 0 0 "${zero}" "${zero}" "${sigs}"

ok="$(jq --arg safe "$(lower "${SAFE}")" --arg t "${EXECUTION_SUCCESS}" \
    '[.logs[] | select((.address | ascii_downcase) == $safe and .topics[0] == $t)] | length' <<<"${RECEIPT}")"
[ "${ok}" = "1" ] || die "the Safe did not emit ExecutionSuccess."
events="$(jq --arg b "$(lower "${BRIDGE}")" --arg t "${BRIDGE_EVENT}" \
    '[.logs[] | select((.address | ascii_downcase) == $b and .topics[0] == $t)] | length' <<<"${RECEIPT}")"
[ "${events}" = "${PAYOUT_COUNT}" ] || die "${events} BridgeEvents, expected ${PAYOUT_COUNT}."
burns="$(jq --arg u "$(lower "${USDCE}")" --arg t "$(cast keccak "Burn(address,uint256)")" \
    '[.logs[] | select((.address | ascii_downcase) == $u and .topics[0] == $t)] | length' <<<"${RECEIPT}")"
[ "${burns}" = "${PAYOUT_COUNT}" ] || die "${burns} Burn events, expected ${PAYOUT_COUNT}."
[ "$(slot_addr "${ZK_MINTER_BURNER}" "${IMPL_SLOT}")" = "$(lower "${impl}")" ] || die "ZkMinterBurner was not upgraded."
[ "$(slot_addr "${USDCE}" "${ZOS_IMPL_SLOT}")" = "$(lower "${USDCE_IMPL}")" ] || die "USDC.e implementation was not restored."
supply_after="$(word call "${USDCE}" "totalSupply()(uint256)")"
[ "$(( supply_before - supply_after ))" = "${PAYOUT_TOTAL}" ] || die "USDC.e supply dropped by $(( supply_before - supply_after )), expected ${PAYOUT_TOTAL}."
echo "OK: ZkMinterBurner upgraded to ${impl}; USDC.e implementation restored; ${burns} burns and"
echo "    ${events} bridgeMessages to L1Escrow; USDC.e supply ${supply_before} -> ${supply_after} (-${PAYOUT_TOTAL})."
