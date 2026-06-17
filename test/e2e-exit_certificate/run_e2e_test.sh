#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

SKIP_RUN_NETWORK=0

usage() {
    cat >&2 <<EOF
Usage: ${0##*/} [options]

Options:
  -s, --skip-run-network  Skip the "Run network" step (run_network.sh restart).
  -h, --help              Show this help message and exit.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -s|--skip-run-network)
            SKIP_RUN_NETWORK=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            log_error "Unknown argument: $1"
            usage
            exit 1
            ;;
    esac
done

if [[ "${SKIP_RUN_NETWORK}" == "1" ]]; then
    log_warn "⏭️  Skipping 'Run network' step"
else
    log_info "🛠️ Run network"
    run_quiet "${SCRIPT_DIR}/run_network.sh" restart
fi

log_info "🛠️ Prepare network for exit-certificate tests"
run_quiet "${SCRIPT_DIR}/prepare_network.sh"

log_info "🌇 sunset network"
run_quiet "${SCRIPT_DIR}/run_exit_tool.sh"

log_info "Stop sequencer"
run_quiet "${SCRIPT_DIR}/stop_sequencer.sh"

log_info "Claim funds"
run_quiet "${SCRIPT_DIR}/claim_exit_certificate_funds.sh"