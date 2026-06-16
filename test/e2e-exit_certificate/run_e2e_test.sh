#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

log_info "🛠️ Run network"
run_quiet "${SCRIPT_DIR}/run_network.sh" restart

log_info "🛠️ Prepare network for exit-certificate tests"
run_quiet "${SCRIPT_DIR}/prepare_network.sh"

log_info "🌇 sunset network"
run_quiet "${SCRIPT_DIR}/run_exit_tool.sh"