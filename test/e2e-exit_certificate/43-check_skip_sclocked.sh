#!/usr/bin/env bash
# Verifies options.skipSCLockedValue (issue #1726): re-runs the full pipeline from the same frozen
# snapshot used by 40-generate_exit_certificate.sh but with skipSCLockedValue=true and NO
# exitAddress in the config, into a scratch workdir (the step-40 output consumed by the later
# submit/claim steps is never touched). The L2 target block is pinned to the block step 40 resolved
# (step-0-l2_target_block.json) and the L1 cutoff to the current L1 head, so both runs see the same
# on-chain snapshot.
#
# Checks, against the step-40 output:
#   - the pipeline succeeds without exitAddress (it is optional with the flag) and writes the final
#     certificate with a non-zero new_local_exit_root
#   - step-c-sc-locked-values.json is identical to step 40's (Step C still runs; same snapshot)
#   - with N = tokens with pendingSCLockedBalance > 0: the step-40 Step D certificate ends with
#     exactly N exits to exitAddress, and the skip-run Step D certificate equals the step-40 one
#     minus those last N exits (Step D appends the SC-locked exits last)
#   - exits to exitAddress: step-40 count == skip-run count + N
#   - step-f-checks.json: every token matches (strict equality, thanks to the omitted-amount
#     discount) and exactly N entries record a skippedSCLockedAmount
#
# When the snapshot has no SC-locked value (N=0) the run still exercises the config/pipeline path
# but the omission itself is not covered — a warning is emitted.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"

command -v jq >/dev/null || { log_error "jq is required"; exit 1; }
if [[ ! -f "$EXIT_CERT_CONFIG" ]]; then
    log_error "exit_certificate config not found: $EXIT_CERT_CONFIG"
    log_error "Run 40-generate_exit_certificate.sh first."
    exit 1
fi
CONFIG_DIR="$(cd "$(dirname "$EXIT_CERT_CONFIG")" && pwd)"

