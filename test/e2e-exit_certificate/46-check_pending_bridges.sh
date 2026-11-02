#!/usr/bin/env bash
# Verifies the exit_certificate PENDING_CLAIMS step (issue #1928): the L2 -> L1 bridge that
# 20-prepare_network.sh emitted BEFORE the exit certificate (and that nobody claims on L1)
# is not part of the certificate's bridge_exits, but it must be listed by
# `exit_certificate --step pending_claims` in step-pending-l2-bridges.json.
#
# The step runs in isolation on top of the step-40 output (it reads the Step G local exit
# tree DB and the settled certificate data), so it must run after 40-generate_exit_certificate.sh.
# 60-claim_exit_certificate_funds.sh then checks that the claimer serves and claims that bridge.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_TOOL="${AGGKIT_DIR}/target/exit_certificate"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"
ERC20_STATE_FILE="${ERC20_STATE_FILE:-${AGGKIT_DIR}/tmp/exit_certificate-e2e-erc20.env}"
# Amount of the L2 -> L1 bridge emitted by 20-prepare_network.sh (bridge_l2_to_l1.sh --amount 1).
PENDING_BRIDGE_AMOUNT="${PENDING_BRIDGE_AMOUNT:-1}"

for f in "$EXIT_TOOL" "$EXIT_CERT_CONFIG" "$ERC20_STATE_FILE"; do
    if [[ ! -e "$f" ]]; then
        log_error "required file not found: $f"
        log_error "Run 20-prepare_network.sh and 40-generate_exit_certificate.sh first."
        exit 1
    fi
done

# The L2 -> L1 bridge of the prepare step is sent from the faucet account to itself.
# shellcheck source=/dev/null
source "$ERC20_STATE_FILE"
EXPECTED_DEST="$ERC20_HOLDER_ADDRESS"

# options.outputDir is relative to the config file dir.
OUTPUT_DIR="$(jq -r '.options.outputDir // "./output"' "$EXIT_CERT_CONFIG")"
[[ "$OUTPUT_DIR" = /* ]] || OUTPUT_DIR="$(dirname "$EXIT_CERT_CONFIG")/${OUTPUT_DIR}"
PENDING_FILE="${OUTPUT_DIR}/step-pending-l2-bridges.json"
CERTIFICATE="${OUTPUT_DIR}/exit-certificate-final.json"

rm -f "$PENDING_FILE"
log_info "🔎 Run the isolated PENDING_CLAIMS step"
run_quiet "$EXIT_TOOL" --config "$EXIT_CERT_CONFIG" --step pending_claims

if [[ ! -f "$PENDING_FILE" ]]; then
    log_error "PENDING_CLAIMS step did not generate $PENDING_FILE"
    exit 1
fi

log_info "🔎 Checking $PENDING_FILE"
failures=0
check() {
    if [[ "$1" == "1" ]]; then
        log_info "✅ $2"
    else
        log_error "❌ $2"
        failures=$((failures + 1))
    fi
}

count="$(jq 'length' "$PENDING_FILE")"
check "$([[ "$count" == "1" ]] && echo 1 || echo 0)" "exactly 1 pending bridge (found ${count})"

entry_ok() { jq -e "$1" "$PENDING_FILE" >/dev/null 2>&1 && echo 1 || echo 0; }
check "$(entry_ok '.[0].leafType == 0')" "pending bridge is an asset (leafType 0)"
check "$(entry_ok '.[0].destinationNetwork == 0')" "pending bridge goes to L1 (destinationNetwork 0)"
check "$(entry_ok ".[0].amount == \"${PENDING_BRIDGE_AMOUNT}\" or .[0].amount == ${PENDING_BRIDGE_AMOUNT}")" \
    "pending bridge amount is ${PENDING_BRIDGE_AMOUNT}"
check "$(entry_ok "(.[0].destinationAddress | ascii_downcase) == \"$(tr '[:upper:]' '[:lower:]' <<<"$EXPECTED_DEST")\"")" \
    "pending bridge destination is ${EXPECTED_DEST}"

# The pending bridge predates the certificate: its leaf must not be among the certificate's exits,
# so its deposit count has to be lower than every leaf the certificate appended.
if [[ -f "$CERTIFICATE" ]]; then
    exits="$(jq '.bridge_exits | length' "$CERTIFICATE")"
    pending_dc="$(jq '.[0].depositCount' "$PENDING_FILE")"
    check "$([[ "$pending_dc" == "0" ]] && echo 1 || echo 0)" \
        "pending bridge is the first leaf of the local exit tree (depositCount ${pending_dc}; certificate has ${exits} exits after it)"
fi

if [[ "$failures" -ne 0 ]]; then
    log_error "${failures} pending-bridge check(s) failed"
    exit 1
fi
log_info "✅ PENDING_CLAIMS step lists the unclaimed pre-certificate L2 -> L1 bridge"
