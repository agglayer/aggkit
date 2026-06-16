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


