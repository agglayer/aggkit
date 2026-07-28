#!/usr/bin/env bash
# Builds the exit_certificate tool, derives its configuration from the running
# Kurtosis enclave and runs the full pipeline (CHECK → … → SIGN), producing
# exit-certificate-final.json / exit-certificate-signed.json in the output dir.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CETIFICATE_SCRIPT_DIR="${AGGKIT_DIR}/tools/exit_certificate/scripts"

log_info "🛠️ Build exit_tool"
pushd "${AGGKIT_DIR}" >/dev/null
run_quiet make build-exit_certificate
popd >/dev/null

log_info "📖 Create configuration"
run_quiet "${EXIT_CETIFICATE_SCRIPT_DIR}/configuration_based_on_kurtosis.sh"

log_info "🌇 Execute tool to sunset kurtosis network"
run_quiet "${AGGKIT_DIR}/target/exit_certificate" --config tmp/exit_certificate-kurtosis.json
