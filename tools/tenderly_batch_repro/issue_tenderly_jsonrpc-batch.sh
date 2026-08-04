#!/usr/bin/env bash
# Reproducer: Tenderly batch eth_getBlockByNumber returns null for historical blocks
# while individual requests to the same blocks return full data.
#
# Usage: RPC_URL=https://... ./repro.sh
#
# Both blocks below exist on Sepolia and return data via individual requests.
# Block 5167139 timestamp: 2024-01-28
# Block 5167005 timestamp: 2024-01-27

set -euo pipefail

if [[ -z "${RPC_URL:-}" ]]; then
  echo "Error: RPC_URL is not set." >&2
  echo "Hint:  RPC_URL=https://sepolia.gateway.tenderly.co/YOUR_KEY_HERE ./repro.sh" >&2
  exit 1
fi
BLOCK_A="0x4EEB3B"  # 5167139
BLOCK_B="0x4ED79D"  # 5167005

echo "=================================================="
echo " RPC: $RPC_URL"
echo " Block A: $BLOCK_A (5167139)"
echo " Block B: $BLOCK_B (5167005)"
echo "=================================================="

echo ""
echo "--- [1] Individual request for Block A ---"
curl -s -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$BLOCK_A\", false],\"id\":1}" \
  | python3 -c "
import json, sys
r = json.load(sys.stdin).get('result', {})
if r:
    print(f'  number:    {r[\"number\"]}')
    print(f'  hash:      {r[\"hash\"]}')
    print(f'  timestamp: {r[\"timestamp\"]}')
else:
    print('  result: NULL')
"

echo ""
echo "--- [2] Individual request for Block B ---"
curl -s -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$BLOCK_B\", false],\"id\":2}" \
  | python3 -c "
import json, sys
r = json.load(sys.stdin).get('result', {})
if r:
    print(f'  number:    {r[\"number\"]}')
    print(f'  hash:      {r[\"hash\"]}')
    print(f'  timestamp: {r[\"timestamp\"]}')
else:
    print('  result: NULL')
"

echo ""
echo "--- [3] Batch request for Block A + Block B ---"
curl -s -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d "[
    {\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$BLOCK_A\", false],\"id\":1},
    {\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$BLOCK_B\", false],\"id\":2}
  ]" \
  | python3 -c "
import json, sys
data = json.load(sys.stdin)
for item in data:
    r = item.get('result')
    err = item.get('error')
    if r:
        print(f'  id={item[\"id\"]}: number={r[\"number\"]}, hash={r[\"hash\"]}')
    else:
        print(f'  id={item[\"id\"]}: result=NULL, error={err}')
"

echo ""
echo "--- [4] Batch request with recent blocks (sanity check) ---"
LATEST_HEX=$(curl -s -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"eth_blockNumber","params":[],"id":0}' \
  | python3 -c "import json,sys; print(json.load(sys.stdin)['result'])")
LATEST=$((16#${LATEST_HEX#0x}))
PREV=$(printf "0x%X" $((LATEST - 1)))
echo "  Using recent blocks: $PREV and $LATEST_HEX"
curl -s -X POST "$RPC_URL" \
  -H "Content-Type: application/json" \
  -d "[
    {\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$PREV\", false],\"id\":1},
    {\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$LATEST_HEX\", false],\"id\":2}
  ]" \
  | python3 -c "
import json, sys
data = json.load(sys.stdin)
for item in data:
    r = item.get('result')
    err = item.get('error')
    if r:
        print(f'  id={item[\"id\"]}: number={r[\"number\"]}, hash={r[\"hash\"]}')
    else:
        print(f'  id={item[\"id\"]}: result=NULL, error={err}')
"

echo ""
echo "=================================================="
echo " EXPECTED: [1] and [2] return data, [3] returns"
echo " null for both, [4] returns data for both."
echo "=================================================="
