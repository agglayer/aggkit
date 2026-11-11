#!/bin/bash
# Prints the full certificate, which the admin API serves.
AGGKIT_URL=${AGGKIT_URL:-http://localhost:5579}

curl -fsS "${AGGKIT_URL%/}/aggsender/v1/certificate" | jq .
