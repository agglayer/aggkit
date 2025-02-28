setup() {
    load '../helpers/common-setup'
    load '../helpers/common'
    load '../helpers/lxly-bridge'

    _common_setup

    if [ -z "$BRIDGE_ADDRESS" ]; then
        local combined_json_file="/opt/zkevm/combined.json"
        log "BRIDGE_ADDRESS env variable is not provided, resolving the bridge address from the Kurtosis CDK '$combined_json_file'"

        # Fetching the combined JSON output and filtering to get polygonZkEVMBridgeAddress
        combined_json_output=$($contracts_service_wrapper "cat $combined_json_file" | tail -n +2)
        bridge_default_address=$(echo "$combined_json_output" | jq -r .polygonZkEVMBridgeAddress)
        BRIDGE_ADDRESS=$bridge_default_address
    fi
    log "🌉 Bridge address=$BRIDGE_ADDRESS"

    readonly sender_private_key=${SENDER_PRIVATE_KEY:-"12d7de8621a77640c9241b2595ba78ce443d05e94090365ab3bb5e19df82c625"}
    readonly sender_addr="$(cast wallet address --private-key $sender_private_key)"
    destination_net=${DESTINATION_NET:-"1"}
    destination_addr=${DESTINATION_ADDRESS:-"0x0bb7AA0b4FdC2D2862c088424260e99ed6299148"}
    ether_value=${ETHER_VALUE:-"0.0200000054"}
    amount=$(cast to-wei $ether_value ether)
    readonly native_token_addr=${NATIVE_TOKEN_ADDRESS:-"0x0000000000000000000000000000000000000000"}
    if [[ -n "$GAS_TOKEN_ADDR" ]]; then
        log "🪙 Using provided GAS_TOKEN_ADDR: $GAS_TOKEN_ADDR"
        gas_token_addr="$GAS_TOKEN_ADDR"
    else
        log "🪙 GAS_TOKEN_ADDR not provided, retrieving from rollup parameters file."
        readonly rollup_params_file=/opt/zkevm/create_rollup_parameters.json
        run bash -c "$contracts_service_wrapper 'cat $rollup_params_file' | tail -n +2 | jq -r '.gasTokenAddress'"
        assert_success
        assert_output --regexp "0x[a-fA-F0-9]{40}"
        gas_token_addr=$output
    fi
    readonly is_forced=${IS_FORCED:-"true"}
    readonly bridge_addr=$BRIDGE_ADDRESS
    meta_bytes=${META_BYTES:-"0x1234"}

    readonly l1_rpc_url=${L1_ETH_RPC_URL:-"$(kurtosis port print $enclave el-1-geth-lighthouse rpc)"}
    readonly aggkit_node_url=${AGGKIT_NODE_URL:-"$(kurtosis port print $enclave cdk-node-001 rpc)"}

    readonly dry_run=${DRY_RUN:-"false"}
    readonly l1_rpc_network_id=$(cast call --rpc-url $l1_rpc_url $bridge_addr 'networkID() (uint32)')
    readonly l2_rpc_network_id=$(cast call --rpc-url $l2_rpc_url $bridge_addr 'networkID() (uint32)')
    gas_price=$(cast gas-price --rpc-url "$l2_rpc_url")

    readonly erc20_artifact_path=${ERC20_ARTIFACT_PATH:-"./contracts/erc20mock/ERC20Mock.json"}
}

@test "Native gas token bridge L1 -> L2" {
    run cast call --rpc-url $l2_rpc_url $bridge_addr 'WETHToken() (address)'
    assert_success
    readonly weth_token_addr=$output

    destination_addr=$sender_addr
    local initial_receiver_balance=$(cast call --rpc-url "$l2_rpc_url" "$weth_token_addr" "$balance_of_fn_sig" "$destination_addr" | awk '{print $1}')
    log "💰 Initial receiver balance of native token on L2 $initial_receiver_balance"

    echo "=== Running LxLy deposit on L1 to network: $l2_rpc_network_id native_token: $native_token_addr" >&3
    destination_net=$l2_rpc_network_id
    run bridge_asset "$native_token_addr" "$l1_rpc_url"
    assert_success
    local bridge_tx_hash=$output

    run get_bridge "$l1_rpc_network_id" "$bridge_tx_hash" 10 3 "$aggkit_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l1_rpc_network_id" "$deposit_count" 10 5 "$aggkit_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l2_rpc_network_id" "$l1_info_tree_index" 10 20 "$aggkit_node_url"
    assert_success
    run generate_claim_proof "$l1_rpc_network_id" "$deposit_count" "$l1_info_tree_index" 10 5 "$aggkit_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l2_rpc_url" 10 10 "$l1_rpc_network_id"
    assert_success

    echo "=== Running LxLy WETH ($weth_token_addr) deposit on L2 to L1 network" >&3
    destination_addr=$sender_addr
    destination_net=0
    run bridge_asset "$weth_token_addr" "$l2_rpc_url"
    assert_success
    local bridge_tx_hash=$output
}

