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
            echo "🔍 Trying L2 RPC node: $node" >&3
            resolved_url=$(get_l2_rpc_url "$enclave" "$node")
            if [ $? -ne 0 ]; then
                echo "⚠️ Failed to resolve the L2 RPC URL from $node, trying next one..." >&3
                continue
            fi

            if [ -n "$resolved_url" ]; then
                echo "✅ Successfully resolved L2 RPC URL from $node" >&3
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

    if [[ "${FUND_L2}" == "true" ]]; then
        echo "💸 FUND_L2 is enabled, invoking fund..." >&3
        local l2_coinbase_key="ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80"
        local receiver="0xE34aaF64b29273B7D567FCFc40544c014EEe9970"
        local amount="1000ether"

        fund "$l2_coinbase_key" "$receiver" "$amount" "$l2_rpc_url"
        if [ $? -ne 0 ]; then
            echo "❌ Funding L2 receiver "$receiver" failed" >&2
            return 1
        fi
    fi

    local combined_json_output=""

    if [[ -z "${L1_BRIDGE_ADDRESS}" || -z "${L2_BRIDGE_ADDRESS}" ]]; then
        local combined_json_file="/opt/zkevm/combined.json"
        echo "ℹ️ Some bridge addresses are missing, fetching from CDK: $combined_json_file" >&3

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
# 2. l2_rpc_node: The name of the L2 RPC node
function get_l2_rpc_url() {
    local enclave=$1
    local l2_rpc_node=${2}

    if [[ -z "$enclave" ]]; then
        echo "❌ Missing enclave name. Usage: get_l2_rpc_url <enclave> <l2_rpc_node>" >&2
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
