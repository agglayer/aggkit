#!/usr/bin/env bash
# Generates, submits and verifies a DELTA exit certificate (options.deltaMode) chained on the previous
# run, whose exits come from a verified SC-locked-funds RECOVERY (`--step recoveries`).
# Usage: 52-generate_delta_certificate.sh [INDEX]   (default 1)
#
# 20-prepare_network.sh (DELTA_SCENARIO=1) locked ETH in two Vault contracts on L2 and the first
# certificate left it behind (skipSCLockedValue=true). Each delta releases one of them to its owner, with a
# REAL zkevm-proof-of-ownership-kit submission (the kit repo, KIT_DIR):
#   - INDEX 1 chains on the first run and recovers vault #1 (the case reaches the tool as the kit's .tgz);
#   - INDEX 2 chains on delta 1 and recovers vault #2 (the case reaches the tool as an unpacked directory).
#     The cases dir then also holds case 1, which the ledger must skip: only the new exit is issued.
#
# For each delta it:
#   1. builds the recovery with the kit: recovery.json + move.sh (the vault owner withdraws to the
#      destination), signatures of the owner and the destination, `prepare-submission.sh` (simulates move.sh on
#      a shadow-fork of the frozen L2 and packs the .tgz) and `verify-submission.sh` (what Polygon runs on it),
#   2. writes a derived config (tmp/exit_certificate-kurtosis-delta-INDEX.json, output dir
#      output-kurtosis-delta-INDEX) whose recoveriesPath is the kit's verified package,
#   3. runs `--step recoveries` and checks the balance state it writes (step C / step F / ledger / delta
#      exits file), that it is deterministic, that it never touches the previous run, and that it refuses
#      a case that must not be applied (negative test),
#   4. runs the tool (CHECK → F → G2 → H → I → SIGN), submits it and waits for L1 settlement,
#   5. verifies the chaining: height, prev/new local exit root, the delta certificate carries only the
#      new exit, and exit-certificate-full.json carries every exit (previous + new).
#
# Needs the kit (it is not part of this repo): KIT_DIR (default: ../zkevm-proof-of-ownership-kit next to this
# repo) with docker, cast and jq. The fork the kit starts reads the L2 over RPC, which is frozen (30-stop_sequencer.sh)
# so its head is stable, as the kit requires. KIT_ANVIL_PORT (default 18546) is the port of that fork.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TMP_DIR="${AGGKIT_DIR}/tmp"
BASE_CONFIG="${EXIT_CERT_CONFIG:-${TMP_DIR}/exit_certificate-kurtosis.json}"
DELTA_STATE_FILE="${DELTA_STATE_FILE:-${TMP_DIR}/exit_certificate-e2e-delta.env}"
INDEX="${1:-1}"
BINARY="${AGGKIT_DIR}/target/exit_certificate"
NATIVE_TOKEN="0x0000000000000000000000000000000000000000"

command -v jq >/dev/null || { log_error "jq is required"; exit 1; }
command -v bc >/dev/null || { log_error "bc is required (amounts exceed 64 bits)"; exit 1; }
for f in "$BASE_CONFIG" "$DELTA_STATE_FILE"; do
    if [[ ! -f "$f" ]]; then
        log_error "required file not found: $f"
        log_error "Run the e2e with --delta (20-prepare_network.sh with DELTA_SCENARIO=1, 40-generate with SKIP_SC_LOCKED_VALUE=1)."
        exit 1
    fi
done
# shellcheck source=/dev/null
source "$DELTA_STATE_FILE"

KIT_DIR="${KIT_DIR:-$(cd "${AGGKIT_DIR}/.." && pwd)/zkevm-proof-of-ownership-kit}"
KIT_ANVIL_PORT="${KIT_ANVIL_PORT:-18546}"
for f in submission/prepare-submission.sh submission/verify-submission.sh ownership-proof/sign-message.sh; do
    if [[ ! -f "${KIT_DIR}/${f}" ]]; then
        log_error "zkevm-proof-of-ownership-kit not found: ${KIT_DIR}/${f} is missing"
        log_error "Set KIT_DIR to a checkout of the kit (with submission/prepare-submission.sh)."
        exit 1
    fi
done
for bin in docker cast; do
    command -v "$bin" >/dev/null || { log_error "$bin is required by the kit"; exit 1; }
done

# --- previous run (the base run for delta 1, delta INDEX-1 otherwise) ---
if [[ "$INDEX" -eq 1 ]]; then
    PREV_CONFIG="$BASE_CONFIG"
