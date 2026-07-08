#!/usr/bin/env bash
# Submits the signed exit certificate to agglayer and waits for L1 settlement.
#
# runAll (40-generate_exit_certificate.sh) stops at SIGN; SUBMIT and WAIT must be
# requested explicitly. This produces step-submit-result.json and
# step-wait-result.json, the L1 settlement record the claimer anchors the claim
# to. Run it while agglayer, L1 and the L2 RPC are still operational, before the
# enclave is torn down.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"

log_info "📤 Submit exit certificate to agglayer and wait for L1 settlement"
run_quiet "${AGGKIT_DIR}/target/exit_certificate" --config tmp/exit_certificate-kurtosis.json --step submit,wait
