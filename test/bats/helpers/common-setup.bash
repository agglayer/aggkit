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
        echo "L2_ETH_RPC_URL not provided ($L2_ETH_RPC_URL)" >&3
        readonly l2_rpc_node=${L2_RPC_NODE:-cdk-erigon-rpc-001}
        readonly l2_rpc_url="$(kurtosis port print $enclave $l2_rpc_node rpc)"
    else
        readonly l2_rpc_url="$L2_ETH_RPC_URL"
        echo "L2 RPC URL: $l2_rpc_url" >&3
    fi
}
