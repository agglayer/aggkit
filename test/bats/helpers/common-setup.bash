#!/usr/bin/env bash

_common_setup() {
    bats_load_library 'bats-support' || return 1
    bats_load_library 'bats-assert' || return 1

    # Resolve project root
    PROJECT_ROOT="$(cd "$(dirname "$BATS_TEST_FILENAME")/.." >/dev/null 2>&1 && pwd)" || {
        echo "❌ Failed to determine PROJECT_ROOT" >&2
        return 1
    }

    PATH="$PROJECT_ROOT/src:$PATH"

    # ERC20 contracts function signatures
    readonly mint_fn_sig="function mint(address,uint256)"
    readonly balance_of_fn_sig="function balanceOf(address) (uint256)"
    readonly approve_fn_sig="function approve(address,uint256)"

    # Kurtosis enclave and service identifiers
    readonly enclave="${KURTOSIS_ENCLAVE:-aggkit}"
    readonly contracts_container="${KURTOSIS_CONTRACTS:-contracts-001}"
    readonly contracts_service_wrapper="${KURTOSIS_CONTRACTS_WRAPPER:-"kurtosis service exec $enclave $contracts_container"}"

    if [[ -z "${L2_ETH_RPC_URL:-}" ]]; then
        readonly l2_rpc_node="${L2_RPC_NODE:-cdk-erigon-rpc-001}"
        echo "ℹ️ L2_ETH_RPC_URL not provided, resolving from Kurtosis (L2_RPC_NODE: $l2_rpc_node)" >&3
        readonly l2_rpc_url="$(kurtosis port print "$enclave" "$l2_rpc_node" rpc)" || {
            echo "❌ Failed to resolve L2 RPC URL from Kurtosis" >&2
            return 1
        }
    else
        readonly l2_rpc_url="$L2_ETH_RPC_URL"
    fi

    local combined_json_output=""

    if [[ -z "${L1_BRIDGE_ADDRESS:-}" || -z "${L2_BRIDGE_ADDRESS:-}" ]]; then
        local combined_json_file="/opt/zkevm/combined.json"
        echo "ℹ️ Some bridge addresses are missing, fetching from CDK: $combined_json_file" >&3
        combined_json_output="$($contracts_service_wrapper "cat $combined_json_file" | tail -n +2)" || {
            echo "❌ Failed to read "$combined_json_file" from Kurtosis CDK" >&2
            return 1
        }
    fi

    if [[ -z "${L1_BRIDGE_ADDRESS:-}" ]]; then
        L1_BRIDGE_ADDRESS="$(echo "$combined_json_output" | jq -r .polygonZkEVMBridgeAddress)" || {
            echo "❌ Failed to extract L1_BRIDGE_ADDRESS from "$combined_json_file"" >&2
            return 1
        }
    fi

    if [[ -z "${L2_BRIDGE_ADDRESS:-}" ]]; then
        L2_BRIDGE_ADDRESS="$(echo "$combined_json_output" | jq -r .polygonZkEVML2BridgeAddress)" || {
            echo "❌ Failed to extract L2_BRIDGE_ADDRESS from "$combined_json_file"" >&2
            return 1
        }
    fi

    echo "✅ L1 bridge address=$L1_BRIDGE_ADDRESS; L2 bridge address=$L2_BRIDGE_ADDRESS" >&3
}
