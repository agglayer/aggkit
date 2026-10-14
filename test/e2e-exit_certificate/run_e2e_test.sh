#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

SKIP_RUN_NETWORK=0
DELTA_MODE=0

usage() {
    cat >&2 <<EOF
Usage: ${0##*/} [options]

Options:
  -s, --skip-run-network  Skip the "Run network" step (10-run_network.sh restart).
  -d, --delta             Delta scenario: the first certificate is generated with skipSCLockedValue
                          (ETH locked in a smart contract is left behind), then two delta
                          certificates recover it chained on top, and every exit — old and new — is
                          claimed against the latest certificate.
  -h, --help              Show this help message and exit.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        -s|--skip-run-network)
            SKIP_RUN_NETWORK=1
            shift
            ;;
        -d|--delta)
            DELTA_MODE=1
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

if [[ "${DELTA_MODE}" == "1" ]]; then
    # 20-prepare_network.sh locks ETH in a contract; 40-generate_exit_certificate.sh then leaves it
    # out of the first certificate (options.skipSCLockedValue) so deltas can recover it.
    export DELTA_SCENARIO=1
    export SKIP_SC_LOCKED_VALUE=1
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

# Both checks assume the regular (non-skipSCLockedValue) first certificate: they run in the default scenario.
if [[ "${DELTA_MODE}" != "1" ]]; then
    log_info "🔁 Check certificate determinism (repeated runs)"
    run_quiet "${SCRIPT_DIR}/42-check_deterministic_certificate.sh"

    log_info "🙈 Check skipSCLockedValue (SC-locked funds left behind)"
    run_quiet "${SCRIPT_DIR}/43-check_skip_sclocked.sh"
fi

log_info "🔎 Check exit certificate"
run_quiet "${SCRIPT_DIR}/45-check_exit_certificate.sh"

log_info "📤 Submit exit certificate"
run_quiet "${SCRIPT_DIR}/50-submit_exit_certificate.sh"

if [[ "${DELTA_MODE}" == "1" ]]; then
    log_info "🧩 Delta 1: recover half of the locked ETH on top of the first certificate"
    run_quiet "${SCRIPT_DIR}/52-generate_delta_certificate.sh" 1

    log_info "🧩 Delta 2: chain a second delta on the first one"
    run_quiet "${SCRIPT_DIR}/52-generate_delta_certificate.sh" 2

    # The claimer serves the full certificate of the last delta (every exit, latest root). The delta
    # output has no EOA list: claim for the EOAs of the first run (old exits) plus the exitAddress (new).
    AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
    FIRST_OUTPUT_REL="$(jq -r '.options.outputDir' "${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json")"
    log_info "Claim funds (old and new exits, against the latest certificate)"
    EXIT_CERT_CONFIG="${AGGKIT_DIR}/tmp/exit_certificate-kurtosis-delta-2.json" \
    EOA_FILE="${AGGKIT_DIR}/tmp/${FIRST_OUTPUT_REL}/step-b-eoa-balances.json" \
        run_quiet "${SCRIPT_DIR}/60-claim_exit_certificate_funds.sh"
else
    log_info "Claim funds"
    run_quiet "${SCRIPT_DIR}/60-claim_exit_certificate_funds.sh"
fi