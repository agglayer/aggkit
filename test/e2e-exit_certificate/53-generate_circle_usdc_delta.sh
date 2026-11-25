#!/usr/bin/env bash
# Delta 3 of the --delta scenario: the recovery of Circle's USDC.e, with bridge MESSAGE exits
# (agglayer/issues#298). It reproduces the mechanism of the real recovery package (one kit case, a Safe
# transaction that upgrades ZkMinterBurner and pays every listed holder) on the contracts
# 21-prepare_circle_usdc.sh deployed.
#
#   1. Builds the kit case with a zkevm-proof-of-ownership-kit that writes recovery.json version 1
#      (KIT_V1_DIR): move.sh is contracts/circle-usdc/move.sh.tpl rendered for this network. On a
#      shadow-fork of the frozen L2 it deploys the payout list (the EOA holders only), ZkMinterBurnerPayout and
#      UsdcRecoveryBurn, and the Safe runs one MultiSendCallOnly transaction:
#        USDC.e.upgradeTo(UsdcRecoveryBurn); ZkMinterBurner.upgradeToAndCall(ZkMinterBurnerPayout, payout());
#        USDC.e.upgradeTo(<original implementation>)
#      payout() burns each holder's USDC.e and calls bridgeMessage(0, L1Escrow, _, abi.encode(holder, amount)).
#      The kit writes recovery.json from the BridgeEvents; the two Safe owners that sent transactions sign;
#      prepare-submission.sh packs the .tgz and verify-submission.sh verifies it.
#   2. Adds the .tgz to the cases dir, next to the two legacy-format cases of deltas 1 and 2, and runs
#      `--step recoveries` chained on delta 2: the ledger skips the legacy cases and issues one Message exit
#      per output of the kit, field by field and in the same order. Messages consume no balance.
#   3. Generates the delta certificate (CHECK → F → G2 → H → I → SIGN), submits it and waits for settlement.
#   4. Verifies the chaining on delta 2, as 52-generate_delta_certificate.sh does for deltas 1 and 2.
#
# With ARCHIVE_URI_BASE set, options.previousRun is the archive BASE (not the run): the tool asks the
# agglayer for the last settled height and reads that run from the archive.
#
# Needs: KIT_V1_DIR (default: ../zkevm-proof-of-ownership-kit-v1 next to this repo), docker, cast, jq, bc.
# KIT_ANVIL_PORT (default 18546) is the port of the kit's fork.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TMP_DIR="${AGGKIT_DIR}/tmp"
BASE_CONFIG="${EXIT_CERT_CONFIG:-${TMP_DIR}/exit_certificate-kurtosis.json}"
CIRCLE_STATE_FILE="${CIRCLE_STATE_FILE:-${TMP_DIR}/exit_certificate-e2e-circle-usdc.env}"
BIN_DIR="${SCRIPT_DIR}/contracts/circle-usdc"
BINARY="${AGGKIT_DIR}/target/exit_certificate"
INDEX=3
PREV_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-$((INDEX - 1)).json"

for bin in jq bc docker cast curl; do
    command -v "$bin" >/dev/null || { log_error "$bin is required"; exit 1; }
done
for f in "$BASE_CONFIG" "$CIRCLE_STATE_FILE" "$PREV_CONFIG"; do
    if [[ ! -f "$f" ]]; then
        log_error "required file not found: $f"
        log_error "Run the e2e with --delta (deltas 1 and 2 come first)."
        exit 1
    fi
done
# shellcheck source=/dev/null
source "$CIRCLE_STATE_FILE"

KIT_V1_DIR="${KIT_V1_DIR:-$(cd "${AGGKIT_DIR}/.." && pwd)/zkevm-proof-of-ownership-kit-v1}"
KIT_ANVIL_PORT="${KIT_ANVIL_PORT:-18546}"
for f in submission/prepare-submission.sh submission/verify-submission.sh submission/helpers/generate.sh \
         ownership-proof/sign-message.sh recovery-tx/build-recovery.sh; do
    if [[ ! -f "${KIT_V1_DIR}/${f}" ]]; then
        log_error "zkevm-proof-of-ownership-kit (recovery.json version 1) not found: ${KIT_V1_DIR}/${f} is missing"
        log_error "Set KIT_V1_DIR to a checkout of the kit that writes recovery.json version 1."
        exit 1
    fi
done

