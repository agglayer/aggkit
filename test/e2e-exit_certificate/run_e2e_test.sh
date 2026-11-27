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
                          (ETH locked in two smart contracts is left behind), then two delta
                          certificates chained on top recover one contract each (two verified
                          recoveries applied with --step recoveries), and a third delta recovers
                          Circle's USDC.e with bridge Message exits (a kit case in the recovery.json
                          v1 format). Every exit — old and new — is claimed against the latest
                          certificate. Each settled run is archived (--step upload) in a GCS
                          emulator, each delta reads its previous run from that archive, and the
                          claimer serves it from there in follow mode.
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
    # 20-prepare_network.sh locks ETH in two contracts; 40-generate_exit_certificate.sh then leaves it
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

log_info "⏳ Check pending L2 -> L1 bridges emitted before the certificate (PENDING_CLAIMS step)"
run_quiet "${SCRIPT_DIR}/46-check_pending_bridges.sh"

if [[ "${DELTA_MODE}" == "1" ]]; then
    log_info "🙅 Check Circle USDC.e is reported and not paid by the first certificate"
    run_quiet "${SCRIPT_DIR}/47-check_circle_usdc_not_paid.sh"
fi

log_info "📤 Submit exit certificate"
run_quiet "${SCRIPT_DIR}/50-submit_exit_certificate.sh"

if [[ "${DELTA_MODE}" == "1" ]]; then
    # Every settled run is archived in a GCS emulator and the next delta reads it back from there.
    AGGKIT_TMP="$(cd "${SCRIPT_DIR}/../.." && pwd)/tmp"
    export ARCHIVE_URI_BASE="gs://let-archive/runs-$(date +%s)"
    export STORAGE_EMULATOR_HOST="127.0.0.1:${ARCHIVE_EMULATOR_PORT:-14443}"
    trap '"${SCRIPT_DIR}/51-archive_run.sh" stop' EXIT

    log_info "📦 Archive the first run"
    run_quiet "${SCRIPT_DIR}/51-archive_run.sh" "${AGGKIT_TMP}/exit_certificate-kurtosis.json" "${ARCHIVE_URI_BASE}"

    log_info "🧩 Delta 1: recovery of the first locked contract, on top of the first certificate"
    run_quiet "${SCRIPT_DIR}/52-generate_delta_certificate.sh" 1

    log_info "📦 Archive delta 1"
    run_quiet "${SCRIPT_DIR}/51-archive_run.sh" "${AGGKIT_TMP}/exit_certificate-kurtosis-delta-1.json" \
        "${ARCHIVE_URI_BASE}"

    log_info "🧩 Delta 2: recovery of the second locked contract, chained on delta 1"
    run_quiet "${SCRIPT_DIR}/52-generate_delta_certificate.sh" 2

    log_info "📦 Archive delta 2"
    run_quiet "${SCRIPT_DIR}/51-archive_run.sh" "${AGGKIT_TMP}/exit_certificate-kurtosis-delta-2.json" \
        "${ARCHIVE_URI_BASE}"

    # The claimer starts on delta 2, served from the archive, in follow mode: when delta 3 is archived it
    # must move to it by itself. It is stopped when the script ends.
    AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
    FIRST_OUTPUT_REL="$(jq -r '.options.outputDir' "${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json")"
    FIRST_EOA_FILE="${AGGKIT_DIR}/tmp/${FIRST_OUTPUT_REL}/step-b-eoa-balances.json"
    DELTA_3_CONFIG="${AGGKIT_TMP}/exit_certificate-kurtosis-delta-3.json"
    trap '"${SCRIPT_DIR}/61-claimer_follow.sh" stop; "${SCRIPT_DIR}/51-archive_run.sh" stop' EXIT
    log_info "🛰️ Start the claimer on delta 2, from the archive, in follow mode"
    run_quiet "${SCRIPT_DIR}/61-claimer_follow.sh" start "${AGGKIT_TMP}/exit_certificate-kurtosis-delta-2.json"
    run_quiet "${SCRIPT_DIR}/62-check_circle_usdc_claims.sh" not-yet

    log_info "🧩 Delta 3: Circle USDC.e recovered with Message exits (kit case in the recovery.json v1 format), chained on delta 2"
    run_quiet "${SCRIPT_DIR}/53-generate_circle_usdc_delta.sh"

    log_info "📦 Archive delta 3"
    run_quiet "${SCRIPT_DIR}/51-archive_run.sh" "${DELTA_3_CONFIG}" "${ARCHIVE_URI_BASE}"

    log_info "🛰️ The running claimer picks up delta 3 from the archive"
    DELTA_3_OUTPUT_REL="$(jq -r '.options.outputDir' "${DELTA_3_CONFIG}")"
    run_quiet "${SCRIPT_DIR}/61-claimer_follow.sh" wait-height \
        "$(jq -r '.height' "${AGGKIT_TMP}/${DELTA_3_OUTPUT_REL}/exit-certificate-full.json")"
    EXIT_CERT_CONFIG="${DELTA_3_CONFIG}" EOA_FILE="${FIRST_EOA_FILE}" \
        run_quiet "${SCRIPT_DIR}/62-check_circle_usdc_claims.sh" listed

    # The claimer now serves the full certificate of the last delta (every exit, latest root). The delta
    # output has no EOA list: claim for the holders of delta 3 (each Message exit through its beneficiary)
    # and the EOAs of the first run (old exits); the exits of deltas 1 and 2 are claimed because
    # claim-all.sh also claims every L1 destination of exit-certificate-full.json.
    log_info "Claim funds (old and new exits, against the latest certificate)"
    CLAIMER_ALREADY_RUNNING=1 \
    EXIT_CERT_CONFIG="${DELTA_3_CONFIG}" \
    EOA_FILE="${AGGKIT_TMP}/exit_certificate-e2e-claim-addresses.json" \
        run_quiet "${SCRIPT_DIR}/60-claim_exit_certificate_funds.sh"

    log_info "💵 Check the Circle USDC claims on L1 (claimMessage → L1Escrow pays the holders)"
    EXIT_CERT_CONFIG="${DELTA_3_CONFIG}" run_quiet "${SCRIPT_DIR}/62-check_circle_usdc_claims.sh" claimed

    # The owners are fresh L1 addresses: once claimed, each must hold exactly its recovered amount.
    log_info "💰 Check the L1 balance of each recovery's owner"
    # shellcheck source=/dev/null
    source "${AGGKIT_DIR}/tmp/exit_certificate-e2e-delta.env"
    source <("${AGGKIT_DIR}/tools/exit_certificate/scripts/export_kurtosis_env.sh" 1)
    for i in 1 2; do
        owner_var="DELTA_OWNER_ADDRESS_${i}"
        amount_var="DELTA_SC_LOCKED_AMOUNT_${i}"
        balance="$(cast balance --rpc-url "$L1_RPC_URL" "${!owner_var}")"
        if [[ "$balance" != "${!amount_var}" ]]; then
            log_error "owner ${i} (${!owner_var}) holds ${balance} wei on L1, expected ${!amount_var}"
            exit 1
        fi
        log_info "  ✅ owner ${i} (${!owner_var}) holds ${balance} wei on L1"
    done
else
    log_info "Claim funds"
    run_quiet "${SCRIPT_DIR}/60-claim_exit_certificate_funds.sh"
fi