@test "Custom gas token deposit L1 -> L2" {
    echo "🪙 Custom gas token deposit (gas token addr: $gas_token_addr, L1 RPC: $l1_rpc_url, L2 RPC: $l2_rpc_url)"

    # SETUP
    # Set receiver address and query for its initial native token balance on the L2
    receiver=${RECEIVER:-"0x85dA99c8a7C2C95964c8EfD687E95E632Fc533D6"}
    local initial_receiver_balance=$(cast balance "$receiver" --rpc-url "$l2_rpc_url")
    log "💰 Initial receiver ($receiver) balance of native token on L2 $initial_receiver_balance"

    local l1_minter_balance=$(cast balance "0x8943545177806ED17B9F23F0a21ee5948eCaa776" --rpc-url "$l1_rpc_url")
    log "💰 Initial minter balance on L1 $l1_minter_balance"

    # Query for initial sender balance
    run query_contract "$l1_rpc_url" "$gas_token_addr" "$balance_of_fn_sig" "$sender_addr"
    assert_success
    local gas_token_init_sender_balance=$(echo "$output" | tail -n 1 | awk '{print $1}')
    log "💰 Initial sender balance "$gas_token_init_sender_balance" of gas token on L1"

    # Mint gas token on L1
    local tokens_amount="0.1ether"
    local wei_amount=$(cast --to-unit $tokens_amount wei)
    local minter_key=${MINTER_KEY:-"bcdf20249abf0ed6d944c0288fad489e33f66b3960d9e6229c1cd214ed3bbe31"}
    run mint_erc20_tokens "$l1_rpc_url" "$gas_token_addr" "$minter_key" "$sender_addr" "$tokens_amount"
    assert_success

    # Assert that balance of gas token (on the L1) is correct
    run query_contract "$l1_rpc_url" "$gas_token_addr" "$balance_of_fn_sig" "$sender_addr"
    assert_success
    local gas_token_final_sender_balance=$(echo "$output" |
        tail -n 1 |
        awk '{print $1}')
    local expected_balance=$(echo "$gas_token_init_sender_balance + $wei_amount" |
        bc |
        awk '{print $1}')

    log "💰 Sender balance ($sender_addr) (gas token L1): $gas_token_final_sender_balance"
    assert_equal "$gas_token_final_sender_balance" "$expected_balance"

    # Send approve transaction to the gas token on L1
    deposit_ether_value="0.1ether"
    run send_tx "$l1_rpc_url" "$sender_private_key" "$gas_token_addr" "$approve_fn_sig" "$bridge_addr" "$deposit_ether_value"
    assert_success
    assert_output --regexp "Transaction successful \(transaction hash: 0x[a-fA-F0-9]{64}\)"

    # Deposit token (on L1)
    destination_addr=$receiver
    destination_net=$l2_rpc_network_id
    amount=$wei_amount
    meta_bytes="0x"
    run bridge_asset "$gas_token_addr" "$l1_rpc_url"
    assert_success
    local bridge_tx_hash=$output

    # Claim deposits (settle them on the L2)
    run get_bridge "$l1_rpc_network_id" "$bridge_tx_hash" 10 3 "$aggkit_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l1_rpc_network_id" "$deposit_count" 10 5 "$aggkit_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l2_rpc_network_id" "$l1_info_tree_index" 10 20 "$aggkit_node_url"
    assert_success
    run generate_claim_proof "$l1_rpc_network_id" "$deposit_count" "$l1_info_tree_index" 10 5 "$aggkit_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l2_rpc_url" 10 20 "$l1_rpc_network_id"
    assert_success
    local claim_global_index="$output"

    # Validate the bridge_getClaims API
    echo "------- bridge_getClaims API testcase --------"
    run get_claim "$l2_rpc_network_id" "$claim_global_index" 10 3 "$aggkit_node_url"
    assert_success

    local origin_network="$(echo "$output" | jq -r '.origin_network')"
    local destination_network="$(echo "$output" | jq -r '.destination_network')"
    assert_equal "$l1_rpc_network_id" "$origin_network"
    assert_equal "$l2_rpc_network_id" "$destination_network"
    echo "🚀🚀 bridge_getClaims API testcase passed" >&3

    # Validate that the native token of receiver on L2 has increased by the bridge tokens amount
    run verify_balance "$l2_rpc_url" "$native_token_addr" "$receiver" "$initial_receiver_balance" "$tokens_amount"
    assert_success
}

