#!/bin/bash
AGGKIT_REST=${AGGKIT_REST:-http://localhost:5577/}

echo "Requesting claims $AGGKIT_REST" >&2
curl  -H "Content-Type: application/json" $AGGKIT_REST'/bridge/v1/claims?network_id=1&include_all_fields=true' 


