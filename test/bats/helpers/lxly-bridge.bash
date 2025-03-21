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
        log "💎 $sender_addr Token Balance: $token_balance units [$token_addr]"
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
        log "💎 $sender_addr Token Balance: $token_balance units [$token_addr]"
    fi

    log "🚀 Bridge asset $amount wei → $destination_addr [network: $destination_net]"

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

    log "❌ Failed to find injected info after index after $max_attempts attempts."
    return 1
}

function find_claim_proof() {
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

    log "❌ Failed to find injected info after index after $max_attempts attempts."
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

        run claim_call $bridge_info $proof $destination_rpc_url "$source_network_id"
        local request_result=$status
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
    local expected_origin_token="$1"
    local max_attempts="$2"
    local poll_frequency="$3"
    local aggkit_url="$4"

    local attempt=0
    local token_mappings_result
    local origin_token_address

    while true; do
        ((attempt++))

        # Fetch token mappings from the RPC
        token_mappings_result=$(cast rpc --rpc-url "$aggkit_url" "bridge_getTokenMappings" "$l2_rpc_network_id")

        # Extract the first origin_token_address (if available)
        origin_token_address=$(echo "$token_mappings_result" | jq -r '.tokenMappings[0].origin_token_address')

        echo "Attempt $attempt: found origin_token_address = $origin_token_address (Expected: $expected_origin_token)" >&3

        # Break loop if the expected token is found
        if [[ "$origin_token_address" == "$expected_origin_token" ]]; then
            echo "Success: Expected origin_token_address '$expected_origin_token' found. Exiting loop." >&3
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

    log "❌ Failed to find injected info after index after $max_attempts attempts."
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

    log "-=------------ new code"
    log "in_rollup_merkle_proof: $in_rollup_merkle_proof"
    log "-=------------ new code"

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

# This function is used to claim a bridge using concrete tx hash
# params:
# - timeout - timeout in seconds
# - tx_hash - tx hash of the bridge
# - destination_addr - destination address
# - destination_rpc_url - url of destination rpc
# - bridge_service_url - url of bridge service
# returns:
# - global_index
function claim_bridge_by_tx_hash() {
    local timeout="$1"
    local tx_hash="$2"
    local destination_addr="$3"
    local destination_rpc_url="$4"
    local bridge_service_url="$5"

    readonly bridge_deposit_file=$(mktemp)
    local ready_for_claim="false"
    local start_time=$(date +%s)
    local current_time=$(date +%s)
    local end_time=$((current_time + timeout))

    if [ -z "$bridge_service_url" ]; then
        log "❌ claim_bridge_by_tx_hash bad params"
        log "❌ claim_bridge_by_tx_hash: $*"
        return 1
    fi

    while true; do
        current_time=$(date +%s)
        elapsed_time=$((current_time - start_time))
        if ((current_time > end_time)); then
            log "❌ Exiting... Timeout reached waiting for tx_hash [$tx_hash] timeout: $timeout! (elapsed: $elapsed_time [s])"
            return 1
        fi

        log "🔍 curl -s \"$bridge_service_url/bridges/$destination_addr?limit=100&offset=0\""
        curl -s "$bridge_service_url/bridges/$destination_addr?limit=100&offset=0" | jq "[.deposits[] | select(.tx_hash == \"$tx_hash\" )]" >"$bridge_deposit_file"
        deposit_count=$(jq '. | length' "$bridge_deposit_file")
        if [[ "$deposit_count" == 0 ]]; then
            log "❌ the tx_hash [$tx_hash] not found (elapsed: $elapsed_time [s] / timeout: $timeout [s])"
            sleep "$claim_frequency"
            continue
        fi

        local ready_for_claim=$(jq -r '.[0].ready_for_claim' "$bridge_deposit_file")
        if [ "$ready_for_claim" != "true" ]; then
            log "⏳ the tx_hash $tx_hash is not ready for claim yet (elapsed: $elapsed_time [s] / timeout: $timeout [s])"
            sleep "$claim_frequency"
            continue
        else
            break
        fi
    done

    # Deposit is ready for claim
    log "🎉 the tx_hash $tx_hash is ready for claim! (elapsed: $elapsed_time [s])"
    local curr_claim_tx_hash=$(jq -r '.[0].claim_tx_hash' "$bridge_deposit_file")
    if [ "$curr_claim_tx_hash" != "" ]; then
        local global_index=$(jq -r '.[0].global_index' "$bridge_deposit_file")
        echo "$global_index"
        log "🎉 the bridge with tx_hash: "$tx_hash" is already claimed (global_index "$global_index")"
        return 0
    fi

    local curr_deposit_cnt=$(jq -r '.[0].deposit_cnt' "$bridge_deposit_file")
    local curr_network_id=$(jq -r '.[0].network_id' "$bridge_deposit_file")

    readonly current_deposit=$(mktemp)
    jq '.[0]' "$bridge_deposit_file" >"$current_deposit"
    log "💡 Found deposit info: $(cat "$current_deposit")"

    readonly current_proof=$(mktemp)
    log "🔍 requesting merkle proof for $tx_hash deposit_cnt=$curr_deposit_cnt network_id: $curr_network_id"
    request_merkle_proof "$curr_deposit_cnt" "$curr_network_id" "$bridge_service_url" "$current_proof"

    while true; do
        log "⏳ Requesting claim for $tx_hash..."
        run request_claim "$current_deposit" "$current_proof" "$destination_rpc_url"
        request_result=$status
        log "💡 request_claim returns $request_result"
        if [ "$request_result" -eq 0 ]; then
            log "🎉 Claim successful"
            break
        fi

        if [ "$request_result" -eq 2 ]; then
            # GlobalExitRootInvalid() let's retry
            log "⏳ Claim failed this time (GER is not yet injected on destination). We'll retry in $claim_frequency seconds"
            current_time=$(date +%s)
            elapsed_time=$((current_time - start_time))
            if ((current_time > end_time)); then
                rm "$current_deposit" "$current_proof" "$bridge_deposit_file"
                log "❌ Exiting... Timeout reached waiting for tx_hash [$tx_hash] timeout: $timeout! (elapsed: $elapsed_time [s])"
                return 1
            fi
            sleep "$claim_frequency"
            continue
        fi

        if [ "$request_result" -ne 0 ]; then
            rm "$current_deposit" "$current_proof" "$bridge_deposit_file"
            log "❌ Claim is not successful tx_hash [$tx_hash]"
            return 1
        fi
    done

    local global_index=$(jq -r '.[0].global_index' "$bridge_deposit_file")
    log "✅ Deposit claimed (global_index: $global_index)"

    # Clean up temp files
    rm "$current_deposit" "$current_proof" "$bridge_deposit_file"

    echo "$global_index"
}

function request_merkle_proof() {
    local curr_deposit_cnt="$1"
    local curr_network_id="$2"
    local bridge_service_url="$3"
    local result_proof_file="$4"
    curl -s "$bridge_service_url/merkle-proof?deposit_cnt=$curr_deposit_cnt&net_id=$curr_network_id" | jq '.' >$result_proof_file
}

# This function is used to claim a concrete tx hash
# global vars:
#  -dry_run
#  -gas_price
#  -sender_private_key
#  -bridge_addr
function request_claim() {
    local deposit_file="$1"
    local proof_file="$2"
    local destination_rpc_url="$3"

    local leaf_type=$(jq -r '.leaf_type' $deposit_file)
    local claim_sig="claimAsset(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"

    if [[ $leaf_type != "0" ]]; then
        claim_sig="claimMessage(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"
    fi

    local in_merkle_proof="$(jq -r -c '.proof.merkle_proof' $proof_file | tr -d '"')"
    local in_rollup_merkle_proof="$(jq -r -c '.proof.rollup_merkle_proof' $proof_file | tr -d '"')"
    local in_global_index=$(jq -r '.global_index' $deposit_file)
    local in_main_exit_root=$(jq -r '.proof.main_exit_root' $proof_file)
    local in_rollup_exit_root=$(jq -r '.proof.rollup_exit_root' $proof_file)
    local in_orig_net=$(jq -r '.orig_net' $deposit_file)
    local in_orig_addr=$(jq -r '.orig_addr' $deposit_file)
    local in_dest_net=$(jq -r '.dest_net' $deposit_file)
    local in_dest_addr=$(jq -r '.dest_addr' $deposit_file)
    local in_amount=$(jq -r '.amount' $deposit_file)
    local in_metadata=$(jq -r '.metadata' $deposit_file)

    log "-=------------ old code"
    log "in_rollup_merkle_proof: $in_rollup_merkle_proof"
    log "-=------------ old code"

    log "📝 Dry run claim (showing calldata only)"
    cast calldata $claim_sig "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata
}