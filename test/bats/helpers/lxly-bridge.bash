#!/usr/bin/env bash

load '../helpers/common'

# Error code reference https://hackmd.io/WwahVBZERJKdfK3BbKxzQQ
function bridge_message() {
    local token_addr="$1"
    local rpc_url="$2"
    local bridge_addr="$3"
    local bridge_sig='bridgeMessage(uint32,address,bool,bytes)'

    local token_balance=$(get_token_balance "$rpc_url" "$token_addr" "$sender_addr")
    if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
        log "💰 $sender_addr ETH balance: $token_balance eth"
    else
        log "💎 $sender_addr ERC20 [$token_addr] token balance: $token_balance eth"
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
    local bridge_addr="$3"
    local bridge_sig='bridgeAsset(uint32,address,uint256,address,bool,bytes)'

    local token_balance=$(get_token_balance "$rpc_url" "$token_addr" "$sender_addr")
    if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
        log "💰 $sender_addr ETH balance: $token_balance eth"
    else
        log "💎 $sender_addr ERC20 [$token_addr] token balance: $token_balance eth"
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

function claim() {
    local destination_rpc_url="$1"
    local bridge_type="$2"
    local bridge_addr="$3"
    local claim_sig="claimAsset(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"
    if [[ $bridge_type == "bridgeMessage" ]]; then
        claim_sig="claimMessage(bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint32,address,uint32,address,uint256,bytes)"
    fi

    readonly bridge_deposit_file=$(mktemp)
    readonly claimable_deposit_file=$(mktemp)
    log "Getting full list of deposits"
    log " curl -s \"$bridge_api_url/bridges/$destination_addr?limit=100&offset=0\""
    curl -s "$bridge_api_url/bridges/$destination_addr?limit=100&offset=0" | jq '.' | tee $bridge_deposit_file

    log "Looking for claimable deposits"
    jq '[.deposits[] | select(.ready_for_claim == true and .claim_tx_hash == "" and .dest_net == '$destination_net')]' $bridge_deposit_file | tee $claimable_deposit_file
    readonly claimable_count=$(jq '. | length' $claimable_deposit_file)
    log "Found $claimable_count claimable deposits"

    if [[ $claimable_count == 0 ]]; then
        log "We have no claimable deposits at this time"
        exit 1
    fi

    log "We have $claimable_count claimable deposits on network $destination_net. Let's get this party started."
    readonly current_deposit=$(mktemp)
    readonly current_proof=$(mktemp)
    local gas_price_factor=1
    while read deposit_idx; do
        log "Starting claim for tx index: $deposit_idx"
        log "Deposit info:"
        log jq --arg idx $deposit_idx '.[($idx | tonumber)]' $claimable_deposit_file | tee $current_deposit

        curr_deposit_cnt=$(jq -r '.deposit_cnt' $current_deposit)
        curr_network_id=$(jq -r '.network_id' $current_deposit)
        curl -s "$bridge_api_url/merkle-proof?deposit_cnt=$curr_deposit_cnt&net_id=$curr_network_id" | jq '.' | tee $current_proof

        in_merkle_proof="$(jq -r -c '.proof.merkle_proof' $current_proof | tr -d '"')"
        in_rollup_merkle_proof="$(jq -r -c '.proof.rollup_merkle_proof' $current_proof | tr -d '"')"
        in_global_index=$(jq -r '.global_index' $current_deposit)
        in_main_exit_root=$(jq -r '.proof.main_exit_root' $current_proof)
        in_rollup_exit_root=$(jq -r '.proof.rollup_exit_root' $current_proof)
        in_orig_net=$(jq -r '.orig_net' $current_deposit)
        in_orig_addr=$(jq -r '.orig_addr' $current_deposit)
        in_dest_net=$(jq -r '.dest_net' $current_deposit)
        in_dest_addr=$(jq -r '.dest_addr' $current_deposit)
        in_amount=$(jq -r '.amount' $current_deposit)
        in_metadata=$(jq -r '.metadata' $current_deposit)

        if [[ $dry_run == "true" ]]; then
            cast calldata $claim_sig "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata
        else
            local comp_gas_price=$(bc -l <<<"$gas_price * 1.5" | sed 's/\..*//')
            if [[ $? -ne 0 ]]; then
                log "Failed to calculate gas price"
                return 1
            fi

            cast send --legacy --gas-price $comp_gas_price --rpc-url $destination_rpc_url --private-key $sender_private_key $bridge_addr "$claim_sig" "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata
        fi

    done < <(seq 0 $((claimable_count - 1)))
}

# This function is used to claim a concrete tx hash
# global vars:
# - destination_addr
# export:
# - global_index
function claim_tx_hash() {
    local timeout="$1"
    local tx_hash="$2"
    local destination_addr="$3"
    local destination_rpc_url="$4"
    local bridge_service_url="$5"
    local bridge_addr="$6"

    readonly bridge_deposit_file=$(mktemp)
    local ready_for_claim="false"
    local start_time=$(date +%s)
    local current_time=$(date +%s)
    local end_time=$((current_time + timeout))
    if [ -z $bridge_service_url ]; then
        log "❌ claim_tx_hash bridge_service_url parameter not provided"
        log "❌ claim_tx_hash: $*"
        exit 1
    fi

    if [ -z $bridge_addr ]; then
        log "❌ claim_tx_hash bridge_addr parameter not provided"
        log "❌ claim_tx_hash: $*"
        exit 1
    fi

    while true; do
        current_time=$(date +%s)
        elapsed_time=$((current_time - start_time))
        if ((current_time > end_time)); then
            log "❌ Exiting... Timeout reached waiting for bridge (tx_hash=$tx_hash) to be claimed timeout: $timeout! (elapsed: $elapsed_time [s])"
            exit 1
        fi

        log "🔍 curl -s \"$bridge_service_url/bridges/$destination_addr?limit=100&offset=0\""
        curl -s "$bridge_service_url/bridges/$destination_addr?limit=100&offset=0" | jq "[.deposits[] | select(.tx_hash == \"$tx_hash\" )]" >$bridge_deposit_file
        deposit_count=$(jq '. | length' $bridge_deposit_file)
        if [[ $deposit_count == 0 ]]; then
            log "❌ the bridge (tx_hash=$tx_hash) not found (elapsed: $elapsed_time [s] / timeout: $timeout [s])"
            sleep "$claim_frequency"
            continue
        fi

        local ready_for_claim=$(jq -r '.[0].ready_for_claim' $bridge_deposit_file)
        if [ $ready_for_claim != "true" ]; then
            log "⏳ the bridge (tx_hash=$tx_hash) is not ready for claim yet (elapsed: $elapsed_time [s] / timeout: $timeout [s])"
            sleep "$claim_frequency"
            continue
        else
            break
        fi
    done

    # Deposit is ready for claim
    log "🎉 the tx_hash $tx_hash is ready for claim! (elapsed: $elapsed_time [s])"
    local curr_claim_tx_hash=$(jq '.[0].claim_tx_hash' $bridge_deposit_file)
    if [ $curr_claim_tx_hash != "\"\"" ]; then
        log "🎉 the bridge (tx_hash=$tx_hash) is already claimed"
        exit 0
    fi

    local curr_deposit_cnt=$(jq '.[0].deposit_cnt' $bridge_deposit_file)
    local curr_network_id=$(jq '.[0].network_id' $bridge_deposit_file)
    readonly current_deposit=$(mktemp)
    jq '.[(0|tonumber)]' $bridge_deposit_file | tee $current_deposit
    log "💡 Found deposit info: $(cat $current_deposit)"

    readonly current_proof=$(mktemp)
    log "🔍 requesting merkle proof for $tx_hash deposit_cnt=$curr_deposit_cnt network_id: $curr_network_id"
    request_merkle_proof "$curr_deposit_cnt" "$curr_network_id" "$bridge_service_url" "$current_proof"

    while true; do
        log "⏳ Requesting claim for $tx_hash..."
        run request_claim $current_deposit $current_proof $destination_rpc_url $bridge_addr
        request_result=$status
        log "💡 request_claim returns status code $request_result"
        if [ $request_result -eq 0 ]; then
            log "🎉 The bridge (tx_hash=$tx_hash) is claimed successfully!"
            break
        fi

        if [ $request_result -eq 2 ]; then
            # GlobalExitRootInvalid() let's retry, since it means that the global exit root is not yet injected to the destination network
            log "⏳ Claim failed this time (GER is not yet injected on destination). We'll retry in $claim_frequency seconds "
            current_time=$(date +%s)
            elapsed_time=$((current_time - start_time))
            if ((current_time > end_time)); then
                log "❌ Exiting... Timeout reached waiting for bridge to be claimed (tx_hash=$tx_hash) timeout: $timeout! (elapsed: $elapsed_time [s])"
                exit 1
            fi
            sleep $claim_frequency
            continue
        fi

        if [ $request_result -ne 0 ]; then
            log "❌ Claim failed for bridge (tx_hash=$tx_hash)"
            exit 1
        fi
    done

    export global_index=$(jq -r '.global_index' $current_deposit)
    log "✅ Bridge (tx_hash=$tx_hash) claimed ($global_index)"

    # clean up temp files
    rm $current_deposit
    rm $current_proof
    rm $bridge_deposit_file
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
    local bridge_addr="$4"

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

function wait_for_claim() {
    local timeout="$1"             # timeout (in seconds)
    local claim_frequency="$2"     # claim frequency (in seconds)
    local destination_rpc_url="$3" # destination rpc url
    local bridge_type="$4"         # bridgeAsset or bridgeMessage
    local bridge_addr="$5"         # bridge address
    local start_time=$(date +%s)
    local end_time=$((start_time + timeout))

    if [ -z $bridge_addr ]; then
        log "❌ wait_for_claim bridge_addr parameter not provided"
        log "❌ wait_for_claim: $*"
        exit 1
    fi

    while true; do
        local current_time=$(date +%s)
        if ((current_time > end_time)); then
            log "❌ Exiting... Timeout reached!"
            return 1
        fi

        run claim $destination_rpc_url $bridge_type $bridge_addr
        if [ $status -eq 0 ]; then
            break
        fi

        sleep "$claim_frequency"
    done
}
