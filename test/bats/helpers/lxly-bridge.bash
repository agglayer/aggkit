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
        log "📝 Dry run bridge asset (showing calldata only)"
        cast calldata $bridge_sig $destination_net $destination_addr $amount $token_addr $is_forced $meta_bytes
    else
        if [[ $token_addr == "0x0000000000000000000000000000000000000000" ]]; then
            log "cast send --legacy --private-key $sender_private_key --value $amount --rpc-url $rpc_url $bridge_addr $bridge_sig $destination_net $destination_addr $is_forced $meta_bytes"
            cast send --legacy --private-key $sender_private_key --value $amount --rpc-url $rpc_url $bridge_addr $bridge_sig $destination_net $destination_addr $is_forced $meta_bytes
        else
            log "cast send --legacy --private-key $sender_private_key --rpc-url $rpc_url $bridge_addr $bridge_sig $destination_net $destination_addr $is_forced $meta_bytes"
            cast send --legacy --private-key $sender_private_key --rpc-url $rpc_url $bridge_addr $bridge_sig $destination_net $destination_addr $is_forced $meta_bytes
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

function claim() {
    local destination_rpc_url="$1"
    local bridge_type="$2"
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

            echo "cast send --legacy --gas-price $comp_gas_price --rpc-url $destination_rpc_url --private-key $sender_private_key $bridge_addr \"$claim_sig\" \"$in_merkle_proof\" \"$in_rollup_merkle_proof\" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata" >&3
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
    local bridge_merkle_proof_url="$5"

    readonly bridge_deposit_file=$(mktemp)
    local ready_for_claim="false"
    local start_time=$(date +%s)
    local current_time=$(date +%s)
    local end_time=$((current_time + timeout))
    if [ -z $bridge_merkle_proof_url ]; then
        log "❌ claim_tx_hash bad params"
        log "❌ claim_tx_hash: $*"
        exit 1
    fi

    while true; do
        current_time=$(date +%s)
        elapsed_time=$((current_time - start_time))
        if ((current_time > end_time)); then
            log "❌ Exiting... Timeout reached waiting for tx_hash [$tx_hash] timeout: $timeout! (elapsed: $elapsed_time)"
            exit 1
        fi

        log "curl -s \"$bridge_merkle_proof_url/bridges/$destination_addr?limit=100&offset=0\""
        curl -s "$bridge_merkle_proof_url/bridges/$destination_addr?limit=100&offset=0" | jq "[.deposits[] | select(.tx_hash == \"$tx_hash\" )]" >$bridge_deposit_file
        deposit_count=$(jq '. | length' $bridge_deposit_file)
        log "deposit_count=$deposit_count bridge_deposit_file=$bridge_deposit_file"
        if [[ $deposit_count == 0 ]]; then
            log "❌ the tx_hash [$tx_hash] not found (elapsed: $elapsed_time / timeout:$timeout)"
            sleep "$claim_frequency"
            continue
        fi

        local ready_for_claim=$(jq '.[0].ready_for_claim' $bridge_deposit_file)
        if [ $ready_for_claim == "true" ]; then
            break
        fi

        log "⏳ the tx_hash $tx_hash is not ready for claim yet (elapsed: $elapsed_time / timeout:$timeout)"
        sleep "$claim_frequency"
        continue
    done
    # Deposit is ready for claim
    log "🎉 the tx_hash $tx_hash is ready for claim! (elapsed: $elapsed_time)"
    local curr_claim_tx_hash=$(jq '.[0].claim_tx_hash' $bridge_deposit_file)
    if [ $curr_claim_tx_hash != "\"\"" ]; then
        log "🎉 the tx_hash $tx_hash is already claimed"
        exit 0
    fi
    local curr_deposit_cnt=$(jq '.[0].deposit_cnt' $bridge_deposit_file)
    local curr_network_id=$(jq '.[0].network_id' $bridge_deposit_file)
    readonly current_deposit=$(mktemp)
    jq '.[(0|tonumber)]' $bridge_deposit_file | tee $current_deposit
    log "Deposit info: $(cat $current_deposit)"
    readonly current_proof=$(mktemp)
    log "requesting merkle proof for $tx_hash deposit_cnt=$curr_deposit_cnt network_id: $curr_network_id"
    request_merkle_proof "$curr_deposit_cnt" "$curr_network_id" "$bridge_merkle_proof_url" "$current_proof"

    while true; do
        log "Requesting claim for $tx_hash..."
        request_claim $current_deposit $current_proof $destination_rpc_url
        request_result=$status
        log "ℹ️ request_claim returns $request_result"
        if [ $request_result -eq 0 ]; then
            log "🎉 Claim successful"
            break
        fi
        if [ $request_result -eq 2 ]; then
            # GlobalExitRootInvalid() let's retry
            log "❌ Claim failed, let's retry"
            current_time=$(date +%s)
            elapsed_time=$((current_time - start_time))
            if ((current_time > end_time)); then
                log "❌ Exiting... Timeout reached waiting for tx_hash [$tx_hash] timeout: $timeout! (elapsed: $elapsed_time)"
                exit 1
            fi
            sleep $claim_frequency
            continue
        fi
        if [ $request_result -ne 0 ]; then
            log "✅ Claim successful tx_hash [$tx_hash]"
            exit 1
        fi
    done

    local global_index=$(jq -r '.global_index' $current_deposit)

    # clean up temp files
    rm $current_deposit
    rm $current_proof
    rm $bridge_deposit_file

    log "✅ Deposit claimed ($global_index)"
    echo $global_index
}

