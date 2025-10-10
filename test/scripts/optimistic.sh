#!/bin/bash
KURTOSIS_ARTIFACT_AGGKIT_CONFIG=${KURTOSIS_ARTIFACT_AGGKIT_CONFIG:-"aggkit-config-artifact"}
KURTOSIS_ENCLAVE=${KURTOSIS_ENCLAVE:-op}
# https://github.com/0xPolygon/kurtosis-cdk/blob/64c640ee0effea15c6ac76a9c5dd5869d79e0393/input_parser.star#L196
PRIVATE_KEY=${PRIVATE_KEY:-"0x12d7de8621a77640c9241b2595ba78ce443d05e94090365ab3bb5e19df82c625"}
function set_rollup_address_from_kurtosis(){
    local DEST=$(mktemp -d)
    kurtosis files download $KURTOSIS_ENCLAVE $KURTOSIS_ARTIFACT_AGGKIT_CONFIG $DEST
    ROLLUP_ADDRESS=$(cat $DEST/config.toml | grep polygonZkEVMAddress | tr -d '[:space:]' | cut -f 2 -d '=' | tr -d '"') 
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
case "$1" in
    enable)
        cast send $ROLLUP_ADDRESS "enableOptimisticMode()" --rpc-url "$L1_RPC_URL" --private-key "$PRIVATE_KEY"
        echo "Optimistic mode enabled."
        ;;
    disable)
        cast send $ROLLUP_ADDRESS "disableOptimisticMode()" --rpc-url "$L1_RPC_URL" --private-key "$PRIVATE_KEY"
        echo "Optimistic mode disabled."
        ;;
    help|*)
        echo "Usage: $0 [enable|disable|help]"
        echo "  enable   - Enables optimistic mode."
        echo "  disable  - Disables optimistic mode."
        echo "  help     - Displays this help message."
        ;;
esac
