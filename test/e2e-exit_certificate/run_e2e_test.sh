#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

SKIP_RUN_NETWORK=0

usage() {
    cat >&2 <<EOF
Usage: ${0##*/} [options]

Options:
  -s, --skip-run-network  Skip the "Run network" step (10-run_network.sh restart).
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
    run_quiet "${SCRIPT_DIR}/10-run_network.sh" restart
fi

log_info "🛠️ Prepare network for exit-certificate tests"
run_quiet "${SCRIPT_DIR}/20-prepare_network.sh"

# Stop only the block-producing services (op-batcher, op-cl/op-node): the chain
# freezes at its current head, but the op-el execution clients keep serving RPC —
# the exit tool still needs to read the L2 state (steps 0/A/B and the Step G2
# Anvil shadow-fork) to generate the certificate.
log_info "Stop sequencer"
run_quiet env L2_SERVICE_PATTERN='^op-(batcher|cl).*-001$' "${SCRIPT_DIR}/30-stop_sequencer.sh"

log_info "🌇 Generate exit certificate to sunset network"
run_quiet "${SCRIPT_DIR}/40-generate_exit_certificate.sh"

log_info "🔁 Check certificate determinism (repeated runs)"
run_quiet "${SCRIPT_DIR}/42-check_deterministic_certificate.sh"

log_info "🔎 Check exit certificate"
run_quiet "${SCRIPT_DIR}/45-check_exit_certificate.sh"

log_info "📤 Submit exit certificate"
run_quiet "${SCRIPT_DIR}/50-submit_exit_certificate.sh"

log_info "Claim funds"
run_quiet "${SCRIPT_DIR}/60-claim_exit_certificate_funds.sh"