else
    PREV_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-$((INDEX - 1)).json"
    [[ -f "$PREV_CONFIG" ]] || { log_error "previous delta config not found: $PREV_CONFIG"; exit 1; }
fi
# Both configs live in tmp/, so the previous run's outputDir (relative to its config) is valid as is.
PREV_OUTPUT_REL="$(jq -r '.options.outputDir' "$PREV_CONFIG")"
[[ "$PREV_OUTPUT_REL" == /* ]] && PREV_OUTPUT="$PREV_OUTPUT_REL" || PREV_OUTPUT="${TMP_DIR}/${PREV_OUTPUT_REL}"
PREV_CERT="${PREV_OUTPUT}/exit-certificate-full.json"
[[ -f "$PREV_CERT" ]] || PREV_CERT="${PREV_OUTPUT}/exit-certificate-signed.json"
[[ -f "$PREV_CERT" ]] || { log_error "previous run certificate not found in $PREV_OUTPUT"; exit 1; }
PREV_WAIT="${PREV_OUTPUT}/step-wait-result.json"
[[ -f "$PREV_WAIT" ]] || { log_error "previous run is not settled ($PREV_WAIT missing)"; exit 1; }

# Where the recoveries step reads its starting balance state from (see recoveriesStateDir): the first
# run's own files for delta 1, the previous delta's recoveries/ subdirectory afterwards.
if [[ "$INDEX" -eq 1 ]]; then PREV_STATE="$PREV_OUTPUT"; else PREV_STATE="${PREV_OUTPUT}/recoveries"; fi

# --- this delta's recovery: one locked contract released to its owner ---
case "$INDEX" in
    1) LOCKED="$DELTA_SC_LOCKED_ADDRESS_1"; AMOUNT="$DELTA_SC_LOCKED_AMOUNT_1"; OWNER="$DELTA_OWNER_ADDRESS_1"
       OWNER_KEY="$DELTA_OWNER_KEY_1"
       RECOVERED_SO_FAR="$DELTA_SC_LOCKED_AMOUNT_1" ;;
    2) LOCKED="$DELTA_SC_LOCKED_ADDRESS_2"; AMOUNT="$DELTA_SC_LOCKED_AMOUNT_2"; OWNER="$DELTA_OWNER_ADDRESS_2"
       OWNER_KEY="$DELTA_OWNER_KEY_2"
       RECOVERED_SO_FAR="$(echo "$DELTA_SC_LOCKED_AMOUNT_1 + $DELTA_SC_LOCKED_AMOUNT_2" | bc)" ;;
    *) log_error "unsupported delta INDEX '$INDEX' (1 or 2)"; exit 1 ;;
esac
CASES_REL="./recoveries-cases"
CASES_DIR="${TMP_DIR}/recoveries-cases"
DELTA_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}.json"
DELTA_OUTPUT_REL="./output-kurtosis-delta-${INDEX}"
DELTA_OUTPUT="${TMP_DIR}/output-kurtosis-delta-${INDEX}"
RECOVERIES_OUT="${DELTA_OUTPUT}/recoveries"
DELTA_EXITS_REL="${DELTA_OUTPUT_REL}/recoveries/delta-exits.json"

# write_case <dir> <locked> <destination> <amount>: a stub kit-style case (recovery.json + move.sh), NOT verified
# by the kit. Only for the negative tests below: the cases the recoveries step must refuse.
write_case() {
    mkdir -p "$1"
    jq -n --arg l "$2" --arg d "$3" --arg a "$4" \
        '{locked_contract: $l, destination: $d, funds: [{token: "native", amount: $a}]}' > "$1/recovery.json"
    printf '#!/bin/bash\n# e2e case %s: simulated recovery, not executed by exit_certificate\n' "$2" > "$1/move.sh"
    echo '[]' > "$1/signatures.json"
}

# build_kit_case <index> <locked> <destination> <destination key> <amount>: a recovery of the zkevm-proof-of-ownership-kit,
# end to end. recovery.json + move.sh (the vault owner withdraws everything to the destination), signed by the
# owner and by the destination (the kit demands both), then prepare-submission.sh (runs move.sh on a fresh
# shadow-fork of the L2 and packs the .tgz) and verify-submission.sh (what Polygon runs on the package).
# Sets KIT_PACKAGE to the verified .tgz. The forks are test forks (--allow-test-fork): the L2 is not zkEVM
# mainnet, so the packages are valid for this e2e only.
build_kit_case() {
    local idx="$1" locked="$2" dest="$3" dest_key="$4" amount="$5"
    local case_id="kurtosis-delta-${idx}"
    local case_dir="${KIT_DIR}/cases/${case_id}"
    local work l2_rpc fork_block nonce
    work="$(mktemp -d)"
    l2_rpc="$(jq -r '.l2RpcUrl' "$BASE_CONFIG")"
    fork_block="$(cast block-number --rpc-url "$l2_rpc")"
    # The kit only accepts a fork whose block is the head of the L2: it must be frozen.
    sleep 3
    if [[ "$(cast block-number --rpc-url "$l2_rpc")" != "$fork_block" ]]; then
        log_error "the L2 is still producing blocks (30-stop_sequencer.sh not run?): the kit needs a stable head"
        exit 1
    fi

    log_info "🧰 Kit case ${case_id}: ${locked} → ${dest} (${amount} wei), L2 fork at block ${fork_block}"
    rm -rf "$case_dir"
    mkdir -p "$case_dir"
    jq -n --arg l "$locked" --arg d "$dest" --arg a "$amount" \
        '{locked_contract: $l, destination: $d, funds: [{token: "native", amount: $a}]}' > "${case_dir}/recovery.json"
    cat > "${case_dir}/move.sh" <<MOVE
#!/bin/bash
# Kit case ${case_id} of the exit_certificate delta e2e: the owner of the Vault releases its ETH.
set -euo pipefail
RPC_URL="\${RPC_URL:-http://127.0.0.1:8545}"
cast rpc anvil_impersonateAccount "${DELTA_VAULT_OWNER_ADDRESS}" --rpc-url "\${RPC_URL}" >/dev/null
cast send --unlocked --from "${DELTA_VAULT_OWNER_ADDRESS}" "${locked}" "withdraw(address,uint256)" "${dest}" "${amount}" \\
    --json --rpc-url "\${RPC_URL}"
MOVE
    chmod +x "${case_dir}/move.sh"
    echo '[]' > "${case_dir}/signatures.json"

    # The signatures are bound to recovery.json and move.sh, so both are final here. Throwaway keystores.
    nonce="e2e-delta-${idx}-$(date -u +%Y%m%d%H%M%S)"
    cast wallet import owner --private-key "$DELTA_VAULT_OWNER_KEY" --unsafe-password pw --keystore-dir "${work}/ks" >/dev/null
    cast wallet import dest --private-key "$dest_key" --unsafe-password pw --keystore-dir "${work}/ks" >/dev/null
    ADDRESS="$DELTA_VAULT_OWNER_ADDRESS" NONCE="$nonce" run_quiet \
        "${KIT_DIR}/ownership-proof/sign-message.sh" "${case_dir}/signatures.json" --keystore "${work}/ks/owner" --password pw
    ADDRESS="$dest" NONCE="$nonce" run_quiet \
        "${KIT_DIR}/ownership-proof/sign-message.sh" "${case_dir}/signatures.json" --keystore "${work}/ks/dest" --password pw

    local -a fork_env=(L2_RPC_URL="$l2_rpc" ANVIL_PORT="$KIT_ANVIL_PORT" FORK_BLOCK="$fork_block" BLOCK_TIME=1
        RPC_URL="http://127.0.0.1:${KIT_ANVIL_PORT}" FORK_LOG="${work}/fork.log")
    log_info "📦 prepare-submission.sh (shadow-fork of the L2, runs move.sh, packs the .tgz)"
    run_quiet env "${fork_env[@]}" "${KIT_DIR}/submission/prepare-submission.sh" "$case_dir" \
        --allow-test-fork --output-dir "${work}/out" </dev/null
    KIT_PACKAGE="$(find "${work}/out" -maxdepth 1 -name "${case_id}-*.tgz" | sort | tail -n 1)"
    [[ -f "$KIT_PACKAGE" ]] || { log_error "prepare-submission.sh did not produce a .tgz in ${work}/out"; exit 1; }

    log_info "🛂 verify-submission.sh (what Polygon runs on the package)"
    run_quiet env "${fork_env[@]}" "${KIT_DIR}/submission/verify-submission.sh" "$KIT_PACKAGE" \
        --allow-test-fork </dev/null
    # Keep the verified package outside the kit tree and the tmp work dir.
    mkdir -p "${TMP_DIR}/kit-packages"
    cp "$KIT_PACKAGE" "${TMP_DIR}/kit-packages/"
    KIT_PACKAGE="${TMP_DIR}/kit-packages/$(basename "$KIT_PACKAGE")"
    rm -rf "$work"
}

log_info "📝 Delta ${INDEX}: recover ${AMOUNT} wei locked in ${LOCKED} → owner ${OWNER}, chained on ${PREV_OUTPUT}"
build_kit_case "$INDEX" "$LOCKED" "$OWNER" "$OWNER_KEY" "$AMOUNT"
if [[ "$INDEX" -eq 1 ]]; then
    rm -rf "$CASES_DIR"
    mkdir -p "$CASES_DIR"
    cp "$KIT_PACKAGE" "$CASES_DIR/"                  # delivered as the kit's .tgz
else
    mkdir -p "$CASES_DIR"
    tar -xzf "$KIT_PACKAGE" -C "$CASES_DIR"          # delivered as an unpacked directory
fi

jq --arg prev "$PREV_OUTPUT_REL" --arg exits "$DELTA_EXITS_REL" --arg out "$DELTA_OUTPUT_REL" --arg cases "$CASES_REL" '
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

# native_field <file> <field>: a field of the native-token entry of step-c / step-f-checks (strings only:
# amounts exceed jq's double precision, so they are never parsed as numbers).
native_field() {
    jq -r --arg z "$NATIVE_TOKEN" --arg f "$2" \
        '.[] | select(.originNetwork == 0 and (.originTokenAddress | ascii_downcase) == $z) | .[$f] // ""' "$1"
}

# --- 1. recoveries: verified case → delta exits file + updated balance state ---
PREV_PENDING="$(native_field "${PREV_STATE}/step-c-sc-locked-values.json" pendingSCLockedBalance)"
PREV_CERT_AMOUNT="$(native_field "${PREV_STATE}/step-f-checks.json" certificateAmount)"
PREV_SKIPPED="$(native_field "${PREV_STATE}/step-f-checks.json" skippedSCLockedAmount)"
PREV_STATE_HASH="$(sha256sum "${PREV_STATE}/step-c-sc-locked-values.json" "${PREV_STATE}/step-f-checks.json")"
if [[ "$INDEX" -eq 1 ]]; then
    TOTAL_LOCKED="$(echo "$DELTA_SC_LOCKED_AMOUNT_1 + $DELTA_SC_LOCKED_AMOUNT_2" | bc)"
    check "the first certificate left behind at least both locked amounts (pending $PREV_PENDING >= $TOTAL_LOCKED)" \
        test "$(echo "$PREV_PENDING >= $TOTAL_LOCKED" | bc)" = 1
fi

log_info "🧾 Apply the recovery (--step recoveries)"
run_quiet "$BINARY" --config "$DELTA_CONFIG" --step recoveries

DELTA_EXITS="${RECOVERIES_OUT}/delta-exits.json"
for f in "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json" \
         "${RECOVERIES_OUT}/step-0-lbt.json" "${RECOVERIES_OUT}/recoveries-applied.json" \
         "${RECOVERIES_OUT}/recoveries-report.md"; do
    [[ -f "$f" ]] || { log_error "recoveries output not found: $f"; exit 1; }
done

log_info "🔎 Checking the recoveries output of delta ${INDEX}"
check "delta-exits.json has exactly the new exit (case $((INDEX)) only; earlier cases are skipped by the ledger)" \
    jq -e '.bridge_exits | length == 1' "$DELTA_EXITS" >/dev/null
check "the exit is $AMOUNT wei of the native token to the owner $OWNER on L1" \
    jq -e --arg a "$AMOUNT" --arg d "$OWNER" --arg z "$NATIVE_TOKEN" '
        .bridge_exits[0] | .amount == $a and (.dest_address | ascii_downcase) == ($d | ascii_downcase)
        and .dest_network == 0 and .token_info.origin_network == 0
        and (.token_info.origin_token_address | ascii_downcase) == $z' "$DELTA_EXITS" >/dev/null
check "step C: pendingSCLockedBalance went down by exactly $AMOUNT" \
    test "$(native_field "${RECOVERIES_OUT}/step-c-sc-locked-values.json" pendingSCLockedBalance)" = \
         "$(echo "$PREV_PENDING - $AMOUNT" | bc)"
check "step C: recoveredSCLockedBalance is the total recovered so far ($RECOVERED_SO_FAR)" \
    test "$(native_field "${RECOVERIES_OUT}/step-c-sc-locked-values.json" recoveredSCLockedBalance)" = "$RECOVERED_SO_FAR"
check "step F: certificateAmount went up by $AMOUNT" \
    test "$(native_field "${RECOVERIES_OUT}/step-f-checks.json" certificateAmount)" = "$(echo "$PREV_CERT_AMOUNT + $AMOUNT" | bc)"
check "step F: skippedSCLockedAmount went down by $AMOUNT" \
    test "$(native_field "${RECOVERIES_OUT}/step-f-checks.json" skippedSCLockedAmount)" = "$(echo "$PREV_SKIPPED - $AMOUNT" | bc)"
check "the ledger holds the $INDEX applied case(s), the last one for $LOCKED" \
    jq -e --argjson n "$INDEX" --arg l "$LOCKED" \
        'length == $n and (.[-1].lockedContract | ascii_downcase) == ($l | ascii_downcase)' \
        "${RECOVERIES_OUT}/recoveries-applied.json" >/dev/null
check "the kit package of the case is verified and carries the expected recovery (${LOCKED} → ${OWNER}, ${AMOUNT} wei)" \
    bash -c 'tar -xzOf "$1" --wildcards "*/manifest.json" | jq -e --arg l "$2" --arg d "$3" --arg a "$4" '"'"'
        (.locked_contract | ascii_downcase) == ($l | ascii_downcase) and (.destination | ascii_downcase) == ($d | ascii_downcase)
        and (.balances | length == 1) and .balances[0].amount == $a and .balances[0].locked_after == "0"
        and (.required_signers | length == 2)'"'"' >/dev/null' _ "$KIT_PACKAGE" "$LOCKED" "$OWNER" "$AMOUNT"
check "the previous run's balance files were not modified" \
    test "$PREV_STATE_HASH" = "$(sha256sum "${PREV_STATE}/step-c-sc-locked-values.json" "${PREV_STATE}/step-f-checks.json")"

# Deterministic: regenerating from the same previous state gives the same files. It goes to ANOTHER output dir:
# re-running into the same one is refused (it would overwrite the state and drop earlier cases), which is
# checked right after.
FIRST_HASH="$(sha256sum "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json")"
RERUN_OUTPUT_REL="./output-kurtosis-delta-${INDEX}-rerun"
RERUN_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}-rerun.json"
rm -rf "${TMP_DIR}/output-kurtosis-delta-${INDEX}-rerun"
jq --arg out "$RERUN_OUTPUT_REL" '.options.outputDir = $out' "$DELTA_CONFIG" > "$RERUN_CONFIG"
run_quiet "$BINARY" --config "$RERUN_CONFIG" --step recoveries
RERUN_OUT="${TMP_DIR}/output-kurtosis-delta-${INDEX}-rerun/recoveries"
check "regenerating --step recoveries from the same state gives identical files" \
    test "$FIRST_HASH" = "$(sha256sum "${RERUN_OUT}/delta-exits.json" "${RERUN_OUT}/step-c-sc-locked-values.json" "${RERUN_OUT}/step-f-checks.json" | sed "s#${RERUN_OUT}#${RECOVERIES_OUT}#")"
RERUN_LOG="$(mktemp)"
if "$BINARY" --config "$DELTA_CONFIG" --step recoveries >"$RERUN_LOG" 2>&1; then
    RERUN_RC=0
else
    RERUN_RC=$?
fi
check "re-running --step recoveries into the same output dir is refused (it would drop the earlier cases)" \
    bash -c '[[ "$1" -ne 0 ]] && grep -q -- "already holds the recoveries" "$2"' _ "$RERUN_RC" "$RERUN_LOG"
check "... and the first output is left as it was" \
    test "$FIRST_HASH" = "$(sha256sum "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json")"
rm -f "$RERUN_LOG"
rm -rf "${TMP_DIR}/output-kurtosis-delta-${INDEX}-rerun"

# Negative test: a case that must NOT be applied makes the step fail and write nothing. Delta 1: more than
# what is left pending. Delta 2: sink #1 again for the same owner (already recovered by case 1, recorded in the
# ledger; another owner of the same contract would be accepted).
NEG_CASES="${TMP_DIR}/recoveries-cases-negative-${INDEX}"
NEG_OUTPUT_REL="./output-kurtosis-delta-${INDEX}-negative"
rm -rf "$NEG_CASES" "${TMP_DIR}/output-kurtosis-delta-${INDEX}-negative"
if [[ "$INDEX" -eq 1 ]]; then
    write_case "${NEG_CASES}/too-much" "0x00000000000000000000000000000000deadbeef" "$OWNER" "$(echo "$PREV_PENDING + 1" | bc)"
    NEG_EXPECT="exceeds pendingSCLockedBalance"
else
    write_case "${NEG_CASES}/again" "$DELTA_SC_LOCKED_ADDRESS_1" "$DELTA_OWNER_ADDRESS_1" "1"
    NEG_EXPECT="already recovered"
fi
NEG_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}-negative.json"
jq --arg cases "./recoveries-cases-negative-${INDEX}" --arg out "$NEG_OUTPUT_REL" \
    '.options.recoveriesPath = $cases | .options.outputDir = $out' "$DELTA_CONFIG" > "$NEG_CONFIG"
