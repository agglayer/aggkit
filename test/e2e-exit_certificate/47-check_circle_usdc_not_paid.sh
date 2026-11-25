#!/usr/bin/env bash
# Delta scenario: checks what the FIRST exit certificate does with Circle's USDC.e, the token
# 21-prepare_circle_usdc.sh deployed. USDC.e is native to the L2 (minted by ZkMinterBurner on bridge
# messages, not wrapped by the bridge), so it is not in the LBT: the tool reports it as an ERC-20 contract it
# found (Step B2) and pays nothing for it. That is the gap delta 3 closes.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"
CIRCLE_STATE_FILE="${CIRCLE_STATE_FILE:-${AGGKIT_DIR}/tmp/exit_certificate-e2e-circle-usdc.env}"
for f in "$EXIT_CERT_CONFIG" "$CIRCLE_STATE_FILE"; do
    [[ -f "$f" ]] || { log_error "required file not found: $f"; exit 1; }
done
# shellcheck source=/dev/null
source "$CIRCLE_STATE_FILE"

OUTPUT_DIR="$(jq -r '.options.outputDir // "./output"' "$EXIT_CERT_CONFIG")"
[[ "$OUTPUT_DIR" == /* ]] || OUTPUT_DIR="$(cd "$(dirname "$EXIT_CERT_CONFIG")" && pwd)/${OUTPUT_DIR}"
CERT="${OUTPUT_DIR}/exit-certificate-final.json"
LBT="${OUTPUT_DIR}/step-0-lbt.json"
DETECTED="${OUTPUT_DIR}/step-b2-detected-erc20s.json"
DISCARDED="${OUTPUT_DIR}/step-b2-discarded-erc20s.json"
for f in "$CERT" "$LBT" "$DETECTED" "$DISCARDED"; do
    [[ -f "$f" ]] || { log_error "exit_certificate output not found: $f"; exit 1; }
done

FAILURES=0
check() {
    local desc="$1"
    shift
    if "$@"; then
        log_info "  ✅ $desc"
    else
        log_error "  ❌ $desc"
        FAILURES=$((FAILURES + 1))
    fi
}

log_info "🔎 Checking the first certificate: USDC.e (${CIRCLE_USDCE}) is reported, not paid"
check "Step B2 reports USDC.e as an ERC-20 contract, with its total supply (${CIRCLE_TOTAL_DEPOSITED})" \
    jq -e -s --arg t "${CIRCLE_USDCE,,}" --arg s "$CIRCLE_TOTAL_DEPOSITED" \
        'add | any((.address | ascii_downcase) == $t and .totalSupply == $s)' "$DETECTED" "$DISCARDED" >/dev/null
# The L2 address of USDC.e can coincide with the L1 address of USDC (same deployer, same nonce): what
# identifies USDC.e is its origin, the L2 network.
check "USDC.e is not in the LBT (no token of origin network ${CIRCLE_L2_NETWORK_ID})" \
    jq -e --argjson n "$CIRCLE_L2_NETWORK_ID" 'all(.originNetwork != $n)' "$LBT" >/dev/null
check "no exit of the certificate pays a token of the L2 network, nor is a Message" \
    jq -e --argjson n "$CIRCLE_L2_NETWORK_ID" \
        '.bridge_exits | all(.token_info.origin_network != $n and (.leaf_type == "Transfer" or .leaf_type == 0))' "$CERT" >/dev/null
for holder in "$CIRCLE_HOLDER_1" "$CIRCLE_HOLDER_2" "$CIRCLE_HOLDER_3"; do
    check "USDC.e holder ${holder} (it holds nothing else) gets no exit" \
        jq -e --arg h "${holder,,}" '.bridge_exits | all((.dest_address | ascii_downcase) != $h)' "$CERT" >/dev/null
done
check "the only L1 USDC paid is the bridge-wrapped USDC (${CIRCLE_BW_USDC_AMOUNT}), a regular LBT token" \
    jq -e --arg t "${CIRCLE_L1_USDC,,}" --arg a "$CIRCLE_BW_USDC_AMOUNT" \
        '[.bridge_exits[] | select(.token_info.origin_network == 0 and (.token_info.origin_token_address | ascii_downcase) == $t)]
         | length == 1 and .[0].amount == $a' "$CERT" >/dev/null

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES check(s) failed"
    exit 1
fi
log_info "✅ The first certificate reports USDC.e and pays none of its ${CIRCLE_TOTAL_DEPOSITED} base units"