# Resolve the step-40 output: options.outputDir is relative to the config file dir.
OUTPUT_DIR="$(jq -r '.options.outputDir // "./output"' "$EXIT_CERT_CONFIG")"
[[ "$OUTPUT_DIR" == /* ]] || OUTPUT_DIR="${CONFIG_DIR}/${OUTPUT_DIR}"
STEP40_STEP_D_CERT="${OUTPUT_DIR}/step-d-exit-certificate.json"
STEP40_STEP_C="${OUTPUT_DIR}/step-c-sc-locked-values.json"
STEP40_TARGET_BLOCK_FILE="${OUTPUT_DIR}/step-0-l2_target_block.json"
for f in "$STEP40_STEP_D_CERT" "$STEP40_STEP_C" "$STEP40_TARGET_BLOCK_FILE"; do
    if [[ ! -f "$f" ]]; then
        log_error "step-40 output file not found: $f"
        log_error "Run 40-generate_exit_certificate.sh first."
        exit 1
    fi
done

BINARY="${AGGKIT_DIR}/target/exit_certificate"
if [[ ! -x "$BINARY" ]]; then
    log_info "🛠️ Building exit_certificate"
    pushd "$AGGKIT_DIR" >/dev/null
    run_quiet make build-exit_certificate
    popd >/dev/null
fi

EXIT_ADDR="$(jq -re '.exitAddress' "$EXIT_CERT_CONFIG")"
EXIT_ADDR_LC="${EXIT_ADDR,,}"
TARGET_BLOCK="$(jq -re '.' "$STEP40_TARGET_BLOCK_FILE")"

# Pin the L1 cutoff (unless the step-40 config already pins one) so Steps E/I see a stable L1 view.
L1_END_BLOCK="$(jq -r '.options.l1EndBlock // 0' "$EXIT_CERT_CONFIG")"
L1_RPC_URL="$(jq -r '.l1RpcUrl // empty' "$EXIT_CERT_CONFIG")"
if [[ "$L1_END_BLOCK" == "0" && -n "$L1_RPC_URL" ]]; then
    L1_END_BLOCK="$(curl -s -m 10 -X POST "$L1_RPC_URL" -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' \
        | jq -re '.result' | xargs printf '%d\n')" \
        || { log_error "Cannot resolve L1 latest block from $L1_RPC_URL"; exit 1; }
fi

WORKDIR="$(mktemp -d /tmp/e2e-exit-cert-skip-sclocked.XXXXXX)"
RUN_DIR="${WORKDIR}/output"
RUN_CONFIG="${WORKDIR}/config-skip-sclocked.json"
mkdir -p "$RUN_DIR"

# Derive the skip config from the step-40 one: enable the flag, DROP exitAddress (it must be
# optional now), pin the snapshot and point the output at the scratch dir. signerConfig.Path is
# made absolute (it resolves relative to the config file, which moves into the workdir).
jq --arg out "$RUN_DIR" \
   --arg target "$TARGET_BLOCK" \
   --arg l1end "$L1_END_BLOCK" \
   --arg cfgdir "$CONFIG_DIR" \
   '
    del(.exitAddress)
    | .options.skipSCLockedValue = true
    | .options.outputDir = $out
    | .targetBlock = $target
    | (if $l1end != "0" then .options.l1EndBlock = ($l1end | tonumber) else . end)
    | (if (.signerConfig.Path? // "") != "" and (.signerConfig.Path | startswith("/") | not)
       then .signerConfig.Path = $cfgdir + "/" + .signerConfig.Path else . end)
   ' "$EXIT_CERT_CONFIG" > "$RUN_CONFIG"

log_info "🚀 Running the pipeline with skipSCLockedValue=true and no exitAddress (log: ${WORKDIR}/run.log)"
if ! "$BINARY" --config "$RUN_CONFIG" > "${WORKDIR}/run.log" 2>&1; then
    log_error "Pipeline failed with skipSCLockedValue=true — last log lines (workdir kept: $WORKDIR):"
    tail -10 "${WORKDIR}/run.log" >&2
    exit 1
fi

FAILURES=0
check() { # check <description> <condition-exit-code>
    if [[ "$2" -eq 0 ]]; then
        log_info "  ✅ $1"
    else
        log_error "  ❌ $1"
        FAILURES=$((FAILURES + 1))
    fi
}

SKIP_STEP_D_CERT="${RUN_DIR}/step-d-exit-certificate.json"
SKIP_STEP_C="${RUN_DIR}/step-c-sc-locked-values.json"
SKIP_STEP_F_CHECKS="${RUN_DIR}/step-f-checks.json"
SKIP_FINAL="${RUN_DIR}/exit-certificate-final.json"

log_info "🔎 Checking the skip run against the step-40 output"

[[ -f "$SKIP_FINAL" ]]; check "exit-certificate-final.json written" $?
if [[ -f "$SKIP_FINAL" ]]; then
    LER="$(jq -r '.new_local_exit_root // empty' "$SKIP_FINAL")"
    [[ -n "$LER" && "$LER" != "0x0000000000000000000000000000000000000000000000000000000000000000" ]]
    check "new_local_exit_root is set ($LER)" $?
fi

# Step C still runs with the flag on, and on the same snapshot it must produce the same values.
diff <(jq -S . "$STEP40_STEP_C") <(jq -S . "$SKIP_STEP_C") >/dev/null
check "step-c-sc-locked-values.json identical to step 40's (Step C still runs)" $?

# N = tokens whose SC-locked exit the flag must omit.
N="$(jq '[.[] | select((.pendingSCLockedBalance // "0") != "0")] | length' "$STEP40_STEP_C")"
if [[ "$N" -eq 0 ]]; then
    log_warn "  ⚠️  Snapshot has no SC-locked value (N=0): the omission path is not exercised," \
             "only the config/pipeline path"
else
    log_info "  Snapshot has $N token(s) with SC-locked value to omit"
fi

# Step D appends the SC-locked exits last: the step-40 certificate must end with exactly N exits to
# exitAddress, and the skip certificate must be the step-40 one minus those last N exits.
if [[ "$N" -gt 0 ]]; then
    jq -e --argjson n "$N" --arg exit "$EXIT_ADDR_LC" \
        '[.bridge_exits[-$n:][] | select((.dest_address | ascii_downcase) != $exit)] | length == 0' \
        "$STEP40_STEP_D_CERT" >/dev/null
    check "step-40 Step D certificate ends with $N SC-locked exit(s) to exitAddress ($EXIT_ADDR)" $?
fi
diff <(jq -S --argjson n "$N" '.bridge_exits[0:(.bridge_exits | length) - $n]' "$STEP40_STEP_D_CERT") \
     <(jq -S '.bridge_exits' "$SKIP_STEP_D_CERT") >/dev/null
check "skip-run Step D certificate == step-40 one minus its last $N SC-locked exit(s)" $?

# Exits to exitAddress: everything the flag removed, nothing else.
STEP40_TO_EXIT="$(jq --arg exit "$EXIT_ADDR_LC" \
    '[.bridge_exits[] | select((.dest_address | ascii_downcase) == $exit)] | length' "$STEP40_STEP_D_CERT")"
SKIP_TO_EXIT="$(jq --arg exit "$EXIT_ADDR_LC" \
    '[.bridge_exits[] | select((.dest_address | ascii_downcase) == $exit)] | length' "$SKIP_STEP_D_CERT")"
[[ "$STEP40_TO_EXIT" -eq $((SKIP_TO_EXIT + N)) ]]
check "exits to exitAddress: step-40 has $STEP40_TO_EXIT == skip-run $SKIP_TO_EXIT + $N omitted" $?

# Step F: the discount keeps the balance check on strict equality, and records what was omitted.
jq -e 'all(.[]; .match)' "$SKIP_STEP_F_CHECKS" >/dev/null
check "step-f-checks.json: every token matches (LBT discount applied)" $?
SKIPPED_ENTRIES="$(jq '[.[] | select(.skippedSCLockedAmount != null)] | length' "$SKIP_STEP_F_CHECKS")"
[[ "$SKIPPED_ENTRIES" -eq "$N" ]]
check "step-f-checks.json records skippedSCLockedAmount for exactly $N token(s)" $?

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES check(s) failed. Workdir kept for inspection: $WORKDIR"
    exit 1
fi

rm -rf "$WORKDIR"
log_info "✅ skipSCLockedValue works: certificate omits the $N SC-locked exit(s), exitAddress is optional, and the Step F balance check still matches"