# --- previous run: delta 2 ---
PREV_OUTPUT_REL="$(jq -r '.options.outputDir' "$PREV_CONFIG")"
[[ "$PREV_OUTPUT_REL" == /* ]] && PREV_OUTPUT="$PREV_OUTPUT_REL" || PREV_OUTPUT="${TMP_DIR}/${PREV_OUTPUT_REL}"
PREV_CERT="${PREV_OUTPUT}/exit-certificate-full.json"
[[ -f "$PREV_CERT" ]] || { log_error "previous run certificate not found: $PREV_CERT"; exit 1; }
[[ -f "${PREV_OUTPUT}/step-wait-result.json" ]] || { log_error "previous run is not settled"; exit 1; }
PREV_STATE="${PREV_OUTPUT}/recoveries"

CASES_REL="./recoveries-cases"
CASES_DIR="${TMP_DIR}/recoveries-cases"
DELTA_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}.json"
DELTA_OUTPUT_REL="./output-kurtosis-delta-${INDEX}"
DELTA_OUTPUT="${TMP_DIR}/output-kurtosis-delta-${INDEX}"
RECOVERIES_OUT="${DELTA_OUTPUT}/recoveries"
DELTA_EXITS_REL="${DELTA_OUTPUT_REL}/recoveries/delta-exits.json"

L2_RPC="$(jq -r '.l2RpcUrl' "$BASE_CONFIG")"
L2_BRIDGE="$(jq -r '.l2BridgeAddress' "$BASE_CONFIG")"
HOLDERS=("$CIRCLE_HOLDER_1" "$CIRCLE_HOLDER_2" "$CIRCLE_HOLDER_3")
AMOUNTS=("$CIRCLE_HOLDER_AMOUNT_1" "$CIRCLE_HOLDER_AMOUNT_2" "$CIRCLE_HOLDER_AMOUNT_3")
PAYOUT_COUNT="${#HOLDERS[@]}"

FAILURES=0
# check <description> <command...>: runs the command (a failing one must not abort under set -e) and
# reports ✅/❌. Use `test`, not `[[ ]]`, for the conditions.
check() {
    local desc="$1"
    shift
    if "$@"; then
        log_info "  ✅ $desc"
    else
        log_error "  ❌ $desc"
        FAILURES=$((FAILURES + 1))
    fi
}
l2_call() {
    local out
    out="$(cast call --rpc-url "$L2_RPC" "$@")"
    printf '%s' "${out%% *}"
}

# The exits this delta must carry: one Message per payout, in payout order, as a JSON array in the shape of
# delta-exits.json (metadata = abi.encode(holder, amount), plain hex).
expected_exits() {
    local i md out="[]"
    for i in "${!HOLDERS[@]}"; do
        md="$(cast abi-encode "f(address,uint256)" "${HOLDERS[$i]}" "${AMOUNTS[$i]}")"
        out="$(jq -c --argjson n "$CIRCLE_L2_NETWORK_ID" --arg o "${CIRCLE_ZK_MINTER_BURNER,,}" \
            --arg d "${CIRCLE_L1_ESCROW,,}" --arg m "${md#0x}" \
            '. + [{leaf_type: "Message", token_info: {origin_network: $n, origin_token_address: $o},
                   dest_network: 0, dest_address: $d, amount: "0", metadata: $m}]' <<<"$out")"
    done
    printf '%s' "$out"
}
# normalize_exits: bridge exits (delta-exits.json / certificate shape) with lower-case addresses and metadata.
normalize_exits() {
    jq -c '[.[] | {leaf_type, token_info: {origin_network: .token_info.origin_network,
                 origin_token_address: (.token_info.origin_token_address | ascii_downcase)},
             dest_network, dest_address: (.dest_address | ascii_downcase), amount,
             metadata: ((.metadata // "") | ascii_downcase | ltrimstr("0x"))}]'
}
# outputs_as_exits: the outputs of a recovery.json version 1 in the shape of delta-exits.json.
outputs_as_exits() {
    jq -c '[.[] | {leaf_type, token_info, dest_network: .destination_network,
                   dest_address: .destination_address, amount, metadata}]' | normalize_exits
}

# --- rendering of move.sh (contracts/circle-usdc/README.md) ---
# subst_address <hex> <mainnet address> <e2e address> <occurrences>: the package contracts hard-code the
# mainnet addresses as constants; each is a 20-byte literal in the creation bytecode.
subst_address() {
    local hex="$1" old="${2#0x}" new="${3#0x}" n
    old="${old,,}"
    new="${new,,}"
    n="$(grep -o "$old" <<<"$hex" | wc -l)"
    [[ "$n" == "$4" ]] || { log_error "bytecode: ${old} appears ${n} time(s), expected $4"; exit 1; }
    printf '%s' "${hex//${old}/${new}}"
}
# payout_chunks: init code of the payout-list data contracts, one per line, from the HOLDERS/AMOUNTS arrays.
# Runtime = 0x00 ‖ (20-byte holder ‖ 6-byte big-endian amount)*, at most 828 entries per contract; init code
# prefix 61 <runtime length> 80 60 0a 3d 39 3d f3 (copies the runtime that follows and returns it).
payout_chunks() {
    local data="00" n=0 i holder
    for i in "${!HOLDERS[@]}"; do
        holder="${HOLDERS[$i]#0x}"
        data="${data}${holder,,}$(printf '%012x' "${AMOUNTS[$i]}")"
        n=$((n + 1))
        if (( n == 828 )); then
            printf '    "0x61%04x80600a3d393df3%s"\n' "$(( ${#data} / 2 ))" "$data"
            data="00"
            n=0
        fi
    done
    if (( n > 0 )); then
        printf '    "0x61%04x80600a3d393df3%s"\n' "$(( ${#data} / 2 ))" "$data"
    fi
}
render_move() {
    local out="$1" impl burn rendered kv safe_nonce
    impl="$(cat "${BIN_DIR}/ZkMinterBurnerPayout.bin")"
    impl="$(subst_address "$impl" 0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe "$L2_BRIDGE" 3)"               # BRIDGE
    impl="$(subst_address "$impl" 0x37eAA0eF3549a5Bb7D431be78a3D99BD360d19e5 "$CIRCLE_USDCE" 3)"            # USDCE
    impl="$(subst_address "$impl" 0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB "$CIRCLE_L1_ESCROW" 3)"        # L1_ESCROW
    burn="$(cat "${BIN_DIR}/UsdcRecoveryBurn.bin")"
    burn="$(subst_address "$burn" 0xBDa0B27f93B2FD3f076725b89cf02e48609bC189 "$CIRCLE_ZK_MINTER_BURNER" 5)" # ZK_MINTER_BURNER
    safe_nonce="$(l2_call "$CIRCLE_SAFE" "nonce()(uint256)")"
    rendered="$(cat "${BIN_DIR}/move.sh.tpl")"
    # The executor and the approver are the first two Safe owners: with threshold 2, one approveHash.
    for kv in "BRIDGE=${L2_BRIDGE}" "ZK_MINTER_BURNER=${CIRCLE_ZK_MINTER_BURNER}" \
              "OLD_IMPL=${CIRCLE_ZK_MINTER_BURNER_IMPL}" "USDCE=${CIRCLE_USDCE}" "USDCE_IMPL=${CIRCLE_USDCE_IMPL}" \
              "MULTISEND=${CIRCLE_MULTISEND}" "SAFE=${CIRCLE_SAFE}" "SAFE_NONCE=${safe_nonce}" \
              "THRESHOLD=${CIRCLE_SAFE_THRESHOLD}" "L1_ESCROW=${CIRCLE_L1_ESCROW}" \
              "EXECUTOR=${CIRCLE_SAFE_OWNER_1}" "APPROVERS=${CIRCLE_SAFE_OWNER_2}" \
              "COUNT=${PAYOUT_COUNT}" "TOTAL=${CIRCLE_PAYOUT_TOTAL}" \
              "IMPL_BYTECODE=0x${impl}" "BURN_BYTECODE=0x${burn}" "CHUNKS=$(payout_chunks)"; do
        rendered="${rendered//@@${kv%%=*}@@/${kv#*=}}"
    done
    if grep -q '@@[A-Z_]*@@' <<<"$rendered"; then
        log_error "move.sh.tpl: placeholder(s) not rendered: $(grep -o '@@[A-Z_]*@@' <<<"$rendered" | sort -u | tr '\n' ' ')"
        exit 1
    fi
    printf '%s\n' "$rendered" > "$out"
    chmod +x "$out"
}

# build_circle_kit_case: the kit case, end to end. Sets KIT_PACKAGE (the verified .tgz) and KIT_RECOVERY (the
# recovery.json the kit wrote). The forks are test forks (--allow-test-fork, another bridge address): the L2
# is not zkEVM mainnet, so the package is valid for this e2e only.
build_circle_kit_case() {
    local case_id="kurtosis-circle-usdc"
    local case_dir="${KIT_V1_DIR}/cases/${case_id}"
    local work fork_block nonce gen_log
    work="$(mktemp -d)"
    fork_block="$(cast block-number --rpc-url "$L2_RPC")"
    # The kit only accepts a fork whose block is the head of the L2: it must be frozen.
    sleep 3
    if [[ "$(cast block-number --rpc-url "$L2_RPC")" != "$fork_block" ]]; then
        log_error "the L2 is still producing blocks (30-stop_sequencer.sh not run?): the kit needs a stable head"
        exit 1
    fi

    log_info "🧰 Kit case ${case_id}: ${PAYOUT_COUNT} USDC.e holders paid through ZkMinterBurner ${CIRCLE_ZK_MINTER_BURNER} (L2 fork at block ${fork_block})"
    rm -rf "$case_dir"
    mkdir -p "$case_dir"
    render_move "${case_dir}/move.sh"
    echo '[]' > "${case_dir}/signatures.json"

    local -a fork_env=(L2_RPC_URL="$L2_RPC" ANVIL_PORT="$KIT_ANVIL_PORT" FORK_BLOCK="$fork_block" BLOCK_TIME=1
        RPC_URL="http://127.0.0.1:${KIT_ANVIL_PORT}" FORK_LOG="${work}/fork.log" BRIDGE_ADDRESS="$L2_BRIDGE")

    # First run, without signatures: the kit runs move.sh on the fork and writes recovery.json from its
    # BridgeEvents, then stops because nobody signed yet. That is the file the signers read and sign.
    log_info "📝 generate.sh: run move.sh on the fork; the kit writes recovery.json from the BridgeEvents"
    gen_log="${work}/generate.log"
    if env "${fork_env[@]}" "${KIT_V1_DIR}/submission/helpers/generate.sh" "$case_dir" \
        --out-dir "${work}/generated" --allow-test-fork </dev/null >"$gen_log" 2>&1; then
        log_error "generate.sh passed without any signature: the Safe owners must be required to sign"
        cat "$gen_log" >&2
        exit 1
    fi
    if [[ ! -f "${case_dir}/recovery.json" ]]; then
        log_error "the kit did not write ${case_dir}/recovery.json:"
        cat "$gen_log" >&2
        exit 1
    fi
    check "the kit stopped only because the Safe owners have not signed yet" \
        grep -q "have no signature" "$gen_log"
    check "the kit wrote recovery.json version 1 with ${PAYOUT_COUNT} outputs" \
        jq -e --argjson n "$PAYOUT_COUNT" '.version == 1 and (.outputs | length == $n)' "${case_dir}/recovery.json" >/dev/null

    # The signatures are bound to recovery.json and move.sh, so both are final here. Throwaway keystores.
    nonce="e2e-circle-usdc-$(date -u +%Y%m%d%H%M%S)"
    cast wallet import executor --private-key "$CIRCLE_SAFE_OWNER_KEY_1" --unsafe-password pw --keystore-dir "${work}/ks" >/dev/null
    cast wallet import approver --private-key "$CIRCLE_SAFE_OWNER_KEY_2" --unsafe-password pw --keystore-dir "${work}/ks" >/dev/null
    ADDRESS="$CIRCLE_SAFE_OWNER_1" NONCE="$nonce" run_quiet \
        "${KIT_V1_DIR}/ownership-proof/sign-message.sh" "${case_dir}/signatures.json" --keystore "${work}/ks/executor" --password pw
    ADDRESS="$CIRCLE_SAFE_OWNER_2" NONCE="$nonce" run_quiet \
        "${KIT_V1_DIR}/ownership-proof/sign-message.sh" "${case_dir}/signatures.json" --keystore "${work}/ks/approver" --password pw

    log_info "📦 prepare-submission.sh (fresh fork, runs move.sh again, packs the .tgz)"
    run_quiet env "${fork_env[@]}" "${KIT_V1_DIR}/submission/prepare-submission.sh" "$case_dir" \
        --allow-test-fork --output-dir "${work}/out" </dev/null
    KIT_PACKAGE="$(find "${work}/out" -maxdepth 1 -name "${case_id}-*.tgz" | sort | tail -n 1)"
    [[ -f "$KIT_PACKAGE" ]] || { log_error "prepare-submission.sh did not produce a .tgz in ${work}/out"; exit 1; }

    log_info "🛂 verify-submission.sh (what Polygon runs on the package)"
    run_quiet env "${fork_env[@]}" "${KIT_V1_DIR}/submission/verify-submission.sh" "$KIT_PACKAGE" \
        --allow-test-fork </dev/null
    mkdir -p "${TMP_DIR}/kit-packages"
    cp "$KIT_PACKAGE" "${TMP_DIR}/kit-packages/"
    KIT_PACKAGE="${TMP_DIR}/kit-packages/$(basename "$KIT_PACKAGE")"
    KIT_RECOVERY="${TMP_DIR}/kit-packages/${case_id}-recovery.json"
    cp "${case_dir}/recovery.json" "$KIT_RECOVERY"
    rm -rf "$work"
}

log_info "📝 Delta ${INDEX}: recover the USDC.e of ${PAYOUT_COUNT} EOA holders (${CIRCLE_PAYOUT_TOTAL} base units) with Message exits, chained on ${PREV_OUTPUT}"
build_circle_kit_case
[[ -d "$CASES_DIR" ]] || { log_error "cases dir not found: $CASES_DIR (deltas 1 and 2 put their cases there)"; exit 1; }
cp "$KIT_PACKAGE" "$CASES_DIR/"

EXPECTED_EXITS="$(expected_exits)"
log_info "🔎 Checking the kit package"
check "the kit's outputs are the ${PAYOUT_COUNT} payouts: Message, (L2, ZkMinterBurner) → (0, L1Escrow), amount 0, metadata abi.encode(holder, amount), in payout order" \
    test "$(jq '.outputs' "$KIT_RECOVERY" | outputs_as_exits)" = "$(normalize_exits <<<"$EXPECTED_EXITS")"
check "the package is verified: recovery.json version 1, the 2 Safe owners that sent transactions signed, no contract sender" \
    bash -c 'tar -xzOf "$1" --wildcards "*/manifest.json" | jq -e --argjson n "$2" --arg a "$3" --arg b "$4" '"'"'
        (.outputs | length == $n) and (.contract_senders | length == 0)
        and ((.required_signers | map(ascii_downcase) | sort) == ([$a, $b] | map(ascii_downcase) | sort))'"'"' >/dev/null' \
        _ "$KIT_PACKAGE" "$PAYOUT_COUNT" "$CIRCLE_SAFE_OWNER_1" "$CIRCLE_SAFE_OWNER_2"
check "the recovery.json inside the package is the one the kit wrote" \
    bash -c 'test "$(tar -xzOf "$1" --wildcards "*/recovery.json" | sha256sum)" = "$(sha256sum < "$2")"' _ "$KIT_PACKAGE" "$KIT_RECOVERY"
# The kit works on a fork: the real L2 is untouched (the delta certificate is what moves the funds).
check "the real L2 is untouched: the holders still hold their USDC.e" \
    test "$(l2_call "$CIRCLE_USDCE" "totalSupply()(uint256)")" = "$CIRCLE_TOTAL_DEPOSITED"

PREVIOUS_RUN="$PREV_OUTPUT_REL"
PREV_RUN_ARCHIVE=""
if [[ -n "${ARCHIVE_URI_BASE:-}" ]]; then
    # The archive base, not the run: the tool finds the run of the last settled height by itself.
    PREVIOUS_RUN="${ARCHIVE_URI_BASE%/}"
    PREV_RUN_ARCHIVE="${ARCHIVE_URI_BASE%/}/$(jq -r '.network_id' "$PREV_CERT")/$(printf '%010d' "$(jq -r '.height' "$PREV_CERT")")"
    log_info "📥 previousRun: the archive base ${PREVIOUS_RUN} (expected run: ${PREV_RUN_ARCHIVE})"
fi
jq --arg prev "$PREVIOUS_RUN" --arg exits "$DELTA_EXITS_REL" --arg out "$DELTA_OUTPUT_REL" --arg cases "$CASES_REL" '
    .options.deltaMode = true
    | .options.previousRun = $prev
    | .options.deltaExitsFile = $exits
    | .options.recoveriesPath = $cases
    | .options.outputDir = $out
' "$BASE_CONFIG" > "$DELTA_CONFIG"
rm -rf "$DELTA_OUTPUT"

if [[ ! -x "$BINARY" ]]; then
    log_info "🛠️ Build exit_tool"
    pushd "$AGGKIT_DIR" >/dev/null
    run_quiet make build-exit_certificate
    popd >/dev/null
fi

# --- 1. recoveries: the verified case → delta exits file; legacy cases 1 and 2 are skipped by the ledger ---
PREV_LEDGER_LEN="$(jq 'length' "${PREV_STATE}/recoveries-applied.json")"
PREV_STATE_HASH="$(sha256sum "${PREV_STATE}/step-c-sc-locked-values.json" "${PREV_STATE}/step-f-checks.json" "${PREV_STATE}/recoveries-applied.json")"

log_info "🧾 Apply the recovery (--step recoveries)"
run_quiet "$BINARY" --config "$DELTA_CONFIG" --step recoveries

DELTA_EXITS="${RECOVERIES_OUT}/delta-exits.json"
for f in "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json" \
         "${RECOVERIES_OUT}/step-0-lbt.json" "${RECOVERIES_OUT}/recoveries-applied.json" \
         "${RECOVERIES_OUT}/recoveries-report.md"; do
    [[ -f "$f" ]] || { log_error "recoveries output not found: $f"; exit 1; }
done

log_info "🔎 Checking the recoveries output of delta ${INDEX}"
check "delta-exits.json has exactly the ${PAYOUT_COUNT} new exits (the legacy cases of deltas 1 and 2 are skipped by the ledger)" \
    jq -e --argjson n "$PAYOUT_COUNT" '.bridge_exits | length == $n' "$DELTA_EXITS" >/dev/null
check "the delta exits equal the kit's outputs (its BridgeEvents) field by field and in the same order" \
    test "$(jq '.bridge_exits' "$DELTA_EXITS" | normalize_exits)" = "$(jq '.outputs' "$KIT_RECOVERY" | outputs_as_exits)"
check "every exit is a Message with amount 0 whose metadata is plain hex (no 0x)" \
    jq -e '.bridge_exits | all(.leaf_type == "Message" and .amount == "0" and (.metadata | test("^[0-9a-f]{128}$")))' "$DELTA_EXITS" >/dev/null
check "step C is unchanged: a Message consumes no SC-locked balance" \
    diff <(jq -S . "${PREV_STATE}/step-c-sc-locked-values.json") <(jq -S . "${RECOVERIES_OUT}/step-c-sc-locked-values.json")
check "step F checks are unchanged: a Message moves no token amount" \
    diff <(jq -S . "${PREV_STATE}/step-f-checks.json") <(jq -S . "${RECOVERIES_OUT}/step-f-checks.json")
check "the ledger keeps the ${PREV_LEDGER_LEN} legacy entries of deltas 1 and 2 unchanged" \
    diff <(jq -S . "${PREV_STATE}/recoveries-applied.json") <(jq -S --argjson n "$PREV_LEDGER_LEN" '.[0:$n]' "${RECOVERIES_OUT}/recoveries-applied.json")
check "... and adds one version-1 Message entry per output" \
    jq -e --argjson p "$PREV_LEDGER_LEN" --argjson n "$PAYOUT_COUNT" \
        '(length == $p + $n) and (.[$p:] | all(.version == 1 and .leafType == "Message" and .amount == "0"))' \
        "${RECOVERIES_OUT}/recoveries-applied.json" >/dev/null
check "the legacy ledger entries have no version (old format) and the same case hashes as before" \
    jq -e --argjson p "$PREV_LEDGER_LEN" '.[0:$p] | all(has("version") | not)' "${RECOVERIES_OUT}/recoveries-applied.json" >/dev/null
check "the previous run's state files were not modified" \
    test "$PREV_STATE_HASH" = "$(sha256sum "${PREV_STATE}/step-c-sc-locked-values.json" "${PREV_STATE}/step-f-checks.json" "${PREV_STATE}/recoveries-applied.json")"

# Deterministic: regenerating from the same previous state gives the same files (in another output dir:
# re-running into the same one is refused, which is checked right after).
FIRST_HASH="$(sha256sum "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json")"
RERUN_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}-rerun.json"
RERUN_DIR="${TMP_DIR}/output-kurtosis-delta-${INDEX}-rerun"
rm -rf "$RERUN_DIR"
jq --arg out "./output-kurtosis-delta-${INDEX}-rerun" '.options.outputDir = $out' "$DELTA_CONFIG" > "$RERUN_CONFIG"
run_quiet "$BINARY" --config "$RERUN_CONFIG" --step recoveries
check "regenerating --step recoveries from the same state gives identical files" \
    test "$FIRST_HASH" = "$(sha256sum "${RERUN_DIR}/recoveries/delta-exits.json" "${RERUN_DIR}/recoveries/step-c-sc-locked-values.json" "${RERUN_DIR}/recoveries/step-f-checks.json" | sed "s#${RERUN_DIR}/recoveries#${RECOVERIES_OUT}#")"
RERUN_LOG="$(mktemp)"
if "$BINARY" --config "$DELTA_CONFIG" --step recoveries >"$RERUN_LOG" 2>&1; then RERUN_RC=0; else RERUN_RC=$?; fi
check "re-running --step recoveries into the same output dir is refused" \
    bash -c '[[ "$1" -ne 0 ]] && grep -q -- "already holds the recoveries" "$2"' _ "$RERUN_RC" "$RERUN_LOG"
rm -f "$RERUN_LOG" "$RERUN_CONFIG"
rm -rf "$RERUN_DIR"

# Negative test: a version-1 case the step must refuse writes nothing. A Message that carries value is not
# supported (nothing accounts for it), and neither is a Message sent from another network than the L2.
neg_case() {
    local name="$1" filter="$2" expect="$3" cases out cfg log rc
    cases="${TMP_DIR}/recoveries-cases-negative-${INDEX}-${name}"
    out="${TMP_DIR}/output-kurtosis-delta-${INDEX}-negative-${name}"
    cfg="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}-negative-${name}.json"
    rm -rf "$cases" "$out"
    mkdir -p "${cases}/bad"
    jq "$filter" "$KIT_RECOVERY" > "${cases}/bad/recovery.json"
    printf '#!/bin/bash\n# e2e negative case %s: not executed by exit_certificate\n' "$name" > "${cases}/bad/move.sh"
    jq --arg cases "./recoveries-cases-negative-${INDEX}-${name}" --arg o "./output-kurtosis-delta-${INDEX}-negative-${name}" \
        '.options.recoveriesPath = $cases | .options.outputDir = $o' "$DELTA_CONFIG" > "$cfg"
    log="$(mktemp)"
    if "$BINARY" --config "$cfg" --step recoveries >"$log" 2>&1; then rc=0; else rc=$?; fi
    check "a Message ${name} makes the step fail ('${expect}')" \
        bash -c '[[ "$1" -ne 0 ]] && grep -qi -- "$2" "$3"' _ "$rc" "$expect" "$log"
    check "... and nothing is written (all or nothing)" test ! -e "${out}/recoveries"
    if [[ "$rc" -eq 0 ]] || ! grep -qi -- "$expect" "$log"; then tail -n 5 "$log" >&2; fi
    rm -f "$log" "$cfg"
    rm -rf "$cases"
}
neg_case "with-value" '.outputs[0].amount = "1"' "amount"
neg_case "from-another-network" '.outputs[0].token_info.origin_network = 0' "network"

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES recoveries check(s) failed for delta ${INDEX} (output: $RECOVERIES_OUT)"
    exit 1
fi

# --- 2. the delta certificate ---
log_info "🌇 Generate the delta certificate"
run_quiet "$BINARY" --config "$DELTA_CONFIG"

log_info "📤 Submit the delta certificate and wait for L1 settlement"
run_quiet "$BINARY" --config "$DELTA_CONFIG" --step submit,wait

# --- 3. verification: same chaining checks as deltas 1 and 2 ---
SIGNED="${DELTA_OUTPUT}/exit-certificate-signed.json"
FULL="${DELTA_OUTPUT}/exit-certificate-full.json"
MANIFEST="${DELTA_OUTPUT}/delta-manifest.json"
WAIT="${DELTA_OUTPUT}/step-wait-result.json"
for f in "$SIGNED" "$FULL" "$MANIFEST" "$WAIT" "${DELTA_OUTPUT}/step-g-l2bridgesyncerlite.sqlite"; do
    [[ -f "$f" ]] || { log_error "delta output not found: $f"; exit 1; }
done

log_info "🔎 Checking delta ${INDEX} against the previous run"
PREV_HEIGHT="$(jq -r '.height' "$PREV_CERT")"
PREV_LER="$(jq -r '.new_local_exit_root' "$PREV_CERT")"
PREV_EXITS="$(jq '.bridge_exits | length' "$PREV_CERT")"
DELTA_LER="$(jq -r '.new_local_exit_root' "$SIGNED")"

check "the delta certificate settled" \
    jq -e '.finalStatus == 4 or .finalStatus == "Settled"' "$WAIT" >/dev/null
check "height is the previous height + 1 ($((PREV_HEIGHT + 1)))" \
    jq -e --argjson h "$((PREV_HEIGHT + 1))" '.height == $h' "$SIGNED" >/dev/null
check "prev_local_exit_root is the previous new_local_exit_root ($PREV_LER)" \
    jq -e --arg ler "$PREV_LER" '.prev_local_exit_root == $ler' "$SIGNED" >/dev/null
check "new_local_exit_root changed ($DELTA_LER)" test "$DELTA_LER" != "$PREV_LER"
check "the delta certificate carries only the ${PAYOUT_COUNT} new exits" \
    jq -e --argjson n "$PAYOUT_COUNT" '.bridge_exits | length == $n' "$SIGNED" >/dev/null
# In a certificate the metadata of an exit is its keccak256 hash, not the raw bytes.
check "they are the Message exits of the recovery, in order (origin, destination, amount 0, keccak256 of the metadata)" \
    test "$(jq -c '[.bridge_exits[] | {leaf_type, o: (.token_info.origin_token_address | ascii_downcase), n: .token_info.origin_network,
                     d: (.dest_address | ascii_downcase), dn: .dest_network, amount, metadata: (.metadata | ascii_downcase | ltrimstr("0x"))}]' "$SIGNED")" = \
         "$(jq -r '.[].metadata' <<<"$EXPECTED_EXITS" | while read -r md; do cast keccak "0x${md}"; done | jq -R . | jq -sc \
              --argjson n "$CIRCLE_L2_NETWORK_ID" --arg o "${CIRCLE_ZK_MINTER_BURNER,,}" --arg d "${CIRCLE_L1_ESCROW,,}" \
              '[.[] | {leaf_type: "Message", o: $o, n: $n, d: $d, dn: 0, amount: "0", metadata: (ascii_downcase | ltrimstr("0x"))}]')"
