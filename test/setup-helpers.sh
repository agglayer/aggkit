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
        echo "⚠️ Usage: fund <sender_private_key> <receiver_addr> <amount> <rpc_url>"
        return 1
    fi

    # Fetch gas price
    local raw_gas_price
    if ! raw_gas_price=$(cast gas-price --rpc-url "$rpc_url" 2>/dev/null); then
        echo "❌ Failed to fetch gas price from $rpc_url"
        return 1
    fi

    # Bump gas price by 50% to avoid transaction being underpriced
    local gas_price=$(printf "%.0f" "$(echo "$raw_gas_price * 1.5" | bc -l)")

    echo "Using bumped gas price: $gas_price wei (original: $raw_gas_price wei)"

    # Fund the receiver address with the specified amount of the specified token
    cast send --rpc-url "$rpc_url" \
        --legacy --private-key "$sender_private_key" \
        --gas-price "$gas_price" \
        --timeout 180s \
        --value "$amount" \
        "$receiver_addr"
    if [ $? -ne 0 ]; then
        echo "❌ Failed to fund $receiver_addr with $amount of native tokens"
        return 1
    fi
    echo "✅ Successfully funded $receiver_addr with $amount of native tokens"
}

# Function is used to resolve the L2 RPC URL from the Kurtosis environment.
# It takes two arguments:
# 1. enclave: The name of the Kurtosis enclave
# 2. l2_rpc_node: The name of the L2 RPC node (default: cdk-erigon-rpc-001)
function get_l2_rpc_url() {
    local enclave=$1
    local l2_rpc_node=${2:-cdk-erigon-rpc-001}

    if [[ -z "$enclave" ]]; then
        echo "❌ Missing enclave name. Usage: get_l2_rpc_url <enclave> [l2_rpc_node]" >&2
        return 1
    fi

    local l2_rpc_url

    l2_rpc_url=$(kurtosis port print "$enclave" "$l2_rpc_node" rpc)
    if [ $? -ne 0 ]; then
        echo "❌ Failed to resolve L2 RPC URL from Kurtosis environment"
        return 1
    fi

    echo "$l2_rpc_url"
}
