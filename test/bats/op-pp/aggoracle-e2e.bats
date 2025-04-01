setup() {
    load '../helpers/common-setup'
    _common_setup

    load '../helpers/common'
    load '../helpers/lxly-bridge'

    if [ -z "$BRIDGE_ADDRESS" ]; then
        local combined_json_file="/opt/zkevm/combined.json"
        echo "BRIDGE_ADDRESS env variable is not provided, resolving the bridge address from the Kurtosis CDK '$combined_json_file'" >&3

        # Fetching the combined JSON output and filtering to get polygonZkEVMBridgeAddress
        combined_json_output=$($contracts_service_wrapper "cat $combined_json_file" | tail -n +2)
        bridge_default_address=$(echo "$combined_json_output" | jq -r .polygonZkEVMBridgeAddress)
        BRIDGE_ADDRESS=$bridge_default_address
    fi
    echo "Bridge address=$BRIDGE_ADDRESS" >&3

    ether_value=${ETHER_VALUE:-"0.0200000054"}
    amount=$(cast to-wei $ether_value ether)
    readonly native_token_addr=${NATIVE_TOKEN_ADDRESS:-"0x0000000000000000000000000000000000000000"}
    
    readonly sender_private_key=${SENDER_PRIVATE_KEY:-"12d7de8621a77640c9241b2595ba78ce443d05e94090365ab3bb5e19df82c625"}
    readonly sender_addr="$(cast wallet address --private-key $sender_private_key)"

    destination_net=${DESTINATION_NET:-"2"}
    destination_addr=${DESTINATION_ADDRESS:-"0x0bb7AA0b4FdC2D2862c088424260e99ed6299148"}
    
    # Params for lxly-bridge functions
    readonly is_forced=${IS_FORCED:-"true"}
    readonly bridge_addr=$BRIDGE_ADDRESS
    meta_bytes=${META_BYTES:-"0x1234"}

    readonly l1_rpc_url=${L1_ETH_RPC_URL:-"$(kurtosis port print $enclave el-1-geth-lighthouse rpc)"}
    readonly bridge_api_url=${BRIDGE_API_URL:-"$(kurtosis port print $enclave sovereign-bridge-service-001 rpc)"}
    readonly aggkit_node_url=${AGGKIT_NODE_URL:-"$(kurtosis port print $enclave aggkit-001 rpc)"}
    readonly op_rpc_url=$(kurtosis port print $enclave op-el-1-op-geth-op-node-001 rpc)

    gas_price=$(cast gas-price --rpc-url "$op_rpc_url")

    readonly dry_run=${DRY_RUN:-"false"}
    readonly l1_rpc_network_id=$(cast call --rpc-url $l1_rpc_url $bridge_addr 'networkID() (uint32)')
    readonly op_rpc_network_id=$(cast call --rpc-url $op_rpc_url $bridge_addr 'networkID() (uint32)')
}

@test "Test L1 to op-stack-L2 bridge" {
    echo "=== Running L1 to op-stack-L2 chain" >&3
    destination_addr=$sender_addr

    run bridge_asset "$native_token_addr" "$l1_rpc_url"
    assert_success
    local bridge_tx_hash=$output
    
    log "Waiting GER to be injected to the destination network..."
    sleep 60

    timeout="180"
    claim_frequency="10"
    run claim_bridge_by_tx_hash "$timeout" "$bridge_tx_hash" "$destination_addr" "$op_rpc_url" "$bridge_api_url"
    assert_success

    local balance=$(cast balance --ether --rpc-url $op_rpc_url $destination_addr)
    log "Balance of $destination_addr: $balance"
    
    if (( $(echo "$balance > 0" | bc -l) )); then
        assert_success
    else
        log "Error: Balance is not greater than 0"
        exit 1
    fi
}