NEG_LOG="$(mktemp)"
if "$BINARY" --config "$NEG_CONFIG" --step recoveries >"$NEG_LOG" 2>&1; then
    NEG_RC=0
else
    NEG_RC=$?
fi
check "a case that must not be applied makes the step fail ('$NEG_EXPECT')" \
    bash -c '[[ "$1" -ne 0 ]] && grep -q -- "$2" "$3"' _ "$NEG_RC" "$NEG_EXPECT" "$NEG_LOG"
check "... and nothing is written (all or nothing)" \
    test ! -e "${TMP_DIR}/output-kurtosis-delta-${INDEX}-negative/recoveries"
rm -f "$NEG_LOG"

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES recoveries check(s) failed for delta ${INDEX} (output: $RECOVERIES_OUT)"
    exit 1
fi

# --- 2. the delta certificate from the delta exits file the recoveries step wrote ---
log_info "🌇 Generate the delta certificate"
run_quiet "$BINARY" --config "$DELTA_CONFIG"

log_info "📤 Submit the delta certificate and wait for L1 settlement"
run_quiet "$BINARY" --config "$DELTA_CONFIG" --step submit,wait

# --- 3. verification ---
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

# agglayertypes.CertificateStatus is serialized as its number (Settled = 4) in step-wait-result.json.
check "the delta certificate settled" \
    jq -e '.finalStatus == 4 or .finalStatus == "Settled"' "$WAIT" >/dev/null
