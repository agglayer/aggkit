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
        echo "Usage: fund <sender_private_key> <receiver_addr> <amount> <rpc_url>"
        return 1
    fi

    gas_price=$(cast gas-price --rpc-url "$rpc_url")

    # Fund the receiver address with the specified amount of the specified token
    cast send --rpc-url "$rpc_url" --private-key "$sender_private_key" --gas-price "$gas_price" --value "$amount" "$receiver_addr"
    if [ $? -ne 0 ]; then
        echo "Failed to fund $receiver_addr with $amount of native tokens"
        return 1
    fi
    echo "Successfully funded $receiver_addr with $amount of native tokens"
}
