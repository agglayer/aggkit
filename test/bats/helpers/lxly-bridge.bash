#!/usr/bin/env bash

load '../helpers/common'

# Error code reference https://hackmd.io/WwahVBZERJKdfK3BbKxzQQ
function bridge_message() {
    local token_addr="$1"
    local rpc_url="$2"
    local bridge_sig='bridgeMessage(uint32,address,bool,bytes)'

    if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
        local eth_balance=$(cast balance -e --rpc-url "$rpc_url" "$sender_addr")
        log "💰 $sender_addr ETH Balance: $eth_balance wei"
    else
        local balance_wei=$(cast call --rpc-url "$rpc_url" "$token_addr" "$balance_of_fn_sig" "$sender_addr" | awk '{print $1}')
        local token_balance=$(cast --from-wei "$balance_wei")
        log "💎 $sender_addr token ($token_addr) balance: $token_balance [eth]"
    fi

    log "🚀 Bridge message $amount wei → $destination_addr [network: $destination_net, token: $token_addr, rpc: $rpc_url]"

    if [[ $dry_run == "true" ]]; then
        log "📝 Dry run bridge message (showing calldata only)"
        cast calldata "$bridge_sig" "$destination_net" \
            "$destination_addr" "$is_forced" "$meta_bytes"
    else
        local response
        if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
            response=$(cast send --legacy --private-key "$sender_private_key" --value "$amount" \
                --rpc-url "$rpc_url" "$bridge_addr" "$bridge_sig" "$destination_net" \
                "$destination_addr" "$is_forced" "$meta_bytes")
        else
            response=$(cast send --legacy --private-key "$sender_private_key" \
                --rpc-url "$rpc_url" "$bridge_addr" "$bridge_sig" "$destination_net" \
                "$destination_addr" "$is_forced" "$meta_bytes")
        fi

        local bridge_tx_hash=$(echo "$response" | grep "^transactionHash" | cut -f 2- -d ' ' | sed 's/ //g')
        if [[ -n "$bridge_tx_hash" ]]; then
            log "🎉 Success: Tx Hash → $bridge_tx_hash"
            echo $bridge_tx_hash
        else
            log "❌ Error: Transaction failed (no hash returned)"
            return 1
        fi
    fi
}

function bridge_asset() {
    local token_addr="$1"
    local rpc_url="$2"
    local bridge_sig='bridgeAsset(uint32,address,uint256,address,bool,bytes)'

    if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
        local eth_balance=$(cast balance -e --rpc-url "$rpc_url" "$sender_addr")
        log "💰 $sender_addr ETH Balance: $eth_balance wei"
    else
        local balance_wei=$(cast call --rpc-url "$rpc_url" "$token_addr" "$balance_of_fn_sig" "$sender_addr" | awk '{print $1}')
        local token_balance=$(cast --from-wei "$balance_wei")
        log "💎 $sender_addr token ($token_addr) balance: $token_balance [eth]"
    fi

    log "🚀 Bridge asset $amount wei → $destination_addr [network: $destination_net, rpc url: $rpc_url]"

    if [[ $dry_run == "true" ]]; then
        log "📝 Dry run bridge asset (showing calldata only)"
        cast calldata "$bridge_sig" "$destination_net" "$destination_addr" "$amount" "$token_addr" "$is_forced" "$meta_bytes"
    else
        local response
        if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
            response=$(cast send --legacy --private-key "$sender_private_key" \
                --value "$amount" \
                --rpc-url "$rpc_url" "$bridge_addr" \
                "$bridge_sig" "$destination_net" "$destination_addr" "$amount" "$token_addr" "$is_forced" "$meta_bytes")
        else
            response=$(cast send --legacy --private-key "$sender_private_key" \
                --rpc-url "$rpc_url" "$bridge_addr" \
                "$bridge_sig" "$destination_net" "$destination_addr" "$amount" "$token_addr" "$is_forced" "$meta_bytes")
        fi

        local bridge_tx_hash=$(echo "$response" | grep "^transactionHash" | cut -f 2- -d ' ' | sed 's/ //g')
        if [[ -n "$bridge_tx_hash" ]]; then
            log "🎉 Success: Tx Hash → $bridge_tx_hash"
            echo $bridge_tx_hash
        else
            log "❌ Error: Transaction failed (no hash returned)"
            log "Response:"
            log "$response"
            return 1
        fi
    fi
}

