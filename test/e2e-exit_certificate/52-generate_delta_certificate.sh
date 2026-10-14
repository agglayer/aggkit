#!/usr/bin/env bash
# Generates, submits and verifies a DELTA exit certificate (options.deltaMode) chained on the previous
# run. Usage: 52-generate_delta_certificate.sh [INDEX]   (default 1)
#
# The delta recovers part of the ETH that 20-prepare_network.sh locked in a smart contract on L2
# (DELTA_SCENARIO=1) and that the first certificate left behind (skipSCLockedValue=true):
#   - INDEX 1 chains on the first run (tmp/exit_certificate-kurtosis.json) and recovers half of it;
#   - INDEX 2 chains on delta 1 and recovers the other half.
# Funds go to the config's exitAddress, which claim-all.sh claims for.
#
# For each delta it:
#   1. writes the delta exits file (tmp/delta-exits-INDEX.json) and a derived config
#      (tmp/exit_certificate-kurtosis-delta-INDEX.json, output dir output-kurtosis-delta-INDEX),
#   2. runs the tool (CHECK → F → G2 → H → I → SIGN), then submits it and waits for L1 settlement,
#   3. verifies the chaining: height, prev/new local exit root, the delta certificate carries only the
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

command -v jq >/dev/null || { log_error "jq is required"; exit 1; }
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

# --- delta exits: half of the locked amount (delta 1), the rest (delta 2) ---
HALF=$((DELTA_SC_LOCKED_AMOUNT / 2))
case "$INDEX" in
    1) DELTA_AMOUNT="$HALF" ;;
    2) DELTA_AMOUNT=$((DELTA_SC_LOCKED_AMOUNT - HALF)) ;;
    *) log_error "unsupported delta INDEX '$INDEX' (1 or 2)"; exit 1 ;;
esac
EXIT_ADDRESS="$(jq -re '.exitAddress' "$BASE_CONFIG")"
DELTA_EXITS_REL="delta-exits-${INDEX}.json"
DELTA_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-delta-${INDEX}.json"
DELTA_OUTPUT_REL="./output-kurtosis-delta-${INDEX}"
DELTA_OUTPUT="${TMP_DIR}/output-kurtosis-delta-${INDEX}"

log_info "📝 Delta ${INDEX}: ${DELTA_AMOUNT} wei (native) → ${EXIT_ADDRESS}, chained on ${PREV_OUTPUT}"
# Native token (origin network 0, zero address) with no metadata: the tool derives it from the L2 bridge.
jq -n --arg dest "$EXIT_ADDRESS" --arg amount "$DELTA_AMOUNT" '{
    bridge_exits: [{
        leaf_type: "Transfer",
        token_info: {origin_network: 0, origin_token_address: "0x0000000000000000000000000000000000000000"},
        dest_network: 0,
        dest_address: $dest,
        amount: $amount
    }]
}' > "${TMP_DIR}/${DELTA_EXITS_REL}"

jq --arg prev "$PREV_OUTPUT_REL" --arg exits "./${DELTA_EXITS_REL}" --arg out "$DELTA_OUTPUT_REL" '
    .options.deltaMode = true
    | .options.previousRun = $prev
    | .options.deltaExitsFile = $exits
    | .options.outputDir = $out
' "$BASE_CONFIG" > "$DELTA_CONFIG"
rm -rf "$DELTA_OUTPUT"

if [[ ! -x "$BINARY" ]]; then
    log_info "🛠️ Build exit_tool"
    pushd "$AGGKIT_DIR" >/dev/null
    run_quiet make build-exit_certificate
    popd >/dev/null
fi

log_info "🌇 Generate the delta certificate"
run_quiet "$BINARY" --config "$DELTA_CONFIG"

log_info "📤 Submit the delta certificate and wait for L1 settlement"
run_quiet "$BINARY" --config "$DELTA_CONFIG" --step submit,wait

# --- verification ---
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
check "the delta exit amount is $DELTA_AMOUNT" \
    jq -e --arg a "$DELTA_AMOUNT" '.bridge_exits[0].amount == $a' "$SIGNED" >/dev/null

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
log_info "✅ Delta ${INDEX} generated, settled and chained on the previous certificate"
