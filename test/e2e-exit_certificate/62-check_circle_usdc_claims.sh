#!/usr/bin/env bash
# Checks of the Circle USDC recovery (delta 3) against the claimer API v2 and L1, around the claims.
#
#   62-check_circle_usdc_claims.sh not-yet   the claimer still serves delta 2: no holder has a Message exit
#   62-check_circle_usdc_claims.sh listed    the claimer serves delta 3: every holder finds its exit by its own
#                                            address (the beneficiary decoded from the metadata), with
#                                            claim_method claimMessage; API v1 never returns it; nothing is
#                                            claimed yet. Writes the address list claim-all.sh claims for
#                                            (CIRCLE_CLAIM_ADDRESSES_FILE): the holders first, so each
#                                            message is claimed through its beneficiary.
#   62-check_circle_usdc_claims.sh claimed   after claim-all.sh: each holder holds exactly its amount of L1
#                                            USDC, L1Escrow dropped by the total, every leaf is claimed, and
#                                            a second claim reverts.
#
# Env: CLAIMER_URL (default http://127.0.0.1:7080), EXIT_CERT_CONFIG (L1 RPC, L1 bridge), EOA_FILE (the EOAs of
# the first run, for `listed`).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TMP_DIR="${AGGKIT_DIR}/tmp"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${TMP_DIR}/exit_certificate-kurtosis.json}"
CIRCLE_STATE_FILE="${CIRCLE_STATE_FILE:-${TMP_DIR}/exit_certificate-e2e-circle-usdc.env}"
CIRCLE_CLAIM_ADDRESSES_FILE="${CIRCLE_CLAIM_ADDRESSES_FILE:-${TMP_DIR}/exit_certificate-e2e-claim-addresses.json}"
CIRCLE_DEPOSITS_FILE="${TMP_DIR}/exit_certificate-e2e-circle-usdc-deposits.json"
CLAIMER_URL="${CLAIMER_URL:-http://127.0.0.1:${CLAIMER_PORT:-7080}}"
CLAIM_ASSET="${AGGKIT_DIR}/tools/exit_certificate_claimer/scripts/claim-asset.sh"
MODE="${1:-}"

for f in "$EXIT_CERT_CONFIG" "$CIRCLE_STATE_FILE"; do
    [[ -f "$f" ]] || { log_error "required file not found: $f"; exit 1; }
done
# shellcheck source=/dev/null
source "$CIRCLE_STATE_FILE"
L1_RPC_URL="$(jq -r '.l1RpcUrl' "$EXIT_CERT_CONFIG")"
L1_BRIDGE="$(jq -r '.l1BridgeAddress' "$EXIT_CERT_CONFIG")"
HOLDERS=("$CIRCLE_HOLDER_1" "$CIRCLE_HOLDER_2" "$CIRCLE_HOLDER_3")
AMOUNTS=("$CIRCLE_HOLDER_AMOUNT_1" "$CIRCLE_HOLDER_AMOUNT_2" "$CIRCLE_HOLDER_AMOUNT_3")

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
finish() {
    if [[ "$FAILURES" -ne 0 ]]; then
        log_error "$FAILURES Circle USDC check(s) failed ($MODE)"
        exit 1
    fi
}
api() { curl -sf --get "${CLAIMER_URL}/claimer/$1" --data-urlencode "$2"; }
l1_usdc() {
    local out
    out="$(cast call --rpc-url "$L1_RPC_URL" "$CIRCLE_L1_USDC" "balanceOf(address)(uint256)" "$1")"
    printf '%s' "${out%% *}"
}

case "$MODE" in
not-yet)
    log_info "🔎 Before delta 3: the claimer has no Message exit for the USDC.e holders"
    for holder in "${HOLDERS[@]}"; do
        check "no exit listed for holder ${holder}" \
            bash -c 'jq -e ".api_version == 2 and (.bridges | length == 0)" <<<"$1" >/dev/null' _ "$(api v2/bridges "address=${holder}")"
    done
    check "no Message exit to L1Escrow ${CIRCLE_L1_ESCROW}" \
        bash -c 'jq -e ".bridges | length == 0" <<<"$1" >/dev/null' _ "$(api v2/bridges "address=${CIRCLE_L1_ESCROW}")"
    finish
    ;;