function find_l1_info_tree_index_for_bridge() {
    local network_id="$1"
    local expected_deposit_count="$2"
    local max_attempts="$3"
    local poll_frequency="$4"
    local aggkit_url="$5"

    local attempt=0
    local index=""

    while ((attempt < max_attempts)); do
        ((attempt++))
        log "🔎 Attempt $attempt/$max_attempts: Fetching L1 info tree index for bridge with deposit count $expected_deposit_count"

        # Capture both stdout (index) and stderr (error message)
        index=$(cast rpc --rpc-url "$aggkit_url" "bridge_l1InfoTreeIndexForBridge" "$network_id" "$expected_deposit_count" 2>&1)
        log "------ index ------"
        log "$index"
        log "------ index ------"

        # Check if the response contains an error
        if [[ "$index" == *"error"* || "$index" == *"Error"* || "$index" == "" ]]; then
            log "⚠️ RPC Error: $index"
            sleep "$poll_frequency"
            continue
        fi

        echo "$index"
        return 0
    done

    log "❌ Failed to find L1 info tree index after $max_attempts attempts"
    return 1
}

function find_injected_info_after_index() {
    local network_id="$1"
    local index="$2"
    local max_attempts="$3"
    local poll_frequency="$4"
    local aggkit_url="$5"

    local attempt=0
    local injected_info=""

    while ((attempt < max_attempts)); do
        ((attempt++))
        log "🔎 Attempt $attempt/$max_attempts: fetching injected info after index, params: network_id = $network_id, index = $index"

        # Capture both stdout (injected_info) and stderr (error message)
        injected_info=$(cast rpc --rpc-url "$aggkit_url" "bridge_injectedInfoAfterIndex" "$network_id" "$index" 2>&1)
        log "------ injected_info ------"
        log "$injected_info"
        log "------ injected_info ------"

        # Check if the response contains an error
        if [[ "$injected_info" == *"error"* || "$injected_info" == *"Error"* || "$injected_info" == "" ]]; then
            log "⚠️ RPC Error: $injected_info"
            sleep "$poll_frequency"
            continue
        fi

        echo "$injected_info"
        return 0
    done

    log "❌ Failed to find injected info after index $index after $max_attempts attempts."
    return 1
}

function generate_claim_proof() {
    local network_id="$1"
    local deposit_count="$2"
    local l1_info_tree_index="$3"
    local max_attempts="$4"
    local poll_frequency="$5"
    local aggkit_url="$6"

    local attempt=0
    local proof=""

    while ((attempt < max_attempts)); do
        ((attempt++))
        log "🔎 Attempt $attempt/$max_attempts: fetching proof, params: network_id = $network_id, deposit_count = $deposit_count, l1_info_tree_index = $l1_info_tree_index"

        # Capture both stdout (proof) and stderr (error message)
        proof=$(cast rpc --rpc-url "$aggkit_url" "bridge_claimProof" "$network_id" "$deposit_count" "$l1_info_tree_index" 2>&1)
        log "------ proof ------"
        log "$proof"
        log "------ proof ------"

        # Check if the response contains an error
        if [[ "$proof" == *"error"* || "$proof" == *"Error"* || "$proof" == "" ]]; then
            log "⚠️ RPC Error: $proof"
            sleep "$poll_frequency"
            continue
        fi

        echo "$proof"
        return 0
    done

    log "❌ Failed to generate a claim proof for $deposit_count after $max_attempts attempts."
    return 1
}

function claim_bridge() {
    local bridge_info="$1"
    local proof="$2"
    local destination_rpc_url="$3"
    local max_attempts="$4"
    local poll_frequency="$5"
    local source_network_id="$6"

    local attempt=0

    while true; do
        ((attempt++))
        log "🔍 Attempt $attempt"

        run claim_call "$bridge_info" "$proof" "$destination_rpc_url" "$source_network_id"
        local request_result="$status"
        log "💡 claim_call returns $request_result"
        if [ "$request_result" -eq 0 ]; then
            log "🎉 Claim successful"
            run generate_global_index "$bridge_info" "$source_network_id"
            echo $output
            return 0
        fi

        # Fail test if max attempts are reached
        if [[ "$attempt" -ge "$max_attempts" ]]; then
            echo "❌ Error: Reached max attempts ($max_attempts) without claiming." >&2
            return 1
        fi

        log "⏳ Claim failed this time. We'll retry in $poll_frequency seconds"
        # Sleep before the next attempt
        sleep "$poll_frequency"
    done
}

function check_claim_revert_code() {
    local file_curl_response="$1"
    local response_content
    response_content=$(<"$file_curl_response")

    # 0x646cf558 -> AlreadyClaimed()
    log "💡 Check claim revert code"
    log "$response_content"

    if grep -q "0x646cf558" <<<"$response_content"; then
        log "🎉 Deposit is already claimed (revert code 0x646cf558)"
        return 0
    fi

    # 0x002f6fad -> GlobalExitRootInvalid(), meaning that the global exit root is not yet injected to the destination network
    if grep -q "0x002f6fad" <<<"$response_content"; then
        log "⏳ GlobalExitRootInvalid() (revert code 0x002f6fad)"
        return 2
    fi

    log "❌ Claim failed. response: $response_content"
    return 1
}

