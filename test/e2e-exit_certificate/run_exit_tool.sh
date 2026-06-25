#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="${SCRIPT_DIR}/../../"
EXIT_CETIFICATE_SCRIPT_DIR="${SCRIPT_DIR}/../../tools/exit_certificate/scripts"


log_info "🛠️ Build exit_tool"
pushd "${AGGKIT_DIR}" >/dev/null
run_quiet make build-exit_certificate
popd >/dev/null

log_info "📖 Create configuration"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/configuration_based_on_kurtosis.sh"

log_info "🌇 Execute tool to sunset kurtosis network"
run_quiet "${AGGKIT_DIR}/target/exit_certificate" --config tmp/exit_certificate-kurtosis.json

# runAll stops at SIGN; SUBMIT and WAIT must be requested explicitly. Submit the
# signed exit certificate to agglayer and wait for it to settle on L1 — this is
# what produces step-submit-result.json and step-wait-result.json, the L1
# settlement record the claimer anchors the claim to. Done here, while agglayer
# and L1 are fully operational, before the L2 network is stopped.
log_info "📤 Submit exit certificate to agglayer and wait for L1 settlement"
run_quiet "${AGGKIT_DIR}/target/exit_certificate" --config tmp/exit_certificate-kurtosis.json --step submit,wait