function request_merkle_proof() {
    local curr_deposit_cnt="$1"
    local curr_network_id="$2"
    local bridge_merkle_proof_url="$3"
    local result_proof_file="$4"
    curl -s "$bridge_merkle_proof_url/merkle-proof?deposit_cnt=$curr_deposit_cnt&net_id=$curr_network_id" | jq '.' >$result_proof_file
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
    if [[ $dry_run == "true" ]]; then
        log "📝 Dry run claim (showing calldata only)"
        cast calldata $claim_sig "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata
    else
        local comp_gas_price=$(bc -l <<<"$gas_price * 1.5" | sed 's/\..*//')
        if [[ $? -ne 0 ]]; then
            log "❌ Failed to calculate gas price" >&3
            return 1
        fi
        log "Claiming deposit: global_index: $in_global_index orig_net: $in_orig_net dest_net: $in_dest_net amount:$in_amount"
        log "claim: mainnetExitRoot=$in_main_exit_root rollupExitRoot=$in_rollup_exit_root"
        log "cast send --legacy --gas-price $comp_gas_price --rpc-url $destination_rpc_url --private-key $sender_private_key $bridge_addr \"$claim_sig\" \"$in_merkle_proof\" \"$in_rollup_merkle_proof\" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata"
        local tmp_response=$(mktemp)
        cast send --legacy --gas-price $comp_gas_price \
            --rpc-url $destination_rpc_url \
            --private-key $sender_private_key \
            $bridge_addr "$claim_sig" "$in_merkle_proof" "$in_rollup_merkle_proof" $in_global_index $in_main_exit_root $in_rollup_exit_root $in_orig_net $in_orig_addr $in_dest_net $in_dest_addr $in_amount $in_metadata 2>$tmp_response || check_claim_revert_code $tmp_response
    fi
}

function check_claim_revert_code() {
    local file_curl_reponse="$1"
    # 0x646cf558 -> AlreadyClaimed()
    log "check revert"
    cat $file_curl_reponse
    cat $file_curl_reponse | grep "0x646cf558" >/dev/null
    if [ $? -eq 0 ]; then
        log "🎉 Deposit is already claimed (revert code 0x646cf558)"
        return 0
    fi
    cat $file_curl_reponse | grep "0x002f6fad" >/dev/null
    if [ $? -eq 0 ]; then
        log "🎉 GlobalExitRootInvalid()(revert code 0x002f6fad)"
        return 2
    fi
    log "❌ Claim failed"
    log $file_curl_reponse
    return 1
}

function wait_for_claim() {
    local timeout="$1"             # timeout (in seconds)
    local claim_frequency="$2"     # claim frequency (in seconds)
    local destination_rpc_url="$3" # destination rpc url
    local bridge_type="$4"         # bridgeAsset or bridgeMessage
    local start_time=$(date +%s)
    local end_time=$((start_time + timeout))

    while true; do
        local current_time=$(date +%s)
        if ((current_time > end_time)); then
            log "❌ Exiting... Timeout reached!"
            return 1
        fi

        run claim $destination_rpc_url $bridge_type
        if [ $status -eq 0 ]; then
            break
        fi

        sleep "$claim_frequency"
    done
}