listed)
    log_info "🔎 Delta 3 served: every holder finds its Message exit through the claimer API v2"
    deposits="[]"
    for i in "${!HOLDERS[@]}"; do
        holder="${HOLDERS[$i]}"
        amount="${AMOUNTS[$i]}"
        bridges="$(api v2/bridges "address=${holder}")"
        check "holder ${holder}: one exit, claim_method claimMessage, beneficiary = the holder, decoded_amount ${amount}, display_token USDC (6 decimals) = L1 USDC" \
            bash -c 'jq -e --arg h "${2,,}" --arg a "$3" --arg t "${4,,}" --arg e "${5,,}" --arg o "${6,,}" --argjson n "$7" "
                (.bridges | length == 1) and (.bridges[0] |
                    .claim_method == \"claimMessage\" and .leaf_type == 1 and .amount == \"0\"
                    and (.beneficiary | ascii_downcase) == \$h and .decoded_amount == \$a
                    and (.display_token.address | ascii_downcase) == \$t and .display_token.symbol == \"USDC\"
                    and .display_token.decimals == 6
                    and (.destination_address | ascii_downcase) == \$e and .destination_network == 0
                    and (.origin_token_address | ascii_downcase) == \$o and .origin_network == \$n)" <<<"$1" >/dev/null' \
                _ "$bridges" "$holder" "$amount" "$CIRCLE_L1_USDC" "$CIRCLE_L1_ESCROW" "$CIRCLE_ZK_MINTER_BURNER" "$CIRCLE_L2_NETWORK_ID"
        dc="$(jq -r '.bridges[0].deposit_count // empty' <<<"$bridges")"
        [[ "$dc" =~ ^[0-9]+$ ]] || { log_error "holder ${holder}: no deposit_count in ${bridges}"; exit 1; }
        params="$(api v2/claim-params "address=${holder}")"
        expected_md="$(cast abi-encode "f(address,uint256)" "$holder" "$amount")"
        expected_md_hash="$(cast keccak "$expected_md")"
        # API v2 always returns the raw leaf metadata, plus its keccak256 in metadata_hash.
        check "holder ${holder}: /bridges returns the raw metadata abi.encode(holder, amount) and metadata_hash = its keccak256" \
            bash -c 'jq -e --arg md "${2,,}" --arg h "${3,,}" "
                .bridges[0] | (.metadata | ascii_downcase) == \$md and (.metadata_hash | ascii_downcase) == \$h" <<<"$1" >/dev/null' \
                _ "$bridges" "$expected_md" "$expected_md_hash"
        check "holder ${holder}: claim-params returns claimMessage for deposit ${dc} with the raw metadata abi.encode(holder, amount) and its metadata_hash" \
            bash -c 'jq -e --argjson dc "$2" --arg md "${3,,}" --arg h "${4,,}" "
                (.claims | length == 1) and (.claims[0] | .claim_method == \"claimMessage\" and .deposit_count == \$dc
                    and (.metadata | ascii_downcase) == \$md and (.metadata_hash | ascii_downcase) == \$h)" <<<"$1" >/dev/null' \
                _ "$params" "$dc" "$expected_md" "$expected_md_hash"
        check "holder ${holder}: API v1 does not return the Message exit (v1 clients can only claimAsset)" \
            bash -c 'jq -e ".bridges | length == 0" <<<"$1" >/dev/null' _ "$(api v1/bridges "dest_address=${holder}")"
        check "deposit ${dc} is not claimed on L1 yet" \
            test "$(cast call --rpc-url "$L1_RPC_URL" "$L1_BRIDGE" "isClaimed(uint32,uint32)(bool)" "$dc" "$CIRCLE_L2_NETWORK_ID")" = "false"
        check "holder ${holder} holds no L1 USDC yet" test "$(l1_usdc "$holder")" = "0"
        deposits="$(jq -c --arg h "$holder" --argjson dc "$dc" '. + [{holder: $h, deposit_count: $dc}]' <<<"$deposits")"
    done
    check "the ${#HOLDERS[@]} exits have distinct, consecutive deposit counts in payout order" \
        jq -e '[.[].deposit_count] as $d | ($d | length) > 0 and ($d == [range($d[0]; $d[0] + ($d | length))])' <<<"$deposits" >/dev/null
    escrow_bridges="$(api v2/bridges "address=${CIRCLE_L1_ESCROW}")"
    check "the same ${#HOLDERS[@]} exits are listed for their destination contract (L1Escrow)" \
        test "$(jq -c '[.bridges[].deposit_count] | sort' <<<"$escrow_bridges")" = "$(jq -c '[.[].deposit_count] | sort' <<<"$deposits")"
    check "API v1 does not list them for L1Escrow either" \
        bash -c 'jq -e ".bridges | all(.leaf_type == 0)" <<<"$1" >/dev/null' _ "$(api v1/bridges "dest_address=${CIRCLE_L1_ESCROW}")"
    check "the contract holder ${CIRCLE_CONTRACT_HOLDER} was left out of the payout: no exit" \
        bash -c 'jq -e ".bridges | length == 0" <<<"$1" >/dev/null' _ "$(api v2/bridges "address=${CIRCLE_CONTRACT_HOLDER}")"
    check "L1Escrow still holds the ${CIRCLE_TOTAL_DEPOSITED} L1 USDC" test "$(l1_usdc "$CIRCLE_L1_ESCROW")" = "$CIRCLE_TOTAL_DEPOSITED"
    echo "$deposits" > "$CIRCLE_DEPOSITS_FILE"

    # claim-all.sh claims for these addresses in order: the holders first (each message is then claimed
    # through its beneficiary; when L1Escrow comes up later the deposits are already handled), then the EOAs
    # of the first run.
    eoa_file="${EOA_FILE:-}"
    [[ -f "$eoa_file" ]] || { log_error "EOA_FILE not found: ${eoa_file}"; exit 1; }
    jq -n --argjson holders "$(printf '%s\n' "${HOLDERS[@]}" | jq -R '{address: .}' | jq -sc .)" --slurpfile eoas "$eoa_file" \
        '$holders + [$eoas[0][] | if type == "object" then {address} else {address: .} end]' > "$CIRCLE_CLAIM_ADDRESSES_FILE"
    finish
    ;;
