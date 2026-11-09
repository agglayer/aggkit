#!/usr/bin/env bash
# Generates, submits and verifies a DELTA exit certificate (options.deltaMode) chained on the previous
# run, whose exits come from a verified SC-locked-funds RECOVERY (`--step recoveries`).
# Usage: 52-generate_delta_certificate.sh [INDEX]   (default 1)
#
# 20-prepare_network.sh (DELTA_SCENARIO=1) locked ETH in two smart contracts on L2 and the first
# certificate left it behind (skipSCLockedValue=true). Each delta releases one of them to its owner,
# like a zkevm-proof-of-ownership-kit case already verified by the team:
#   - INDEX 1 chains on the first run and recovers sink #1 (case delivered as a kit-style .tgz);
#   - INDEX 2 chains on delta 1 and recovers sink #2 (case delivered as a directory). The cases dir
#     then also holds case 1, which the ledger must skip: only the new exit is issued.
#
# For each delta it:
#   1. writes the recovery case under tmp/recoveries-cases and a derived config
#      (tmp/exit_certificate-kurtosis-delta-INDEX.json, output dir output-kurtosis-delta-INDEX),
#   2. runs `--step recoveries` and checks the balance state it writes (step C / step F / ledger / delta
#      exits file), that it is deterministic, that it never touches the previous run, and that it refuses
#      a case that must not be applied (negative test),
#   3. runs the tool (CHECK → F → G2 → H → I → SIGN), submits it and waits for L1 settlement,
#   4. verifies the chaining: height, prev/new local exit root, the delta certificate carries only the
#      new exit, and exit-certificate-full.json carries every exit (previous + new).
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
       RECOVERED_SO_FAR="$DELTA_SC_LOCKED_AMOUNT_1" ;;
    2) LOCKED="$DELTA_SC_LOCKED_ADDRESS_2"; AMOUNT="$DELTA_SC_LOCKED_AMOUNT_2"; OWNER="$DELTA_OWNER_ADDRESS_2"
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

# write_case <dir> <locked> <destination> <amount>: a kit-style case (recovery.json + move.sh).
write_case() {
    mkdir -p "$1"
    jq -n --arg l "$2" --arg d "$3" --arg a "$4" \
        '{locked_contract: $l, destination: $d, funds: [{token: "native", amount: $a}]}' > "$1/recovery.json"
    printf '#!/bin/bash\n# e2e case %s: simulated recovery, not executed by exit_certificate\n' "$2" > "$1/move.sh"
    echo '[]' > "$1/signatures.json"
}

log_info "📝 Delta ${INDEX}: recover ${AMOUNT} wei locked in ${LOCKED} → owner ${OWNER}, chained on ${PREV_OUTPUT}"
if [[ "$INDEX" -eq 1 ]]; then
    rm -rf "$CASES_DIR"
    STAGE="$(mktemp -d)"
    write_case "${STAGE}/case-1" "$LOCKED" "$OWNER" "$AMOUNT"
    mkdir -p "$CASES_DIR"
    tar -C "$STAGE" -czf "${CASES_DIR}/case-1.tgz" case-1   # delivered as the kit's .tgz
    rm -rf "$STAGE"
else
    write_case "${CASES_DIR}/case-${INDEX}" "$LOCKED" "$OWNER" "$AMOUNT"   # delivered as a directory
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
check "the previous run's balance files were not modified" \
    test "$PREV_STATE_HASH" = "$(sha256sum "${PREV_STATE}/step-c-sc-locked-values.json" "${PREV_STATE}/step-f-checks.json")"

# Deterministic: regenerating from the same previous state gives the same files (the step never reads its
# own output, so re-running it is always safe).
FIRST_HASH="$(sha256sum "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json")"
run_quiet "$BINARY" --config "$DELTA_CONFIG" --step recoveries
check "re-running --step recoveries gives identical files" \
    test "$FIRST_HASH" = "$(sha256sum "$DELTA_EXITS" "${RECOVERIES_OUT}/step-c-sc-locked-values.json" "${RECOVERIES_OUT}/step-f-checks.json")"

# Negative test: a case that must NOT be applied makes the step fail and write nothing. Delta 1: more than
# what is left pending. Delta 2: sink #1 again (already recovered by case 1, recorded in the ledger).
NEG_CASES="${TMP_DIR}/recoveries-cases-negative-${INDEX}"
NEG_OUTPUT_REL="./output-kurtosis-delta-${INDEX}-negative"
rm -rf "$NEG_CASES" "${TMP_DIR}/output-kurtosis-delta-${INDEX}-negative"
if [[ "$INDEX" -eq 1 ]]; then
    write_case "${NEG_CASES}/too-much" "0x00000000000000000000000000000000deadbeef" "$OWNER" "$(echo "$PREV_PENDING + 1" | bc)"
    NEG_EXPECT="exceeds pendingSCLockedBalance"
else
    write_case "${NEG_CASES}/again" "$DELTA_SC_LOCKED_ADDRESS_1" "$OWNER" "1"
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
