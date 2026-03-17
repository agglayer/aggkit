#!/bin/bash
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5576/}

curl -X POST $AGGKIT_URL -H "Content-Type: application/json"  -d '{"method":"aggsender_getCertificateHeaderPerHeight", "params":[], "id":1}' | jq .
