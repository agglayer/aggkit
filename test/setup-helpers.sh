#!/bin/bash

function fund() {
    local sender_private_key=$1
    local receiver_addr=$2
    local amount=$3
    local rpc_url=$4

    if [ -z "$sender_private_key" ] || [ -z "$receiver_addr" ] || [ -z "$amount" ] || [ -z "$rpc_url" ]; then
        echo "⚠️ Usage: fund <sender_private_key> <receiver_addr> <amount> <rpc_url>" >&2
        return 1
    fi

    local max_attempts=3
    local attempt=1
    local success=0

    while [ $attempt -le $max_attempts ]; do
        echo "🚀 Attempt $attempt to fund the $receiver_addr..." >&2

        # Fetch gas price before each attempt
        local raw_gas_price
        raw_gas_price=$(cast gas-price --rpc-url "$rpc_url" 2>/dev/null)
        local status=$?
        if [ $status -ne 0 ] || [ -z "$raw_gas_price" ]; then
            echo "❌ Failed to fetch gas price from $rpc_url (attempt $attempt)" >&2
            return 1
        fi

        # Bump gas price by 50%
        local gas_price
        gas_price=$(printf "%.0f" "$(echo "$raw_gas_price * 1.5" | bc -l)")

        echo "Using bumped gas price: $gas_price [wei] (original: $raw_gas_price [wei])" >&2

        cast send --rpc-url "$rpc_url" \
            --legacy \
            --private-key "$sender_private_key" \
            --gas-price "$gas_price" \
            --value "$amount" \
            "$receiver_addr"

        status=$?
        if [ $status -eq 0 ]; then
            success=1
            break
        fi

        echo "⚠️ Attempt $attempt failed. Retrying in 3s..." >&2
        sleep 3
        attempt=$((attempt + 1))
    done

    if [ $success -eq 0 ]; then
        echo "❌ Failed to fund $receiver_addr after $max_attempts attempts" >&2
        return 1
    fi

    echo "✅ Successfully funded $receiver_addr with $amount of native tokens" >&2
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
    local status=$?
    if [ $status -ne 0 ]; then
        echo "❌ Failed to resolve L2 RPC URL from Kurtosis environment" >&2
        return 1
    fi

    echo "$l2_rpc_url"
}
