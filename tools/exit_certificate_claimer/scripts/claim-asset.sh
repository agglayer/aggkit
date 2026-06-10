#!/usr/bin/env bash
#
# claim-asset.sh — fetch the claimAsset parameters for a single bridge exit from the
# exit_certificate_claimer service and submit AgglayerBridge.claimAsset on L1.
#
# Usage:
#   ./claim-asset.sh <dest_address> <deposit_count>
#
# Environment:
#   CLAIMER_URL     Base URL of the claimer service (default: http://localhost:8080)
#   L1_RPC_URL      L1 RPC endpoint where AgglayerBridge lives (required to send)
#   BRIDGE_ADDRESS  AgglayerBridge contract address on L1 (required to send)
#   PRIVATE_KEY     Signing key for the claimAsset transaction (required to send)
#   DRY_RUN         When set to 1, only print the parameters and the cast command (no tx)
#   ASSUME_YES      When set to 1, skip the interactive confirmation prompt
#
# Examples:
#   DRY_RUN=1 ./claim-asset.sh 0xAbC...001 42
#   L1_RPC_URL=http://localhost:8545 BRIDGE_ADDRESS=0xBridge... PRIVATE_KEY=0xabc... \
#     ./claim-asset.sh 0xAbC...001 42
#
set -euo pipefail

CLAIMER_URL="${CLAIMER_URL:-http://localhost:8080}"
API_BASE="${CLAIMER_URL%/}/claimer/v1"
DRY_RUN="${DRY_RUN:-0}"
ASSUME_YES="${ASSUME_YES:-0}"

# AgglayerBridge.claimAsset selector signature.
CLAIM_ASSET_SIG="claimAsset(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"

usage() {
	echo "Usage: $0 <dest_address> <deposit_count>" >&2
	echo "  CLAIMER_URL (env)    defaults to http://localhost:8080" >&2
	echo "  L1_RPC_URL, BRIDGE_ADDRESS, PRIVATE_KEY (env) required to submit the tx" >&2
	echo "  DRY_RUN=1 (env)      only print params and the cast command" >&2
	exit 2
}

for bin in curl jq; do
	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "error: required dependency '$bin' not found in PATH" >&2
		exit 1
	fi
done

[ "$#" -eq 2 ] || usage
DEST_ADDRESS="$1"
DEPOSIT_COUNT="$2"

if ! [[ "$DEST_ADDRESS" =~ ^0x[0-9a-fA-F]{40}$ ]]; then
	echo "error: '$DEST_ADDRESS' is not a valid hex address" >&2
	exit 1
fi
if ! [[ "$DEPOSIT_COUNT" =~ ^[0-9]+$ ]]; then
	echo "error: deposit_count '$DEPOSIT_COUNT' must be a non-negative integer" >&2
	exit 1
fi

# Fetch /claim-params for the selected deposit, capturing body and HTTP status.
response="$(curl -sS -w $'\n%{http_code}' \
	--get "${API_BASE}/claim-params" \
	--data-urlencode "dest_address=${DEST_ADDRESS}" \
	--data-urlencode "deposit_count=${DEPOSIT_COUNT}")"
http_code="${response##*$'\n'}"
body="${response%$'\n'*}"

if [ "$http_code" != "200" ]; then
	echo "error: claimer returned HTTP ${http_code}" >&2
	if [ "$http_code" = "409" ]; then
		echo "  the certificate's local exit root is not yet settled on L1." >&2
	fi
	echo "$body" | jq -r '.error // .' >&2 2>/dev/null || echo "$body" >&2
	exit 1
fi

n_claims="$(echo "$body" | jq '.claims | length')"
if [ "$n_claims" -eq 0 ]; then
	echo "error: no claim found for deposit_count=${DEPOSIT_COUNT} at ${DEST_ADDRESS}" >&2
	exit 1
fi
if [ "$n_claims" -ne 1 ]; then
	echo "error: expected exactly 1 claim, got ${n_claims} (refine deposit_count)" >&2
	exit 1