@test "Custom gas token deposit L2 -> L1" {
    log "🪙 Custom gas token deposit (gas token addr: $gas_token_addr, L1 RPC: $l1_rpc_url, L2 RPC: $l2_rpc_url)"

    local initial_receiver_balance=$(cast call --rpc-url "$l1_rpc_url" "$gas_token_addr" "$balance_of_fn_sig" "$destination_addr" | awk '{print $1}')
    assert_success
    log "💰 Receiver balance of gas token on L1 $initial_receiver_balance"

    # Deposit token (on L2)
    destination_net=$l1_rpc_network_id
    run bridge_asset "$native_token_addr" "$l2_rpc_url"
    assert_success
    local bridge_tx_hash=$output

    # Claim withdrawals (settle them on the L1)
    run get_bridge "$l2_rpc_network_id" "$bridge_tx_hash" 10 3 "$aggkit_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l2_rpc_network_id" "$deposit_count" 10 5 "$aggkit_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l1_rpc_network_id" "$l1_info_tree_index" 10 20 "$aggkit_node_url"
    assert_success
    run generate_claim_proof "$l2_rpc_network_id" "$deposit_count" "$l1_info_tree_index" 10 5 "$aggkit_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l1_rpc_url" 10 10 "$l2_rpc_network_id"
    assert_success

    # Validate that the token of receiver on L1 has increased by the bridge tokens amount
    run verify_balance "$l1_rpc_url" "$gas_token_addr" "$destination_addr" "$initial_receiver_balance" "$ether_value"
    if [ $status -eq 0 ]; then
        break
    fi
    assert_success
}

@test "ERC20 token deposit L1 -> L2" {
    log "📜 Retrieving ERC20 contract artifact from $erc20_artifact_path"

    run jq -r '.bytecode' "$erc20_artifact_path"
    assert_success
    local erc20_bytecode="$output"

    run cast send --rpc-url "$l1_rpc_url" --private-key "$sender_private_key" --legacy --create "$erc20_bytecode"
    assert_success
    local erc20_deploy_output=$output
    echo "Contract deployment $erc20_deploy_output"

    local l1_erc20_addr=$(echo "$erc20_deploy_output" |
        grep 'contractAddress' |
        awk '{print $2}' |
        tr '[:upper:]' '[:lower:]')
    log "📜 ERC20 contract address: $l1_erc20_addr"

    # Mint gas token on L1
    local tokens_amount="0.1ether"
    local wei_amount=$(cast --to-unit $tokens_amount wei)
    run mint_erc20_tokens "$l1_rpc_url" "$l1_erc20_addr" "$sender_private_key" "$sender_addr" "$tokens_amount"
    assert_success

    # Assert that balance of gas token (on the L1) is correct
    run query_contract "$l1_rpc_url" "$l1_erc20_addr" "$balance_of_fn_sig" "$sender_addr"
    assert_success
    local l1_erc20_token_sender_balance=$(echo "$output" |
        tail -n 1 |
        awk '{print $1}')
    log "💰 Sender balance ($sender_addr) (ERC20 token L1): $l1_erc20_token_sender_balance [weis]"

    # Send approve transaction to the gas token on L1
    run send_tx "$l1_rpc_url" "$sender_private_key" "$l1_erc20_addr" "$approve_fn_sig" "$bridge_addr" "$tokens_amount"
    assert_success
    assert_output --regexp "Transaction successful \(transaction hash: 0x[a-fA-F0-9]{64}\)"

    # Deposit on L1
    local receiver=${RECEIVER:-"0x85dA99c8a7C2C95964c8EfD687E95E632Fc533D6"}
    destination_addr=$receiver
    destination_net=$l2_rpc_network_id
    amount=$(cast --to-unit $tokens_amount wei)
    meta_bytes="0x"
    run bridge_asset "$l1_erc20_addr" "$l1_rpc_url"
    assert_success
    local bridge_tx_hash=$output

    # Claim deposits (settle them on the L2)
    run get_bridge "$l1_rpc_network_id" "$bridge_tx_hash" 10 3 "$aggkit_node_url"
    assert_success
    local bridge="$output"
    local deposit_count="$(echo "$bridge" | jq -r '.deposit_count')"
    run find_l1_info_tree_index_for_bridge "$l1_rpc_network_id" "$deposit_count" 10 5 "$aggkit_node_url"
    assert_success
    local l1_info_tree_index="$output"
    run find_injected_info_after_index "$l2_rpc_network_id" "$l1_info_tree_index" 10 20 "$aggkit_node_url"
    assert_success
    run generate_claim_proof "$l1_rpc_network_id" "$deposit_count" "$l1_info_tree_index" 10 5 "$aggkit_node_url"
    assert_success
    local proof="$output"
    run claim_bridge "$bridge" "$proof" "$l2_rpc_url" 10 10 "$l1_rpc_network_id"
    assert_success

    run wait_for_expected_token "$aggkit_node_url" "$l1_erc20_addr" 30 2 "$l2_rpc_network_id"
    assert_success
    local token_mappings_result=$output

    local origin_token_addr=$(echo "$token_mappings_result" | jq -r '.tokenMappings[0].origin_token_address')
    assert_equal "$l1_erc20_addr" "$origin_token_addr"

    local l2_wrapped_token=$(echo "$token_mappings_result" | jq -r '.tokenMappings[0].wrapped_token_address')
    log "🪙 L2 wrapped token address $l2_wrapped_token"

    run verify_balance "$l2_rpc_url" "$l2_wrapped_token" "$receiver" 0 "$tokens_amount"
    assert_success
}

