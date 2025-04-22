setup() {
    load '../helpers/common-setup'
    load '../helpers/common'
    load '../helpers/lxly-bridge'

    _common_setup

    readonly sender_private_key=${SENDER_PRIVATE_KEY:-"12d7de8621a77640c9241b2595ba78ce443d05e94090365ab3bb5e19df82c625"}
    readonly sender_addr="$(cast wallet address --private-key $sender_private_key)"
    destination_net=${DESTINATION_NET:-"1"}
    destination_addr=${DESTINATION_ADDRESS:-"0x0bb7AA0b4FdC2D2862c088424260e99ed6299148"}
    ether_value=${ETHER_VALUE:-"0.0200000054"}
    amount=$(cast to-wei $ether_value ether)
    readonly native_token_addr=${NATIVE_TOKEN_ADDRESS:-"0x0000000000000000000000000000000000000000"}
    
    readonly is_forced=${IS_FORCED:-"true"}
    readonly l1_bridge_addr=$L1_BRIDGE_ADDRESS
    readonly l2_bridge_addr=$L2_BRIDGE_ADDRESS
    readonly meta_bytes=${META_BYTES:-"0x1234"}

    readonly l1_rpc_url=${L1_ETH_RPC_URL:-"$(kurtosis port print $enclave el-1-geth-lighthouse rpc)"}
    readonly bridge_api_url=${BRIDGE_API_URL:-"$(kurtosis port print $enclave zkevm-bridge-service-001 rpc)"}

    readonly dry_run=${DRY_RUN:-"false"}
    readonly l1_rpc_network_id=$(cast call --rpc-url $l1_rpc_url $l1_bridge_addr 'networkID() (uint32)')
    readonly l2_rpc_network_id=$(cast call --rpc-url $l2_rpc_url $l2_bridge_addr 'networkID() (uint32)')
    gas_price=$(cast gas-price --rpc-url "$l2_rpc_url")
    readonly weth_token_addr=$(cast call --rpc-url $l2_rpc_url $l2_bridge_addr 'WETHToken() (address)')
}

@test "Native gas token deposit to WETH" {
    destination_addr=$sender_addr
    local initial_receiver_balance=$(cast call --rpc-url "$l2_rpc_url" "$weth_token_addr" "$balance_of_fn_sig" "$destination_addr" | awk '{print $1}')
    echo "Initial receiver balance of native token on L2 $initial_receiver_balance" >&3

    echo "=== Running LxLy deposit on L1 to network: $l2_rpc_network_id native_token: $native_token_addr" >&3
    destination_net=$l2_rpc_network_id
    run bridge_asset "$native_token_addr" "$l1_rpc_url" "$l1_bridge_addr"
    assert_success
    local bridge_tx_hash=$output

    echo "=== Running LxLy claim on L2" >&3
    timeout="120"
    claim_frequency="10"
    run claim_tx_hash "$timeout" "$bridge_tx_hash" "$destination_addr" "$l2_rpc_url" "$bridge_api_url" "$l2_bridge_addr"
    assert_success

    echo "=== Running LxLy WETH ($weth_token_addr) deposit on L2 to L1 network" >&3
    destination_addr=$sender_addr
    destination_net=0
    run bridge_asset "$weth_token_addr" "$l2_rpc_url" "$l2_bridge_addr"
    assert_success
}
