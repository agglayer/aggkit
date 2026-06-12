#!/usr/bin/env bash
#
# list-bridges.sh — list the bridge exits (deposits) associated with a destination
# address by querying the exit_certificate_claimer service.
#
# Usage:
#   ./list-bridges.sh <dest_address>
#
# Environment:
#   CLAIMER_URL   Base URL of the claimer service (default: http://localhost:8080)
#
# Examples:
#   ./list-bridges.sh 0xAbC0000000000000000000000000000000000001
#   CLAIMER_URL=http://10.0.0.5:9090 ./list-bridges.sh 0xAbC...001
#
set -euo pipefail

CLAIMER_URL="${CLAIMER_URL:-http://localhost:8080}"
API_BASE="${CLAIMER_URL%/}/claimer/v1"

usage() {
	echo "Usage: $0 <dest_address>" >&2
	echo "  CLAIMER_URL (env) defaults to http://localhost:8080" >&2
	exit 2
}

for bin in curl jq; do
	if ! command -v "$bin" >/dev/null 2>&1; then
		echo "error: required dependency '$bin' not found in PATH" >&2
		exit 1
	fi
done

[ "$#" -eq 1 ] || usage
DEST_ADDRESS="$1"

if ! [[ "$DEST_ADDRESS" =~ ^0x[0-9a-fA-F]{40}$ ]]; then
	echo "error: '$DEST_ADDRESS' is not a valid hex address" >&2
	exit 1
fi

# Fetch /bridges, capturing body and HTTP status separately.
response="$(curl -sS -w $'\n%{http_code}' \
	--get "${API_BASE}/bridges" \
	--data-urlencode "dest_address=${DEST_ADDRESS}")"
http_code="${response##*$'\n'}"
body="${response%$'\n'*}"

if [ "$http_code" != "200" ]; then
	echo "error: claimer returned HTTP ${http_code}" >&2
	echo "$body" | jq -r '.error // .' >&2 2>/dev/null || echo "$body" >&2
	exit 1
fi

count="$(echo "$body" | jq '.bridges | length')"
network_id="$(echo "$body" | jq -r '.network_id')"

echo "Network ID:          ${network_id}"
echo "Destination address: ${DEST_ADDRESS}"
echo "Bridge exits found:  ${count}"
echo

if [ "$count" -eq 0 ]; then
	echo "No bridge exits associated with this address."
	exit 0
fi

# Tabular summary; use --claim-params <deposit_count> against claim-asset.sh to claim.
echo "$body" | jq -r '
	["DEPOSIT_COUNT","LEAF_TYPE","ORIGIN_NET","TOKEN","AMOUNT"],
	(.bridges[] | [
		(.deposit_count|tostring),
		(.leaf_type|tostring),
		(.origin_network|tostring),
		.origin_token_address,
		.amount
	]) | @tsv' | column -t -s $'\t'

echo
echo "Raw JSON:"
echo "$body" | jq .
