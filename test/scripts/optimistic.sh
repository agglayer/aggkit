#!/bin/bash
KURTOSIS_ARTIFACT_AGGKIT_CONFIG=${KURTOSIS_ARTIFACT_AGGKIT_CONFIG:-"cdk-aggoracle-config-artifact"}
KURTOSIS_ENCLAVE=${KURTOSIS_ENCLAVE:-aggkit}

function set_rollup_address_from_kurtosis(){
    local DEST=$(mktemp -d)
    kurtosis files download $KURTOSIS_ENCLAVE $KURTOSIS_ARTIFACT_AGGKIT_CONFIG $DEST
    ROLLUP_ADDRESS=$(cat $DEST/config.toml | grep polygonZkEVMAddress | cut -f 2 -d '=' | tr -d '[:space:]'  | tr -d '"') 
}


function set_l1_rpc_url_from_kurtosis(){
    local _url=$(kurtosis port print $KURTOSIS_ENCLAVE el-1-geth-lighthouse rpc)
    local _port=$(echo "$_url" | sed -E 's|^[a-zA-Z]+://||' | cut -f 2 -d ":")
    export L1_RPC_URL="http://localhost:${_port}"
}


if [ -z $ROLLUP_ADDRESS ]; then
    set_rollup_address_from_kurtosis
fi

if [ -z $L1_RPC_URL ]; then
    set_l1_rpc_url_from_kurtosis
fi

echo "Using rollup address: $ROLLUP_ADDRESS"
echo "Using L1 RPC URL: $L1_RPC_URL"

trustedSequencer=$(cast call "$ROLLUP_ADDRESS" 'trustedSequencer()' --rpc-url "$L1_RPC_URL")
optimisticMode=$(cast call $ROLLUP_ADDRESS "optimisticMode()" --rpc-url $L1_RPC_URL)
echo "Trusted sequencer address: $trustedSequencer"
echo "optimisticMode           : $optimisticMode"
cast send $ROLLUP_ADDRESS "enableOptimisticMode()" --rpc-url "$L1_RPC_URL" --private-key "0xa574853f4757bfdcbb59b03635324463750b27e16df897f3d00dc6bef2997ae0"
#cast send $ROLLUP_ADDRESS "disableOptimisticMode()" --rpc-url "$L1_RPC_URL" --private-key "0xa574853f4757bfdcbb59b03635324463750b27e16df897f3d00dc6bef2997ae0"