check "the full certificate carries every exit (previous $PREV_EXITS + ${PAYOUT_COUNT})" \
    jq -e --argjson n "$((PREV_EXITS + PAYOUT_COUNT))" '.bridge_exits | length == $n' "$FULL" >/dev/null
check "the full certificate has the latest new_local_exit_root" \
    jq -e --arg ler "$DELTA_LER" '.new_local_exit_root == $ler' "$FULL" >/dev/null
check "the previous exits are untouched and keep their order (same leaf indexes)" \
    diff <(jq -S ".bridge_exits[0:${PREV_EXITS}]" "$FULL") <(jq -S '.bridge_exits' "$PREV_CERT")
check "delta-manifest.json links the roots before/after" \
    jq -e --arg b "$PREV_LER" --arg a "$DELTA_LER" '.rootBefore == $b and .rootAfter == $a' "$MANIFEST" >/dev/null
check "delta-manifest.json: the added leaves come after the previous ones" \
    jq -e --argjson n "$PREV_EXITS" '.firstDepositCount >= $n' "$MANIFEST" >/dev/null
if [[ -n "$PREV_RUN_ARCHIVE" ]]; then
    check "the tool found the latest archived run from the archive base: delta-manifest.json records ${PREV_RUN_ARCHIVE}" \
        jq -e --arg u "$PREV_RUN_ARCHIVE" '.previousRunArchive == $u and (.previousRunArchiveManifestSha256 | length == 64)' \
            "$MANIFEST" >/dev/null
fi
check "the L2 itself is unchanged (the certificate moved the funds, not the chain): USDC.e supply ${CIRCLE_TOTAL_DEPOSITED}" \
    test "$(l2_call "$CIRCLE_USDCE" "totalSupply()(uint256)")" = "$CIRCLE_TOTAL_DEPOSITED"

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES check(s) failed for delta ${INDEX} (output: $DELTA_OUTPUT)"
    exit 1
fi
log_info "✅ Delta ${INDEX} (Circle USDC.e, ${PAYOUT_COUNT} Message exits) generated, settled and chained on delta $((INDEX - 1))"
