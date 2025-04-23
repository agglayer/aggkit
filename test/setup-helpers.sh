#!/bin/bash

# Function is used to fund the receiver address with native tokens
# from the sender address. It takes four arguments:
# 1. sender_private_key: The private key of the sender address
# 2. receiver_addr: The address to be funded
# 3. amount: The amount of native tokens to be sent
# 4. rpc_url: The RPC URL of the Ethereum network
function fund() {
    local sender_private_key=$1
    local receiver_addr=$2
    local amount=$3
    local rpc_url=$4

    if [ -z "$sender_private_key" ] || [ -z "$receiver_addr" ] || [ -z "$amount" ] || [ -z "$rpc_url" ]; then
        echo "⚠️ Usage: fund <sender_private_key> <receiver_addr> <amount> <rpc_url>" >&2
        return 1
    fi

    # Fetch gas price
    local raw_gas_price=$(cast gas-price --rpc-url "$rpc_url" 2>/dev/null)
    status=$?
    if [ $status -ne 0 ]; then
        echo "❌ Failed to fetch gas price from $rpc_url" >&2
        return 1
    fi

    # Bump gas price by 50% to avoid transaction being underpriced
    local gas_price=$(printf "%.0f" "$(echo "$raw_gas_price * 1.5" | bc -l)")

    echo "Using bumped gas price: $gas_price wei (original: $raw_gas_price wei)"

    # Fund the receiver address with the specified amount of the specified token
    cast send --rpc-url "$rpc_url" \
        --legacy --private-key "$sender_private_key" \
        --gas-price "$gas_price" \
        --timeout 180 \
        --rpc-timeout 180 \
        --value "$amount" \
        "$receiver_addr"

    local status=$?
    if [ $status -ne 0 ]; then
        echo "❌ Failed to fund $receiver_addr with $amount of native tokens" >&2
        return $status
    fi

    echo "✅ Successfully funded $receiver_addr with $amount of native tokens" >&2
}