function wait_for_expected_token() {
    local bridge_indexer_url="$1"
    local expected_origin_token="$2"
    local max_attempts="$3"
    local poll_frequency="$4"
    local network_id="$5"

    local attempt=0
    local token_mappings_result
    local origin_token_address

    log "⏳ Waiting for expected origin_token_address $bridge_indexer_url @ $enclave..."

    while true; do
        ((attempt++))

        # Fetch token mappings from the RPC
        token_mappings_result=$(cast rpc --rpc-url "$bridge_indexer_url" "bridge_getTokenMappings" "$network_id")

        # Extract the first origin_token_address (if available)
        origin_token_address=$(echo "$token_mappings_result" | jq -r '.tokenMappings[0].origin_token_address')

        log "Attempt $attempt: found origin_token_address = $origin_token_address (Expected: $expected_origin_token)"

        # Break loop if the expected token is found
        if [[ "$origin_token_address" == "$expected_origin_token" ]]; then
            log "✅ Expected origin_token_address '$expected_origin_token' found. Exiting loop."
            echo "$token_mappings_result"
            return 0
        fi

        # Fail test if max attempts are reached
        if [[ "$attempt" -ge "$max_attempts" ]]; then
            echo "Error: Reached max attempts ($max_attempts) without finding expected origin_token_address." >&2
            return 1
        fi

        # Sleep before the next attempt
        sleep "$poll_frequency"
    done
}

function get_bridge() {
    local network_id="$1"
    local expected_tx_hash="$2"
    local max_attempts="$3"
    local poll_frequency="$4"
    local aggkit_url="$5"

    local attempt=0
    local bridges_result=""

    while ((attempt < max_attempts)); do
        ((attempt++))
        log "🔎 Attempt $attempt/$max_attempts: fetching bridge, params: network_id = $network_id, tx_hash = $expected_tx_hash"

        # Capture both stdout (bridge result) and stderr (error message)
        bridges_result=$(cast rpc --rpc-url "$aggkit_url" "bridge_getBridges" "$network_id" 2>&1)
        log "------ bridges_result ------"
        log "$bridges_result"
        log "------ bridges_result ------"

        # Check if the response contains an error
        if [[ "$bridges_result" == *"error"* || "$bridges_result" == *"Error"* || "$bridges_result" == "" ]]; then
            log "⚠️ RPC Error: $bridges_result"
            sleep "$poll_frequency"
            continue
        fi

        # Extract the elements of the 'bridges' array one by one
        for row in $(echo "$bridges_result" | jq -c '.bridges[]'); do
            # Parse out the tx_hash from each element
            tx_hash=$(echo "$row" | jq -r '.tx_hash')

            if [[ "$tx_hash" == "$expected_tx_hash" ]]; then
                log "🎉 Found expected bridge with tx hash: $tx_hash"
                echo "$row"
                return 0
            fi
        done

        sleep "$poll_frequency"
    done

    log "❌ Failed to find bridge after $max_attempts attempts."
    return 1
}

function get_claim() {
    local network_id="$1"
    local expected_global_index="$2"
    local max_attempts="$3"
    local poll_frequency="$4"
    local aggkit_url="$5"
    local attempt=0

    log "🔍 Searching for claim with global_index: "$expected_global_index" (bridge indexer RPC: "$aggkit_url")..."

    while true; do
        ((attempt++))
        log "🔍 Attempt $attempt"
        claims_result=$(cast rpc --rpc-url "$aggkit_url" "bridge_getClaims" "$network_id")

        log "------ claims_result ------"
        log "$claims_result"
        log "------ claims_result ------"

        for row in $(echo "$claims_result" | jq -c '.claims[]'); do
            global_index=$(jq -r '.global_index' <<<"$row")

            if [[ "$global_index" == "$expected_global_index" ]]; then
                log "🎉 Success: Expected global_index '$expected_global_index' found. Exiting loop."
                required_fields=(
                    "block_num"
                    "block_timestamp"
                    "tx_hash"
                    "global_index"
                    "origin_address"
                    "origin_network"
                    "destination_address"
                    "destination_network"
                    "amount"
                    "from_address"
                )
                # Check that all required fields exist (and are not null) in claims[0]
                for field in "${required_fields[@]}"; do
                    value=$(jq -r --arg fld "$field" '.[$fld]' <<<"$row")
                    if [ "$value" = "null" ] || [ -z "$value" ]; then
                        log "🔍 Claims result:"
                        log "$claims_result"

                        echo "❌ Error: Assertion failed missing or null '$field' in the claim object." >&2
                        return 1
                    fi
                done

                echo "$row"
                return 0
            fi
        done

        # Fail test if max attempts are reached
        if [[ "$attempt" -ge "$max_attempts" ]]; then
            log "🔍 Claims result:"
            log "$claims_result"

            echo "❌ Error: Reached max attempts ($max_attempts) without finding expected claim with global index ($expected_global_index)." >&2
            return 1
        fi

        # Sleep before the next attempt
        sleep "$poll_frequency"
    done
}

