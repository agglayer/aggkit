#!/bin/bash
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5577}

START_BLOCK=${1:-0}
END_BLOCK=${2:-99999999}
echo "Requesting claims from block ${START_BLOCK} to ${END_BLOCK}..." >&2
curl -fsS "${AGGKIT_URL%/}/l2claimsync/v1/claims?from_block=${START_BLOCK}&to_block=${END_BLOCK}" | jq .
