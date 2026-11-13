#!/bin/bash

source $(dirname $0)/local_config_helper.sh
export KURTOSIS_ENCLAVE=op
export AGGKIT_CONFIG_ARTIFACT_NAME=${AGGKIT_CONFIG_ARTIFACT_NAME:=aggkit-config-001}
###############################################################################
function export_values_of_aggkit_config() {
    local AGGKIT_CONFIG_FILE="$1"
    common_export_values_of_aggkit_config "$AGGKIT_CONFIG_FILE"
    export_key_from_toml_file_or_fatal sovereign_bridge_proxy_addr $AGGKIT_CONFIG_FILE BridgeL2Sync BridgeAddr
    export_key_from_toml_file_or_fatal sovereign_rollup_addr $AGGKIT_CONFIG_FILE L1Config polygonZkEVMAddress
    export_key_from_toml_file_or_fatal sovereign_ger_proxy_addr $AGGKIT_CONFIG_FILE L2Config GlobalExitRootAddr
}
###############################################################################
function export_ports_from_kurtosis() {
    common_export_ports_from_kurtosis
    # The L2 execution client can be op-geth or op-reth (sequencer_type)
    local _OP_EL=$(find_kurtosis_service 'op-el-1-op-(geth|reth)-op-node-001')
    local _OP_CL=$(find_kurtosis_service 'op-cl-1-op-node-op-(geth|reth)-001')
    [ -z "$_OP_EL" -o -z "$_OP_CL" ] && log_fatal "the L2 services (op-el-1-..., op-cl-1-...) are not in the enclave $KURTOSIS_ENCLAVE"
    export_portnum_from_kurtosis_or_fail op_el_rpc_port $_OP_EL rpc
    export_portnum_from_kurtosis_or_fail op_cl_rpc_url_port $_OP_CL rpc
    export op_el_rpc_url="http://localhost:${op_el_rpc_port}"
    export op_cl_rpc_url="http://localhost:${op_cl_rpc_url_port}"
    # The prover only exists with an aggchain proof (FEP); with a multisig (ecdsa-multisig) there is none
    if [ -n "$(find_kurtosis_service 'aggkit-prover-001')" ]; then
        export_portnum_from_kurtosis_or_fail aggkit_prover_grpc_port aggkit-prover-001 grpc
        export aggchain_proof_url="http://localhost:${aggkit_prover_grpc_port}"
    else
        log_debug "no aggkit-prover-001 in the enclave $KURTOSIS_ENCLAVE: AggchainProofGen gets a URL that is not used"
        export aggchain_proof_url="http://localhost:0"
    fi
}
###############################################################################
# MAIN
###############################################################################
ORIG_TEMPLATE_FILE=test/config/fep-config.toml.template
DEST_TEMPLATE_FILE=$DEST/test.kurtosis.toml
main $*
