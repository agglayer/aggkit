#!/usr/bin/env bash
#
# claim-all.sh — claim every pending bridge exit for all addresses tracked by an
# exit_certificate run, by talking to the running exit_certificate_claimer service.
#
# For each address it enumerates the address's deposits via GET /bridges and submits
# AgglayerBridge.claimAsset for each one through the sibling claim-asset.sh.
#
# The set of addresses is:
#   - every EOA listed in <outputDir>/step-b-eoa-balances.json, and
#   - the config's exitAddress (where the smart-contract-locked funds are sent).
#
# Usage:
#   ./claim-all.sh [config_file]
#
# Arguments:
#   config_file   exit tool config (default: tmp/exit_certificate-kurtosis.json)
#
# Environment:
#   CLAIMER_URL   Base URL of the claimer service (default: 127.0.0.1:7080).
#                 A missing scheme is assumed to be http://.
#   L1_RPC_URL    L1 RPC endpoint (default: config .l1RpcUrl).
#   BRIDGE_ADDRESS AgglayerBridge address on L1 (default: config .l1BridgeAddress).
#   Signing (override the config-derived signer):
#     PRIVATE_KEY            raw hex signing key for the claimAsset transactions
#     KEYSTORE               path to an encrypted keystore JSON
#     KEYSTORE_PASSWORD      password for KEYSTORE
#   DRY_RUN       When 1, only print the parameters / cast command for each claim.
#   ASSUME_YES    When 1, skip the single up-front confirmation prompt.
#
# Examples:
#   ./claim-all.sh
#   ./claim-all.sh tmp/exit_certificate-cardona.json
#   DRY_RUN=1 ./claim-all.sh
#   CLAIMER_URL=http://10.0.0.5:7080 ASSUME_YES=1 ./claim-all.sh
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAIM_ASSET="${SCRIPT_DIR}/claim-asset.sh"

CONFIG_FILE="${1:-tmp/exit_certificate-kurtosis.json}"
CLAIMER_URL="${CLAIMER_URL:-127.0.0.1:7080}"
DRY_RUN="${DRY_RUN:-0}"
ASSUME_YES="${ASSUME_YES:-0}"

usage() {
	cat >&2 <<-'EOF'
		Usage: claim-all.sh [config_file]

		Arguments:
		  config_file        exit tool config (default: tmp/exit_certificate-kurtosis.json)

		Environment variables:
		  CLAIMER_URL        Base URL of the claimer service (default: 127.0.0.1:7080).
		                     A missing scheme is assumed to be http://.
		  L1_RPC_URL         L1 RPC endpoint (default: config .l1RpcUrl).
		  BRIDGE_ADDRESS     AgglayerBridge address on L1 (default: config .l1BridgeAddress).
		  PRIVATE_KEY        Raw hex signing key for the claimAsset transactions.
		  KEYSTORE           Path to an encrypted keystore JSON (alternative to PRIVATE_KEY).
		  KEYSTORE_PASSWORD  Password for KEYSTORE.
		                     If none of the above signer vars are set, the config's local
		                     signerConfig keystore is used.
		  DRY_RUN            When 1, only print the parameters / cast command for each claim.
		  ASSUME_YES         When 1, skip the up-front confirmation prompt.
	EOF
	exit 2
}

if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
	usage
fi

for bin in curl jq; do
	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "error: required dependency '$bin' not found in PATH" >&2
		exit 1
	fi
done

[ -f "$CONFIG_FILE" ] || {
	echo "error: config file '$CONFIG_FILE' not found" >&2
	exit 1
}
[ -x "$CLAIM_ASSET" ] || {
	echo "error: claim-asset.sh not found or not executable at '$CLAIM_ASSET'" >&2
	exit 1
}

# A bare host:port (no scheme) is treated as http://.
case "$CLAIMER_URL" in
	http://* | https://*) ;;
	*) CLAIMER_URL="http://${CLAIMER_URL}" ;;
esac
API_BASE="${CLAIMER_URL%/}/claimer/v1"

