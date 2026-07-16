#!/usr/bin/env bash
# Extract the agglayer LBT (local balance tree) for an L2 network via admin_getTokenBalance.
# Mirrors what exit_certificate Step F sends.
#
# Usage:
#   ./get-agglayer-lbt.sh [ADMIN_URL] [NETWORK_ID] [BEARER_TOKEN]
#
# Defaults target a local Kurtosis devnet; override with the args above for other environments.
set -euo pipefail

ADMIN_URL="${1:-http://localhost:32797}"
NETWORK_ID="${2:-1}"
BEARER_TOKEN="${3:-}"

auth=()
if [[ -n "$BEARER_TOKEN" ]]; then
  auth=(-H "Authorization: Bearer $BEARER_TOKEN")
fi

payload=$(printf '{"jsonrpc":"2.0","id":1,"method":"admin_getTokenBalance","params":[%s,null]}' "$NETWORK_ID")

resp=$(curl -sS -X POST "$ADMIN_URL" \
  -H "Content-Type: application/json" \
  "${auth[@]}" \
  --data "$payload")

# Pretty-print the JSON-RPC result if jq is available, otherwise dump raw.
if command -v jq >/dev/null 2>&1; then
  echo "$resp" | jq '.result'
else
  echo "$resp"
fi
