#!/usr/bin/env bash

_common_setup() {
    bats_load_library 'bats-support'
    bats_load_library 'bats-assert'

    # get the containing directory of this file
    # use $BATS_TEST_FILENAME instead of ${BASH_SOURCE[0]} or $0,
    # as those will point to the bats executable's location or the preprocessed file respectively
    PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." >/dev/null 2>&1 && pwd)"
    # make executables in src/ visible to PATH
    PATH="$PROJECT_ROOT/src:$PATH"

    # ERC20 contracts function signatures
    readonly mint_fn_sig="function mint(address,uint256)"
    readonly balance_of_fn_sig="function balanceOf(address) (uint256)"
    readonly approve_fn_sig="function approve(address,uint256)"

    # Kurtosis enclave and service identifiers
    readonly enclave=${KURTOSIS_ENCLAVE:-aggkit}
    readonly contracts_container=${KURTOSIS_CONTRACTS:-contracts-001}
    readonly contracts_service_wrapper=${KURTOSIS_CONTRACTS_WRAPPER:-"kurtosis service exec $enclave $contracts_container"}
    if [[ -z "$L2_ETH_RPC_URL" ]]; then
        readonly l2_rpc_node=${L2_RPC_NODE:-cdk-erigon-rpc-001}
        echo "L2_ETH_RPC_URL not provided, resolving it from Kurtosis environment (L2_RPC_NODE: $l2_rpc_node)" >&3
        readonly l2_rpc_url="$(kurtosis port print $enclave $l2_rpc_node rpc)"
    else
        readonly l2_rpc_url="$L2_ETH_RPC_URL"
    fi

    if [ -z "$L1_BRIDGE_ADDRESS" ]; then
        local combined_json_file="/opt/zkevm/combined.json"
        echo "L1_BRIDGE_ADDRESS env variable is not provided, resolving the bridge address from the Kurtosis CDK '$combined_json_file'" >&3

        # Fetching the combined JSON output and filtering to get polygonZkEVMBridgeAddress
        combined_json_output=$($contracts_service_wrapper "cat $combined_json_file" | tail -n +2)
        L1_BRIDGE_ADDRESS=$(echo "$combined_json_output" | jq -r .polygonZkEVMBridgeAddress)
    fi

    if [ -z "$L2_BRIDGE_ADDRESS" ]; then
        local combined_json_file="/opt/zkevm/combined.json"
        echo "L2_BRIDGE_ADDRESS env variable is not provided, resolving the bridge address from the Kurtosis CDK '$combined_json_file'" >&3

        # Fetching the combined JSON output and filtering to get polygonZkEVML2BridgeAddress
        combined_json_output=$($contracts_service_wrapper "cat $combined_json_file" | tail -n +2)
        L2_BRIDGE_ADDRESS=$(echo "$combined_json_output" | jq -r .polygonZkEVML2BridgeAddress)
    fi

    echo "L1 bridge address=$L1_BRIDGE_ADDRESS; L2 bridge address=$L2_BRIDGE_ADDRESS" >&3
}