claimed)
    log_info "🔎 After the claims: L1Escrow paid every holder"
    [[ -f "$CIRCLE_DEPOSITS_FILE" ]] || { log_error "$CIRCLE_DEPOSITS_FILE not found (run '${0##*/} listed' first)"; exit 1; }
    for i in "${!HOLDERS[@]}"; do
        holder="${HOLDERS[$i]}"
        dc="$(jq -r --arg h "$holder" '.[] | select(.holder == $h) | .deposit_count' "$CIRCLE_DEPOSITS_FILE")"
        check "holder ${holder} holds exactly ${AMOUNTS[$i]} L1 USDC" test "$(l1_usdc "$holder")" = "${AMOUNTS[$i]}"
        check "deposit ${dc} is claimed on L1" \
            test "$(cast call --rpc-url "$L1_RPC_URL" "$L1_BRIDGE" "isClaimed(uint32,uint32)(bool)" "$dc" "$CIRCLE_L2_NETWORK_ID")" = "true"
    done
    remaining="$(echo "$CIRCLE_TOTAL_DEPOSITED - $CIRCLE_PAYOUT_TOTAL" | bc)"
    check "L1Escrow dropped by exactly the total paid (${CIRCLE_PAYOUT_TOTAL}): it holds ${remaining}, the contract holder's share" \
        test "$(l1_usdc "$CIRCLE_L1_ESCROW")" = "$remaining"
    check "the contract holder received nothing on L1" test "$(l1_usdc "$CIRCLE_CONTRACT_HOLDER")" = "0"

    # A second claim of the same leaf must revert, for every holder, and move nothing.
    claimer_key="$(cat "${TMP_DIR}/exit_address_privatekey.txt" 2>/dev/null || true)"
    claimer_key="${claimer_key:-0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59}"
    for i in "${!HOLDERS[@]}"; do
        holder="${HOLDERS[$i]}"
        dc="$(jq -r --arg h "$holder" '.[] | select(.holder == $h) | .deposit_count' "$CIRCLE_DEPOSITS_FILE")"
        log="$(mktemp)"
        if CLAIMER_URL="$CLAIMER_URL" L1_RPC_URL="$L1_RPC_URL" BRIDGE_ADDRESS="$L1_BRIDGE" PRIVATE_KEY="$claimer_key" \
            ASSUME_YES=1 "$CLAIM_ASSET" "$holder" "$dc" >"$log" 2>&1; then rc=0; else rc=$?; fi
        check "a second claimMessage of deposit ${dc} (holder ${holder}) reverts" test "$rc" -ne 0
        if [[ "$rc" -eq 0 ]]; then tail -n 20 "$log" >&2; fi
        rm -f "$log"
        check "... and pays nothing: the holder still holds ${AMOUNTS[$i]}" test "$(l1_usdc "$holder")" = "${AMOUNTS[$i]}"
    done
    check "L1Escrow still holds ${remaining} after the repeated claims" test "$(l1_usdc "$CIRCLE_L1_ESCROW")" = "$remaining"
    finish
    log_info "✅ Circle USDC: ${#HOLDERS[@]} Message exits claimed with claimMessage, L1Escrow paid ${CIRCLE_PAYOUT_TOTAL} L1 USDC"
    ;;
*)
    log_error "usage: ${0##*/} not-yet | listed | claimed"
    exit 1
    ;;
esac
