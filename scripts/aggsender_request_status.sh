#!/bin/bash
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5577}

curl -fsS "${AGGKIT_URL%/}/aggsender/v1/status" | jq .