@test "ERC20 token deposit L2 -> L1" {
    log "Retrieving ERC20 contract artifact from $erc20_artifact_path"

    run jq -r '.bytecode' "$erc20_artifact_path"
    assert_success
    local erc20_bytecode="$output"

    echo "🚀 Deploying ERC20 contract on L2 ($l2_rpc_url)"
    run cast send --rpc-url "$l2_rpc_url" --private-key "$sender_private_key" --legacy --create "$erc20_bytecode"
    assert_success
    local erc20_deploy_output=$output

    local l2_erc20_addr=$(echo "$erc20_deploy_output" |
        grep 'contractAddress' |
        awk '{print $2}' |
        tr '[:upper:]' '[:lower:]')
    log "📜 ERC20 contract address: $l2_erc20_addr"

    # Mint ERC20 tokens on L2
    local tokens_amount="10ether"
    local wei_amount=$(cast --to-unit $tokens_amount wei)
    run mint_erc20_tokens "$l2_rpc_url" "$l2_erc20_addr" "$sender_private_key" "$sender_addr" "$tokens_amount"
    assert_success

    # Assert that balance of ERC20 token (on the L2) is correct
    run query_contract "$l2_rpc_url" "$l2_erc20_addr" "$balance_of_fn_sig" "$sender_addr"
    assert_success
    local l2_erc20_token_sender_balance=$(echo "$output" |
        tail -n 1 |
        awk '{print $1}')
    log "💰 Sender balance ($sender_addr) (ERC20 token L2): $l2_erc20_token_sender_balance [weis]"

    # Send approve transaction to the ERC20 token on L2
    run send_tx "$l2_rpc_url" "$sender_private_key" "$l2_erc20_addr" "$approve_fn_sig" "$bridge_addr" "$tokens_amount"
    assert_success
    assert_output --regexp "Transaction successful \(transaction hash: 0x[a-fA-F0-9]{64}\)"
    run query_contract "$l2_rpc_url" "$l2_erc20_addr" "allowance(address owner, address spender)(uint256)" "$sender_addr" "$bridge_addr"
    assert_success
    log "🔐 Allowance for bridge contract: $output [weis]"

    # Deposit on L2
    echo "==== 🚀 Depositing ERC20 token on L2 ($l2_rpc_url)" >&3
    destination_addr=$sender_addr
    destination_net=$l1_rpc_network_id
    tokens_amount="1ether"
    amount=$(cast --to-unit $tokens_amount wei)
    meta_bytes="0x"
    run bridge_asset "$l2_erc20_addr" "$l2_rpc_url"
    assert_success
    local bridge_tx_hash=$output

    # Query balance of ERC20 token on L2
    run query_contract "$l2_rpc_url" "$l2_erc20_addr" "$balance_of_fn_sig" "$sender_addr"
    assert_success
    local l2_erc20_token_sender_balance=$(echo "$output" |
        tail -n 1 |
        awk '{print $1}')
    log "💰 Sender balance ($sender_addr) (ERC20 token L2): $l2_erc20_token_sender_balance [weis]"

    # Claim deposit (settle it on the L1)
    echo "==== 🔐 Claiming ERC20 token deposit on L1 ($l1_rpc_url)" >&3
    timeout="180"
    claim_frequency="10"
    run claim_tx_hash "$timeout" "$bridge_tx_hash" "$destination_addr" "$l1_rpc_url" "$bridge_api_url"
    assert_success

    run wait_for_expected_token "$aggkit_node_url" "$l2_erc20_addr" 15 2 "$l1_rpc_network_id"
    assert_success
    local token_mappings_result=$output

    local origin_token_addr=$(echo "$token_mappings_result" | jq -r '.tokenMappings[0].origin_token_address')
    assert_equal "$l2_erc20_addr" "$origin_token_addr"

    local l1_wrapped_token_addr=$(echo "$token_mappings_result" | jq -r '.tokenMappings[0].wrapped_token_address')
    log "🪙 L1 wrapped token address $l1_wrapped_token_addr"

    run verify_balance "$l1_rpc_url" "$l1_wrapped_token_addr" "$destination_addr" 0 "$tokens_amount"
    assert_success

    # Send approve transaction to the ERC20 token on L1
    tokens_amount="1ether"
    run send_tx "$l1_rpc_url" "$sender_private_key" "$l1_wrapped_token_addr" "$approve_fn_sig" "$bridge_addr" "$tokens_amount"
    assert_success
    assert_output --regexp "Transaction successful \(transaction hash: 0x[a-fA-F0-9]{64}\)"

    # Deposit the L1 wrapped token (bridge L1 -> L2)
    echo "==== 🚀 Depositing L1 wrapped token on L1 ($l1_rpc_url)" >&3
    destination_addr=$sender_addr
    destination_net=$l2_rpc_network_id
    amount=$(cast --to-unit $tokens_amount wei)
    meta_bytes="0x"
    run bridge_asset "$l1_wrapped_token_addr" "$l1_rpc_url"
    assert_success
    bridge_tx_hash=$output

    # Claim deposit (settle it on the L2)
    echo "==== 🔐 Claiming deposit on L2 ($l2_rpc_url)" >&3
    timeout="180"
    claim_frequency="10"
    run claim_tx_hash "$timeout" "$bridge_tx_hash" "$destination_addr" "$l2_rpc_url" "$bridge_api_url"
    assert_success

    echo "==== 💰 Verifying balance on L2 ($l2_rpc_url)" >&3
    run verify_balance "$l2_rpc_url" "$l2_erc20_addr" "$destination_addr" "$l2_erc20_token_sender_balance" "$tokens_amount"
    assert_success

    # Query balance of ERC20 token on L1
    run query_contract "$l1_rpc_url" "$l1_wrapped_token_addr" "$balance_of_fn_sig" "$sender_addr"
    assert_success
    local l1_wrapped_token_balance=$(echo "$output" |
        tail -n 1 |
        awk '{print $1}')
    log "💰 Sender balance ($sender_addr) (wrapped ERC20 token L1): $l1_wrapped_token_balance [weis]"

    # Deposit on L2
    echo "==== 🚀 Depositing ERC20 token on L2 ($l2_rpc_url)" >&3
    destination_addr=$sender_addr
    destination_net=$l1_rpc_network_id
    tokens_amount="1ether"
    amount=$(cast --to-unit $tokens_amount wei)
    meta_bytes="0x"
    run bridge_asset "$l2_erc20_addr" "$l2_rpc_url"
    assert_success
    bridge_tx_hash=$output

    # Claim deposit (settle it on the L1)
    echo "==== 🔐 Claiming ERC20 token deposit on L1 ($l1_rpc_url)" >&3
    timeout="180"
    claim_frequency="10"
    run claim_tx_hash "$timeout" "$bridge_tx_hash" "$destination_addr" "$l1_rpc_url" "$bridge_api_url"
    assert_success

     echo "==== 💰 Verifying balance on L1 ($l1_rpc_url)" >&3
    run verify_balance "$l1_rpc_url" "$l1_wrapped_token_addr" "$destination_addr" "$l1_wrapped_token_balance" "$tokens_amount"
    assert_success
}