check "height is the previous height + 1 ($((PREV_HEIGHT + 1)))" \
    jq -e --argjson h "$((PREV_HEIGHT + 1))" '.height == $h' "$SIGNED" >/dev/null
check "prev_local_exit_root is the previous new_local_exit_root ($PREV_LER)" \
    jq -e --arg ler "$PREV_LER" '.prev_local_exit_root == $ler' "$SIGNED" >/dev/null
check "new_local_exit_root changed ($DELTA_LER)" test "$DELTA_LER" != "$PREV_LER"
check "the delta certificate carries only the new exit" \
    jq -e '.bridge_exits | length == 1' "$SIGNED" >/dev/null
check "the delta exit is the recovery: $AMOUNT wei to $OWNER" \
    jq -e --arg a "$AMOUNT" --arg d "$OWNER" \
        '.bridge_exits[0] | .amount == $a and (.dest_address | ascii_downcase) == ($d | ascii_downcase)' "$SIGNED" >/dev/null

check "the full certificate carries every exit (previous $PREV_EXITS + 1)" \
    jq -e --argjson n "$((PREV_EXITS + 1))" '.bridge_exits | length == $n' "$FULL" >/dev/null
check "the full certificate has the latest new_local_exit_root" \
    jq -e --arg ler "$DELTA_LER" '.new_local_exit_root == $ler' "$FULL" >/dev/null
check "the previous exits are untouched and keep their order (same leaf indexes)" \
    diff <(jq -S ".bridge_exits[0:${PREV_EXITS}]" "$FULL") <(jq -S '.bridge_exits' "$PREV_CERT")

check "delta-manifest.json links the roots before/after" \
    jq -e --arg b "$PREV_LER" --arg a "$DELTA_LER" '.rootBefore == $b and .rootAfter == $a' "$MANIFEST" >/dev/null
check "delta-manifest.json: the added leaves come after the previous ones" \
    jq -e --argjson n "$PREV_EXITS" '.firstDepositCount >= $n' "$MANIFEST" >/dev/null

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES check(s) failed for delta ${INDEX} (output: $DELTA_OUTPUT)"
    exit 1
fi
log_info "✅ Delta ${INDEX} (recovery of ${LOCKED}) generated, settled and chained on the previous certificate"