# Paths inside the config (outputDir, keystore) are relative to the config's directory.
CONFIG_DIR="$(cd "$(dirname "$CONFIG_FILE")" && pwd)"
resolve_path() {
	# Echo $1 unchanged if absolute, otherwise anchored at the config directory.
	case "$1" in
		/*) printf '%s' "$1" ;;
		*) printf '%s/%s' "$CONFIG_DIR" "$1" ;;
	esac
}

EXIT_ADDRESS="$(jq -r '.exitAddress // empty' "$CONFIG_FILE")"
OUTPUT_DIR_RAW="$(jq -r '.options.outputDir // empty' "$CONFIG_FILE")"
L1_RPC_URL="${L1_RPC_URL:-$(jq -r '.l1RpcUrl // empty' "$CONFIG_FILE")}"
BRIDGE_ADDRESS="${BRIDGE_ADDRESS:-$(jq -r '.l1BridgeAddress // empty' "$CONFIG_FILE")}"

[ -n "$OUTPUT_DIR_RAW" ] || {
	echo "error: config is missing .options.outputDir" >&2
	exit 1
}
OUTPUT_DIR="$(resolve_path "$OUTPUT_DIR_RAW")"
EOA_FILE="${OUTPUT_DIR}/step-b-eoa-balances.json"
[ -f "$EOA_FILE" ] || {
	echo "error: EOA balances file '$EOA_FILE' not found (run the exit tool first)" >&2
	exit 1
}

# Resolve the signer for claim-asset.sh: an explicit PRIVATE_KEY/KEYSTORE env wins;
# otherwise fall back to the config's local signerConfig keystore.
declare -a signer_env=()
if [ -n "${PRIVATE_KEY:-}" ]; then
	signer_env=(PRIVATE_KEY="$PRIVATE_KEY")
elif [ -n "${KEYSTORE:-}" ]; then
	signer_env=(KEYSTORE="$KEYSTORE" KEYSTORE_PASSWORD="${KEYSTORE_PASSWORD:-}")
else
	signer_method="$(jq -r '.signerConfig.Method // empty' "$CONFIG_FILE")"
	if [ "$signer_method" = "local" ]; then
		ks_path="$(jq -r '.signerConfig.Path // empty' "$CONFIG_FILE")"
		ks_pass="$(jq -r '.signerConfig.Password // empty' "$CONFIG_FILE")"
		[ -n "$ks_path" ] || {
			echo "error: config signerConfig.Method is 'local' but Path is empty" >&2
			exit 1
		}
		ks_path="$(resolve_path "$ks_path")"
		[ -f "$ks_path" ] || {
			echo "error: signer keystore '$ks_path' not found" >&2
			exit 1
		}
		signer_env=(KEYSTORE="$ks_path" KEYSTORE_PASSWORD="$ks_pass")
	else
		echo "error: no signer available — set PRIVATE_KEY or KEYSTORE, or use a 'local' signerConfig" >&2
		exit 1
	fi
fi

# Collect the addresses to claim for: every EOA from step-b-eoa-balances.json, then the
# exitAddress (claimed last, since it receives the smart-contract-locked funds).
# step-b-eoa-balances.json is an array of {address,...}; tolerate a plain array of address
# strings as well. Lower-cased and de-duplicated.
declare -a ADDRESSES=()
declare -A seen=()
add_address() {
	local a
	a="$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')"
	[[ "$a" =~ ^0x[0-9a-f]{40}$ ]] || return 0
	# The zero address can appear in the certificate (value parked at 0x0 is covered so the
	# totals reconcile with the LBT), but nobody owns it and the bridge rejects claims to it:
	# claiming would just revert (or burn the funds on L1), so it is skipped.
	if [ "$a" = "0x0000000000000000000000000000000000000000" ]; then
		echo "note: skipping the zero address (its bridge exits are unclaimable by design)"
		return 0
	fi
	[ -n "${seen[$a]:-}" ] && return 0
	seen[$a]=1
	ADDRESSES+=("$a")
}

# Normalize the exit address and keep it out of the EOA list so it is always claimed last.
EXIT_ADDRESS_LC=""
if [ -n "$EXIT_ADDRESS" ]; then
	EXIT_ADDRESS_LC="$(printf '%s' "$EXIT_ADDRESS" | tr '[:upper:]' '[:lower:]')"
	if [[ "$EXIT_ADDRESS_LC" =~ ^0x[0-9a-f]{40}$ ]]; then
		seen[$EXIT_ADDRESS_LC]=1
	else
		EXIT_ADDRESS_LC=""
	fi
fi

while IFS= read -r addr; do
	add_address "$addr"
done < <(jq -r '.[] | if type == "object" then .address else . end' "$EOA_FILE")

[ "${#ADDRESSES[@]}" -gt 0 ] || [ -n "$EXIT_ADDRESS_LC" ] || {
	echo "error: no addresses to claim for" >&2
	exit 1
}

echo "Claimer:        ${CLAIMER_URL}"
echo "L1 RPC:         ${L1_RPC_URL:-<unset>}"
echo "Bridge address: ${BRIDGE_ADDRESS:-<unset>}"
echo "Output dir:     ${OUTPUT_DIR}"
echo "Exit address:   ${EXIT_ADDRESS:-<none>}"
echo "Addresses:      ${#ADDRESSES[@]} EOA(s)$([ -n "$EXIT_ADDRESS_LC" ] && echo ' + exit address (claimed last)')"
echo "Mode:           $([ "$DRY_RUN" = "1" ] && echo 'DRY_RUN (no transactions)' || echo 'SUBMIT')"
echo

# Each claim is confirmed individually below (set ASSUME_YES=1 to skip every prompt).
if [ "$DRY_RUN" != "1" ] && [ "$ASSUME_YES" != "1" ]; then
	echo "(you will be asked to confirm each claim; set ASSUME_YES=1 to skip the prompts)"
	echo
fi

# Fetch the deposit_count list for a destination address via GET /bridges.
fetch_deposit_counts() {
	local dest="$1" response http_code body
	response="$(curl -sS -w $'\n%{http_code}' \
		--get "${API_BASE}/bridges" \
		--data-urlencode "dest_address=${dest}")" || return 1
	http_code="${response##*$'\n'}"
	body="${response%$'\n'*}"
	if [ "$http_code" != "200" ]; then
		echo "  warning: /bridges returned HTTP ${http_code} for ${dest}" >&2
		echo "$body" | jq -r '.error // .' >&2 2>/dev/null || true
		return 1
	fi
	echo "$body" | jq -r '.bridges[]?.deposit_count'
}

total_claims=0
total_ok=0
total_fail=0
total_skipped=0

# Run-summary stats, populated by claim_for_address.
eoa_with_bridges=0      # EOAs that had at least one bridge exit
exit_bridge_count=-1    # bridge exits for the exit address (-1 = no exit address)
declare -a multi_bridge_addrs=()  # "address (count)" for any address with >1 bridge exit

# Claim every pending deposit for a single address. When warn_if_empty=1, the address is
# the exit address (expected to hold the smart-contract-locked funds), so the absence of
# bridge exits is reported as a warning.
claim_for_address() {
	local addr="$1" warn_if_empty="${2:-0}" dc n
	echo "=== ${addr} ==="
	mapfile -t deposits < <(fetch_deposit_counts "$addr" || true)
	n="${#deposits[@]}"
	if [ "$warn_if_empty" = "1" ]; then
		exit_bridge_count="$n"
	elif [ "$n" -gt 0 ]; then
		eoa_with_bridges=$((eoa_with_bridges + 1))
	fi
	[ "$n" -gt 1 ] && multi_bridge_addrs+=("${addr} (${n})")
	if [ "$n" -eq 0 ]; then
		if [ "$warn_if_empty" = "1" ]; then
			echo "  warning: no bridge exits found for exit address ${addr}" >&2
		else
			echo "  no bridge exits"
		fi
		echo
		return 0
	fi
	echo "  ${n} bridge exit(s): ${deposits[*]}"
	for dc in "${deposits[@]}"; do
		echo "  --- deposit_count=${dc} ---"
		# Confirm each claim individually (unless DRY_RUN or ASSUME_YES).
		if [ "$DRY_RUN" != "1" ] && [ "$ASSUME_YES" != "1" ]; then
			read -r -p "  Submit claimAsset for ${addr} deposit_count=${dc}? [y/N] " reply
			case "$reply" in
				y | Y | yes | YES) ;;
				*)
					total_skipped=$((total_skipped + 1))
					echo "  skipped."
					continue
					;;
			esac
		fi
		total_claims=$((total_claims + 1))
		if env "${signer_env[@]}" \
			CLAIMER_URL="$CLAIMER_URL" \
			L1_RPC_URL="$L1_RPC_URL" \
			BRIDGE_ADDRESS="$BRIDGE_ADDRESS" \
			DRY_RUN="$DRY_RUN" \
			ASSUME_YES=1 \
			"$CLAIM_ASSET" "$addr" "$dc"; then
			total_ok=$((total_ok + 1))
		else
			total_fail=$((total_fail + 1))
            echo " ----------------------------------------------------"
			echo "  warning: claim failed for ${addr} deposit_count=${dc}" >&2
		fi
	done
	echo
}

for addr in "${ADDRESSES[@]}"; do
	claim_for_address "$addr"
done

# Claim the exit address last, warning if it turns out to have no bridge exits.
if [ -n "$EXIT_ADDRESS_LC" ]; then
	claim_for_address "$EXIT_ADDRESS_LC" 1
fi

echo "===================== Summary ====================="
echo "EOAs:             ${#ADDRESSES[@]} total, ${eoa_with_bridges} with bridge exits"
if [ -n "$EXIT_ADDRESS_LC" ]; then
	if [ "$exit_bridge_count" -gt 0 ]; then
		echo "Exit address:     ${EXIT_ADDRESS_LC} — ${exit_bridge_count} bridge exit(s)"
	else
		echo "Exit address:     ${EXIT_ADDRESS_LC} — no bridge exits"
	fi
else
	echo "Exit address:     <none configured>"
fi
if [ "${#multi_bridge_addrs[@]}" -gt 0 ]; then
	echo "Addresses with >1 bridge exit:"
	for a in "${multi_bridge_addrs[@]}"; do
		echo "  - ${a}"
	done
fi
echo "Claims:           submitted=${total_claims} ok=${total_ok} failed=${total_fail} skipped=${total_skipped}"
echo "==================================================="
[ "$total_fail" -eq 0 ]
