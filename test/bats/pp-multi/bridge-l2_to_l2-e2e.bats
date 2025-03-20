# based on: https://github.com/0xPolygon/kurtosis-cdk/blob/jhilliard/multi-pp-testing/multi-pp-test.sh.md

setup() {
    load '../helpers/common-multi_cdk-setup'
    _common_multi_setup
    load '../helpers/common'
    load '../helpers/lxly-bridge'
    load '../helpers/aggsender'

    if [ ! -f $aggsender_find_imported_bridge ]; then
        echo "missing required tool: $aggsender_find_imported_bridge" >&3
        return 1
    fi

    add_network2_to_agglayer
    fund_claim_tx_manager
    mint_pol_token

    ether_value=${ETHER_VALUE:-"0.0200000054"}
    amount=$(cast to-wei $ether_value ether)
    native_token_addr="0x0000000000000000000000000000000000000000"
    readonly sender_private_key=${SENDER_PRIVATE_KEY:-"12d7de8621a77640c9241b2595ba78ce443d05e94090365ab3bb5e19df82c625"}
    readonly sender_addr="$(cast wallet address --private-key $sender_private_key)"
    # Params for lxly-bridge functions
    is_forced=${IS_FORCED:-"true"}
    bridge_addr=$bridge_address
    meta_bytes=${META_BYTES:-"0x1234"}
    destination_addr=$target_address
    timeout="600"
    claim_frequency="30"

    gas_price=$(cast gas-price --rpc-url "$l2_rpc_url")
}

@test "Test L2 to L2 bridge" {
    echo "=== Running LxLy bridge eth L1 to L2(PP1) amount:$amount" >&3
    destination_net=$l2_pp1b_network_id
    run bridge_asset "$native_token_addr" "$l1_rpc_url"
    assert_success
    local bridge_tx_hash_pp1=$output

    echo "=== Running LxLy bridge eth L1 to L2(PP2) amount:$amount" >&3
    destination_net=$l2_pp2b_network_id
    run bridge_asset "$native_token_addr" "$l1_rpc_url"
    assert_success
    local bridge_tx_hash_pp2=$output

    echo "=== Running LxLy claim L1 to L2(PP1) for $bridge_tx_hash_pp1" >&3
    run get_bridge "$l1_rpc_network_id" "$bridge_tx_hash_pp1" 10 3 "$aggkit_pp1_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l1_rpc_network_id" "$deposit_count" 10 3 "$aggkit_pp1_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l2_pp1b_network_id" "$l1_info_tree_index" 10 30 "$aggkit_pp1_node_url"
    assert_success
    local injected_info="$output"
    local l1_info_tree_index=$(echo "$injected_info" | jq -r '.l1_info_tree_index')
    run find_claim_proof "$l1_rpc_network_id" "$deposit_count" "$l1_info_tree_index" 10 3 "$aggkit_pp1_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l2_pp1_url" 10 3
    assert_success

    echo "=== Running LxLy claim L1 to L2(PP2) for $bridge_tx_hash_pp2" >&3
    run get_bridge "$l1_rpc_network_id" "$bridge_tx_hash_pp2" 10 3 "$aggkit_pp2_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l1_rpc_network_id" "$deposit_count" 10 3 "$aggkit_pp2_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l2_pp2b_network_id" "$l1_info_tree_index" 10 30 "$aggkit_pp2_node_url"
    assert_success
    local injected_info="$output"
    local l1_info_tree_index=$(echo "$injected_info" | jq -r '.l1_info_tree_index')
    run find_claim_proof "$l1_rpc_network_id" "$deposit_count" "$l1_info_tree_index" 10 3 "$aggkit_pp2_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l2_pp2_url" 10 3
    assert_success

    # reduce eth amount
    amount=1234567
    echo "=== Running LxLy bridge L2(PP2) to L2(PP1) amount:$amount" >&3
    destination_net=$l2_pp1b_network_id
    meta_bytes="0xbeef"
    run bridge_asset "$native_token_addr" "$l2_pp2_url"
    assert_success
    local bridge_tx_hash=$output

    echo "=== Running LxLy claim L2(PP2) to L2(PP1) for: $bridge_tx_hash" >&3
    run get_bridge "$l2_pp2b_network_id" "$bridge_tx_hash" 10 3 "$aggkit_pp2_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l2_pp2b_network_id" "$deposit_count" 10 3 "$aggkit_pp2_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l2_pp1b_network_id" "$l1_info_tree_index" 10 30 "$aggkit_pp1_node_url"
    assert_success
    local injected_info="$output"
    local l1_info_tree_index=$(echo "$injected_info" | jq -r '.l1_info_tree_index')
    run find_claim_proof "$l2_pp2b_network_id" "$deposit_count" "$l1_info_tree_index" 10 3 "$aggkit_pp2_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l2_pp1_url" 10 3
    assert_success
    local global_index_pp2_to_pp1="$output"

    # Now we need to do a bridge on L2(PP1) to trigger a certificate to be sent to L1
    ether_value=${ETHER_VALUE:-"0.0100000054"}
    amount=$(cast to-wei $ether_value ether)
    echo "=== Running LxLy bridge eth L2(PP1) to L1 (trigger certificate sending on PP1) amount:$amount" >&3
    destination_net=$l1_rpc_network_id
    meta_bytes="0xabcd"
    run bridge_asset "$native_token_addr" "$l2_pp1_url"
    assert_success
    bridge_tx_hash=$output

    echo "=== Running LxLy claim L2(PP1) to L1 for $bridge_tx_hash" >&3
    run get_bridge "$l2_pp1b_network_id" "$bridge_tx_hash" 10 3 "$aggkit_pp1_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l2_pp1b_network_id" "$deposit_count" 10 3 "$aggkit_pp1_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l1_rpc_network_id" "$l1_info_tree_index" 10 30 "$aggkit_pp1_node_url"
    assert_success
    local injected_info="$output"
    local l1_info_tree_index=$(echo "$injected_info" | jq -r '.l1_info_tree_index')
    run find_claim_proof "$l2_pp1b_network_id" "$deposit_count" "$l1_info_tree_index" 10 3 "$aggkit_pp1_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l1_rpc_url" 10 3
    assert_success

    echo "=== Waiting to settled certificate with imported bridge for global_index: $global_index_pp2_to_pp1"
    wait_to_settled_certificate_containing_global_index $aggkit_pp1_node_url $global_index_pp2_to_pp1
}
