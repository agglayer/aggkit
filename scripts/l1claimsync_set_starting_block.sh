#!/bin/bash
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5576/}

START_BLOCK=${1:-1}

echo "Requesting L1 Start block $START_BLOCK..." >&2
curl -X POST $AGGKIT_URL -H "Content-Type: application/json"  -d '{"method":"l1claimsync_setNextRequiredBlock", "params":['${START_BLOCK}'], "id":1}' 2>/dev/null | jq . 