function claim_call() {
    local bridge_info="$1"
    local proof="$2"
    local destination_rpc_url="$3"
    local source_network_id="$4"

    local claim_sig="claimAsset(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"
    local leaf_type=$(echo "$bridge_info" | jq -r '.leaf_type')
    if [[ $leaf_type != "0" ]]; then
        claim_sig="claimMessage(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"
    fi

    local in_merkle_proof=$(echo "$proof" | jq -r '.proof_local_exit_root | join(",")' | sed 's/^/[/' | sed 's/$/]/')
    local in_rollup_merkle_proof=$(echo "$proof" | jq -r '.proof_rollup_exit_root | join(",")' | sed 's/^/[/' | sed 's/$/]/')
    run generate_global_index "$bridge_info" "$source_network_id"
    local in_global_index=$output
    local in_main_exit_root=$(echo "$proof" | jq -r '.l1_info_tree_leaf.mainnet_exit_root')
    local in_rollup_exit_root=$(echo "$proof" | jq -r '.l1_info_tree_leaf.rollup_exit_root')
    local in_orig_net=$(echo "$bridge_info" | jq -r '.origin_network')
    local in_orig_addr=$(echo "$bridge_info" | jq -r '.origin_address')
    local in_dest_net=$(echo "$bridge_info" | jq -r '.destination_network')
    local in_dest_addr=$(echo "$bridge_info" | jq -r '.destination_address')
    local in_amount=$(echo "$bridge_info" | jq -r '.amount')
    local in_metadata=$(echo "$bridge_info" | jq -r '.metadata')

    if [[ $dry_run == "true" ]]; then
        log "📝 Dry run claim (showing calldata only)"
        cast calldata $claim_sig "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata
    else
        local comp_gas_price=$(bc -l <<<"$gas_price * 1.5" | sed 's/\..*//')
        if [[ $? -ne 0 ]]; then
            log "❌ Failed to calculate gas price" >&3
            return 1
        fi
        log "⏳ Claiming deposit: global_index: $in_global_index orig_net: $in_orig_net dest_net: $in_dest_net amount:$in_amount"
        log "🔍 Exit roots: MainnetExitRoot=$in_main_exit_root RollupExitRoot=$in_rollup_exit_root"
        echo "cast send --legacy --gas-price $comp_gas_price --rpc-url $destination_rpc_url --private-key $sender_private_key $bridge_addr \"$claim_sig\" \"$in_merkle_proof\" \"$in_rollup_merkle_proof\" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata"
        local tmp_response=$(mktemp)
        cast send --legacy --gas-price $comp_gas_price \
            --rpc-url $destination_rpc_url \
            --private-key $sender_private_key \
            $bridge_addr "$claim_sig" "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata 2>$tmp_response || check_claim_revert_code $tmp_response
    fi
}

function generate_global_index() {
    local bridge_info="$1"
    local source_network_id="$2"

    # Extract values from JSON
    deposit_count=$(echo "$bridge_info" | jq -r '.deposit_count')

    # Ensure source_network_id and deposit_count are within valid bit ranges
    source_network_id=$((source_network_id & 0xFFFFFFFF))           # Mask to 32 bits
    deposit_count=$((deposit_count & 0xFFFFFFFF)) # Mask to 32 bits

    # Construct the final value using bitwise operations
    final_value=0

    # 192nd bit: (if mainnet is 0, then 1, otherwise 0)
    if [ "$source_network_id" -eq 0 ]; then
        final_value=$(echo "$final_value + 2^64" | bc)
    fi

    # 193-224 bits: (if mainnet is 0, 0; otherwise source_network_id - 1)
    if [ "$source_network_id" -ne 0 ]; then
        dest_shifted=$(echo "($source_network_id - 1) * 2^32" | bc)
        final_value=$(echo "$final_value + $dest_shifted" | bc)
    fi

    # 225-256 bits: deposit_count (32 bits)
    final_value=$(echo "$final_value + $deposit_count" | bc)

    echo "$final_value"
}
