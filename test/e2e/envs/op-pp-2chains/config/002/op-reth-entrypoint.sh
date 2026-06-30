#!/bin/sh
set -e

# Install jq and wget (not always included in op-reth Alpine image)
apk add --no-cache jq wget > /dev/null 2>&1

echo "=== op-reth entrypoint ==="

DATADIR=/data/op-reth
GENESIS_FILE=/shared/patched-genesis.json

# If already initialized (container restart), skip patching and just start op-reth.
# The patched genesis file persists in the shared volume.
if [ -f "/shared/l2_genesis_hash" ] && [ -f "/shared/l2_genesis_time" ] && [ -f "$GENESIS_FILE" ]; then
    echo "Already initialized, skipping genesis patching (container restart)"
    echo "Existing genesis hash: $(cat /shared/l2_genesis_hash)"
    echo "Existing genesis time: $(cat /shared/l2_genesis_time 2>/dev/null || echo unknown)"
    exec op-reth node \
        --datadir="$DATADIR" \
        --chain="$GENESIS_FILE" \
        --http \
        --http.addr=0.0.0.0 \
        --http.port=8545 \
        --http.corsdomain='*' \
        --http.api=admin,net,eth,web3,debug,trace,txpool,miner \
        --ws \
        --ws.addr=0.0.0.0 \
        --ws.port=8546 \
        --ws.api=net,eth,web3,debug,txpool,miner \
        --ws.origins='*' \
        --authrpc.addr=0.0.0.0 \
        --authrpc.port=8551 \
        --authrpc.jwtsecret=/jwt/jwtsecret \
        --disable-discovery
fi

echo "=== Patching L2 genesis timestamp ==="

# Copy genesis to writable shared location
cp /genesis-ro/l2-genesis.json "$GENESIS_FILE"

# Wait for geth to produce L1 origin block 344 and read its real timestamp.
# rollup.json genesis.l2_time is stale (set at snapshot creation); the live L1
# produces block 344 at the current wall-clock time, which will be later — using
# the stale value causes op-node to loop-reset ("L2 time before L1 origin time").
# The geth healthcheck (threshold 320) ensures the L1 chain is progressing before
# this container starts, so block 344 arrives within ~3 minutes.
L1_ORIGIN_HEX="0x158"  # block 344
L1_WAIT=0
L1_TS=""
while [ $L1_WAIT -lt 300 ]; do
    RESP=$(wget -qO- --post-data="{\"jsonrpc\":\"2.0\",\"method\":\"eth_getBlockByNumber\",\"params\":[\"$L1_ORIGIN_HEX\",false],\"id\":1}" \
        --header="Content-Type: application/json" http://geth:8545 2>/dev/null || true)
    L1_TS=$(echo "$RESP" | jq -r '.result.timestamp // empty' 2>/dev/null || true)
    if [ -n "$L1_TS" ] && [ "$L1_TS" != "null" ]; then
        break
    fi
    L1_WAIT=$((L1_WAIT + 2))
    sleep 2
done
if [ -z "$L1_TS" ] || [ "$L1_TS" = "null" ]; then
    echo "ERROR: geth block 344 not available after 300s"
    exit 1
fi
L1_ORIGIN_TS_DEC=$(printf '%d' "$L1_TS")
NOW=$((L1_ORIGIN_TS_DEC + 1))
NOW_HEX=$(printf '0x%x' "$NOW")
echo "L1 origin block 344 timestamp: $L1_ORIGIN_TS_DEC"
echo "Setting L2 genesis timestamp to $NOW ($NOW_HEX)"

jq --arg ts "$NOW_HEX" '.timestamp = $ts' "$GENESIS_FILE" > /tmp/genesis-patched.json
mv /tmp/genesis-patched.json "$GENESIS_FILE"

# Write the patched timestamp for op-node to read
echo "$NOW" > /shared/l2_genesis_time

# Start op-reth node briefly on local port to extract genesis block hash
echo "Starting op-reth briefly to extract genesis block hash..."
op-reth node \
    --datadir="$DATADIR" \
    --chain="$GENESIS_FILE" \
    --http \
    --http.addr=127.0.0.1 \
    --http.port=18545 \
    --http.api=eth \
    --authrpc.addr=127.0.0.1 \
    --authrpc.port=18551 \
    --authrpc.jwtsecret=/jwt/jwtsecret \
    --disable-discovery \
    2>&1 &
RETH_PID=$!

# Wait for RPC to be ready (up to 30s)
RETRIES=0
RESP=""
while [ $RETRIES -lt 30 ]; do
    RESP=$(wget -qO- --post-data='{"jsonrpc":"2.0","method":"eth_getBlockByNumber","params":["0x0",false],"id":1}' \
        --header="Content-Type: application/json" http://127.0.0.1:18545 2>/dev/null || true)
    if [ -n "$RESP" ] && echo "$RESP" | grep -q '"result"'; then
        break
    fi
    RETRIES=$((RETRIES + 1))
    sleep 1
done
kill $RETH_PID 2>/dev/null || true
wait $RETH_PID 2>/dev/null || true

# Extract hash from response
GENESIS_HASH=$(echo "$RESP" | jq -r '.result.hash // empty' 2>/dev/null || true)
if [ -n "$GENESIS_HASH" ]; then
    echo "L2 genesis block hash: $GENESIS_HASH"
    echo "$GENESIS_HASH" > /shared/l2_genesis_hash
else
    echo "ERROR: Could not extract genesis hash"
    echo "RPC response: $RESP"
    exit 1
fi

echo "=== op-reth entrypoint: starting op-reth node ==="

exec op-reth node \
    --datadir="$DATADIR" \
    --chain="$GENESIS_FILE" \
    --http \
    --http.addr=0.0.0.0 \
    --http.port=8545 \
    --http.corsdomain='*' \
    --http.api=admin,net,eth,web3,debug,trace,txpool,miner \
    --ws \
    --ws.addr=0.0.0.0 \
    --ws.port=8546 \
    --ws.api=net,eth,web3,debug,txpool,miner \
    --ws.origins='*' \
    --authrpc.addr=0.0.0.0 \
    --authrpc.port=8551 \
    --authrpc.jwtsecret=/jwt/jwtsecret \
    --disable-discovery
