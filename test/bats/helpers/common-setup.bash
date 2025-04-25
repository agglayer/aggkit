#!/usr/bin/env bash

_common_setup() {
    bats_load_library 'bats-support'
    if [ $? -ne 0 ]; then return 1; fi

    bats_load_library 'bats-assert'
    if [ $? -ne 0 ]; then return 1; fi

    PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." >/dev/null 2>&1 && pwd)"
    if [ $? -ne 0 ]; then
        echo "❌ Failed to determine PROJECT_ROOT" >&2
        return 1
    fi

    PATH="$PROJECT_ROOT/src:$PATH"

    readonly mint_fn_sig="function mint(address,uint256)"
    readonly balance_of_fn_sig="function balanceOf(address) (uint256)"
    readonly approve_fn_sig="function approve(address,uint256)"

    readonly enclave="${KURTOSIS_ENCLAVE:-aggkit}"
    readonly contracts_container="${KURTOSIS_CONTRACTS:-contracts-001}"
    readonly contracts_service_wrapper="${KURTOSIS_CONTRACTS_WRAPPER:-"kurtosis service exec $enclave $contracts_container"}"

    if [[ -z "${L2_ETH_RPC_URL}" ]]; then
        echo "ℹ️ L2_ETH_RPC_URL not provided, attempting resolution from known fallback nodes..." >&3

        local fallback_nodes=("op-el-1-op-geth-op-node-001" "cdk-erigon-rpc-001")
        local resolved_url=""

        for node in "${fallback_nodes[@]}"; do
            # Need to invoke the command this way, otherwise it would fail the entire test
            # if the node is not running, but this is just a sanity check
            kurtosis service inspect "$enclave" "$node" || {
                echo "⚠️ Node $node is not running in the "$enclave" enclave, trying next one..." >&3
                continue
            }

            resolved_url=$(kurtosis port print "$enclave" "$node" rpc)
            if [ -n "$resolved_url" ]; then
                echo "✅ Successfully resolved L2 RPC URL ("$resolved_url") from "$node"" >&3
                break
            fi
        done

        if [ -z "$resolved_url" ]; then
            echo "❌ Failed to resolve L2 RPC URL from all fallback nodes" >&2
            return 1
        fi

        readonly l2_rpc_url="$resolved_url"
    else
        readonly l2_rpc_url="$L2_ETH_RPC_URL"
    fi

    if [[ -z "${DISABLE_L2_FUND}" || "${DISABLE_L2_FUND}" == "false" ]]; then
        readonly test_account_key=${SENDER_PRIVATE_KEY:-"12d7de8621a77640c9241b2595ba78ce443d05e94090365ab3bb5e19df82c625"}
        readonly test_account_addr="$(cast wallet address --private-key $test_account_key)"

        local token_balance
        token_balance=$(cast balance --rpc-url "$l2_rpc_url" "$test_account_addr" 2>/dev/null)
        if [ $? -ne 0 ]; then
            echo "⚠️ Failed to fetch token balance for $test_account_addr on $l2_rpc_url" >&2
            token_balance=0
        fi

        # Threshold: 0.1 ether in wei
        local threshold=100000000000000000

        # Only fund if balance is less than or equal to 0.1 ether
        if [[ $token_balance -le $threshold ]]; then
            local l2_coinbase_key="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
            local amount="1000ether"

            echo "💸 $test_account_addr L2 balance is low (≤ 0.1 ETH), funding with amount=$amount..." >&3
            fund "$l2_coinbase_key" "$test_account_addr" "$amount" "$l2_rpc_url"
            if [ $? -ne 0 ]; then
                echo "❌ Funding L2 receiver $test_account_addr failed" >&2
                return 1
            fi
            echo "✅ Successfully funded $test_account_addr with $amount on L2" >&3
        else
            echo "✅ Receiver $test_account_addr already has $(cast --from-wei "$token_balance") ETH on L2" >&3
        fi
    else
        echo "🚫 Skipping L2 funding since DISABLE_L2_FUND is set to true" >&3
    fi

    local combined_json_output=""

    if [[ -z "${L1_BRIDGE_ADDRESS}" || -z "${L2_BRIDGE_ADDRESS}" ]]; then
        local combined_json_file="/opt/zkevm/combined.json"
        echo "ℹ️ Some bridge addresses are missing, fetching from Kurtosis CDK artifact: $combined_json_file" >&3

        combined_json_output="$($contracts_service_wrapper "cat $combined_json_file" | tail -n +2)"
        if [ $? -ne 0 ]; then
            echo "❌ Failed to read $combined_json_file from Kurtosis CDK" >&2
            return 1
        fi
    fi

    if [[ -z "${L1_BRIDGE_ADDRESS}" ]]; then
        L1_BRIDGE_ADDRESS="$(echo "$combined_json_output" | jq -r .polygonZkEVMBridgeAddress)"
        if [ $? -ne 0 ]; then
            echo "❌ Failed to extract L1_BRIDGE_ADDRESS from $combined_json_file" >&2
            return 1
        fi
    fi

    if [[ -z "${L2_BRIDGE_ADDRESS}" ]]; then
        L2_BRIDGE_ADDRESS="$(echo "$combined_json_output" | jq -r .polygonZkEVML2BridgeAddress)"
        if [ $? -ne 0 ]; then
            echo "❌ Failed to extract L2_BRIDGE_ADDRESS from $combined_json_file" >&2
            return 1
        fi
    fi

    echo "✅ L1 bridge address=$L1_BRIDGE_ADDRESS; L2 bridge address=$L2_BRIDGE_ADDRESS" >&3
}

# Function is used to fund a receiver address with native tokens.
# It takes four arguments:
# 1. sender_private_key: The private key of the sender
# 2. receiver_addr: The address of the receiver
# 3. amount: The amount of native tokens to send (in wei)
# 4. rpc_url: The RPC URL of the Ethereum network
# The function will attempt to send the specified amount of native tokens to the receiver address.
# If the transaction fails, it will retry up to 3 times with a 3-second delay between attempts.
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

        local raw_gas_price
        raw_gas_price=$(cast gas-price --rpc-url "$rpc_url" 2>/dev/null)
        if [ $? -ne 0 ] || [ -z "$raw_gas_price" ]; then
            echo "❌ Failed to fetch gas price from $rpc_url (attempt $attempt)" >&2
            break
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
            "$receiver_addr" || {
            echo "⚠️ Attempt $attempt failed. Retrying in 3s..." >&2
            sleep 3
            attempt=$((attempt + 1))
            continue
        }

        success=1
        break
    done

    if [ $success -eq 0 ]; then
        echo "❌ Failed to fund $receiver_addr after $max_attempts attempts. Continuing..." >&2
        return 1
    fi

    echo "✅ Successfully funded $receiver_addr with $amount of native tokens" >&2
}
