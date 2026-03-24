#!/bin/bash
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5576/}

START_BLOCK=${1:-0}
END_BLOCK=${2:-99999999}
echo "Requesting claims from block ${START_BLOCK} to ${END_BLOCK}..." >&2
curl -X POST $AGGKIT_URL -H "Content-Type: application/json"  -d '{"method":"l2claimsync_getClaims", "params":['${START_BLOCK},${END_BLOCK}'], "id":1}' 2>/dev/null | jq . 