fi

claim="$(echo "$body" | jq '.claims[0]')"

# Extract every claimAsset argument. Fixed bytes32[32] arrays are rendered as "[0x..,0x..]".
smt_local="$(echo "$claim" | jq -r '.smt_proof_local_exit_root | "[" + join(",") + "]"')"
smt_rollup="$(echo "$claim" | jq -r '.smt_proof_rollup_exit_root | "[" + join(",") + "]"')"
global_index="$(echo "$claim" | jq -r '.global_index')"
mainnet_exit_root="$(echo "$claim" | jq -r '.mainnet_exit_root')"
rollup_exit_root="$(echo "$claim" | jq -r '.rollup_exit_root')"
origin_network="$(echo "$claim" | jq -r '.origin_network')"
origin_token="$(echo "$claim" | jq -r '.origin_token_address')"
destination_network="$(echo "$claim" | jq -r '.destination_network')"
destination_address="$(echo "$claim" | jq -r '.destination_address')"
amount="$(echo "$claim" | jq -r '.amount')"
metadata="$(echo "$claim" | jq -r '.metadata')"

echo "claimAsset parameters for deposit_count=${DEPOSIT_COUNT}:"
echo "  global_index:        ${global_index}"
echo "  mainnet_exit_root:   ${mainnet_exit_root}"
echo "  rollup_exit_root:    ${rollup_exit_root}"
echo "  origin_network:      ${origin_network}"
echo "  origin_token:        ${origin_token}"
echo "  destination_network: ${destination_network}"
echo "  destination_address: ${destination_address}"
echo "  amount:              ${amount}"
echo "  metadata:            ${metadata}"
echo "  l1_info_tree_index:  $(echo "$claim" | jq -r '.l1_info_tree_index')"
echo

# Assemble the cast send argument vector once; reused for the printed command and execution.
cast_args=(
	"$CLAIM_ASSET_SIG"
	"$smt_local"
	"$smt_rollup"
	"$global_index"
	"$mainnet_exit_root"
	"$rollup_exit_root"
	"$origin_network"
	"$origin_token"
	"$destination_network"
	"$destination_address"
	"$amount"
	"$metadata"
)

if [ "$DRY_RUN" = "1" ]; then
	echo "DRY_RUN=1 — not submitting. Equivalent cast command:"
	printf 'cast send "%s" \\\n' "${BRIDGE_ADDRESS:-<BRIDGE_ADDRESS>}"
	printf '  --rpc-url "%s" --private-key "<PRIVATE_KEY>" \\\n' "${L1_RPC_URL:-<L1_RPC_URL>}"
	printf "  '%s' \\\\\n" "${cast_args[0]}"
	for ((i = 1; i < ${#cast_args[@]}; i++)); do
		printf "  '%s'" "${cast_args[$i]}"
		[ "$i" -lt $((${#cast_args[@]} - 1)) ] && printf ' \\'
		printf '\n'
	done
	exit 0
fi

# Submission path: validate signing prerequisites.
if ! command -v cast >/dev/null 2>&1; then
	echo "error: 'cast' (foundry) not found in PATH; install foundry or use DRY_RUN=1" >&2
	exit 1
fi
: "${L1_RPC_URL:?error: L1_RPC_URL is required to submit the transaction}"
: "${BRIDGE_ADDRESS:?error: BRIDGE_ADDRESS is required to submit the transaction}"
: "${PRIVATE_KEY:?error: PRIVATE_KEY is required to submit the transaction}"

if [ "$ASSUME_YES" != "1" ]; then
	read -r -p "Submit claimAsset to ${BRIDGE_ADDRESS} via ${L1_RPC_URL}? [y/N] " reply
	case "$reply" in
		y | Y | yes | YES) ;;
		*)
			echo "Aborted." >&2
			exit 1
			;;
	esac
fi

cast send "$BRIDGE_ADDRESS" \
	--rpc-url "$L1_RPC_URL" \
	--private-key "$PRIVATE_KEY" \
	"${cast_args[@]}"
