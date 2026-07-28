#!/usr/bin/env bash
# Verifies the exit certificate is reproducible (AET determinism): re-runs the full pipeline
# E2E_DETERMINISM_RUNS times (default 2) from the same config used by 40-generate_exit_certificate.sh
# — via tools/exit_certificate/scripts/run_and_verify_determinism.sh, which pins the L2 target block
# and the L1 cutoff so every run sees the same on-chain snapshot — and checks all runs produce
# byte-identical certificates. It then cross-checks the reruns against the step-40 certificate:
# same bridge_exits (content and order) and same new_local_exit_root. The reruns write to a scratch
# workdir, so the step-40 output consumed by the later submit/claim steps is never touched.
#
# The comparison against the step-40 certificate deliberately skips L1-dependent fields
# (l1_info_tree_leaf_count): L1 keeps producing blocks between step 40 and this step, while the L2
# is frozen (sequencer stopped in 30-stop_sequencer.sh).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"
E2E_DETERMINISM_RUNS="${E2E_DETERMINISM_RUNS:-2}"
DETERMINISM_SCRIPT="${AGGKIT_DIR}/tools/exit_certificate/scripts/run_and_verify_determinism.sh"

command -v jq >/dev/null || { log_error "jq is required"; exit 1; }
if [[ ! -f "$EXIT_CERT_CONFIG" ]]; then
    log_error "exit_certificate config not found: $EXIT_CERT_CONFIG"
    log_error "Run 40-generate_exit_certificate.sh first."
    exit 1
fi

# Resolve the step-40 certificate: options.outputDir is relative to the config file dir.
OUTPUT_DIR="$(jq -r '.options.outputDir // "./output"' "$EXIT_CERT_CONFIG")"
[[ "$OUTPUT_DIR" == /* ]] || OUTPUT_DIR="$(cd "$(dirname "$EXIT_CERT_CONFIG")" && pwd)/${OUTPUT_DIR}"
STEP40_CERT="${OUTPUT_DIR}/exit-certificate-final.json"
if [[ ! -f "$STEP40_CERT" ]]; then
    log_error "Final certificate not found: $STEP40_CERT"
    log_error "Run 40-generate_exit_certificate.sh first."
    exit 1
fi

BINARY="${AGGKIT_DIR}/target/exit_certificate"
BINARY_ARGS=()
[[ -x "$BINARY" ]] && BINARY_ARGS=(--binary "$BINARY") # already built by step 40; else the script builds it

WORKDIR="$(mktemp -d /tmp/e2e-exit-cert-determinism.XXXXXX)"

log_info "🔁 Re-running the pipeline ${E2E_DETERMINISM_RUNS} times to verify determinism"
if ! "$DETERMINISM_SCRIPT" --config "$EXIT_CERT_CONFIG" --runs "$E2E_DETERMINISM_RUNS" \
        --workdir "$WORKDIR" --keep "${BINARY_ARGS[@]}"; then
    log_error "Determinism verification failed (workdir kept: $WORKDIR)"
    exit 1
fi

log_info "🔎 Cross-checking the reruns against the step-40 certificate: $STEP40_CERT"
RERUN_CERT="${WORKDIR}/run-1/exit-certificate-final.json"

FAILURES=0
if diff <(jq -S '.bridge_exits' "$STEP40_CERT") <(jq -S '.bridge_exits' "$RERUN_CERT") >/dev/null; then
    log_info "  ✅ bridge_exits identical (content and order) to the step-40 certificate"
else
    log_error "  ❌ bridge_exits differ from the step-40 certificate"
    diff <(jq -S '.bridge_exits' "$STEP40_CERT") <(jq -S '.bridge_exits' "$RERUN_CERT") >&2 || true
    FAILURES=$((FAILURES + 1))
fi

STEP40_LER="$(jq -r '.new_local_exit_root' "$STEP40_CERT")"
RERUN_LER="$(jq -r '.new_local_exit_root' "$RERUN_CERT")"
if [[ "${STEP40_LER,,}" == "${RERUN_LER,,}" ]]; then
    log_info "  ✅ new_local_exit_root identical to the step-40 certificate ($STEP40_LER)"
else
    log_error "  ❌ new_local_exit_root differs: step-40 $STEP40_LER vs rerun $RERUN_LER"
    FAILURES=$((FAILURES + 1))
fi

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES cross-check(s) failed. Workdir kept for inspection: $WORKDIR"
    exit 1
fi

rm -rf "$WORKDIR"
log_info "✅ Certificate is deterministic across ${E2E_DETERMINISM_RUNS} runs and matches the step-40 certificate"
