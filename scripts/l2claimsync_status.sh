#!/bin/bash
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5576/}

curl -X POST $AGGKIT_URL -H "Content-Type: application/json"  -d '{"method":"l2claimsync_status", "params":[], "id":1}' 2>/dev/null | jq . 
