#!/bin/bash

source $(dirname $0)/../test/scripts/env.sh
export AGGKIT_SERVICE=${AGGKIT_SERVICE:=aggkit-001}
export DEST=${TMP_AGGKIT_FOLDER}/local_config
# GER validators (AggOracleCommittee v2) to run locally: comma separated indexes (1 is ger-validator-001-001)
export LOCAL_GER_VALIDATORS=${LOCAL_GER_VALIDATORS:=1}
# The validator number N listens locally on GER_VALIDATOR_LOCAL_BASE_PORT + N
export GER_VALIDATOR_LOCAL_BASE_PORT=${GER_VALIDATOR_LOCAL_BASE_PORT:=18080}
export GER_VALIDATOR_TEMPLATE_FILE=test/config/ger-validator-signer.toml.template
# Debug image of the GER validator (make build-docker-debug-ger_validator_signer): dlv listens on DEBUG_BASE_PORT + N
export GER_VALIDATOR_DEBUG_IMAGE=${GER_VALIDATOR_DEBUG_IMAGE:=ger_validator_signer:local-debug}
export GER_VALIDATOR_DEBUG_BASE_PORT=${GER_VALIDATOR_DEBUG_BASE_PORT:=40000}
# Components of the local aggkit: the ones of aggkit-001 in kurtosis (aggsender, aggoracle) plus the bridge service
export AGGKIT_LOCAL_COMPONENTS=${AGGKIT_LOCAL_COMPONENTS:=aggsender,aggoracle,bridge}

###############################################################################
# params:
# $1 -> exported variable name
# $2 -> service name
# $3...$n -> endpoint names (will try all of them until one is found)
###############################################################################
function export_value_from_kurtosis_or_fail() {
    local _EXPORTED_VAR_NAME="$1"
    shift
    local _SERVICE="$1"
    shift
    local _END_POINT
    local _RESULT
    log_debug "Trying to get kurtosis value:$_EXPORTED_VAR_NAME = $KURTOSIS_ENCLAVE $_SERVICE $*"
    while [ ! -z $1 ]; do
        _END_POINT=$1
        shift
        log_debug "---  kurtosis value:  $KURTOSIS_ENCLAVE $_SERVICE $_END_POINT"
        _RESULT=$(kurtosis port print $KURTOSIS_ENCLAVE $_SERVICE $_END_POINT 2>/dev/null)
        if [ ! -z $_RESULT ]; then
            break
        fi
    done
    export $_EXPORTED_VAR_NAME=$_RESULT
    if [ -z $_EXPORTED_VAR_NAME ]; then
        log_fatal "Error getting kurtosis port:  $KURTOSIS_ENCLAVE $_SERVICE $_END_POINT"
    fi
    log_debug "$_EXPORTED_VAR_NAME=${!_EXPORTED_VAR_NAME}  \t\t\t# Kurtosis $KURTOSIS_ENCLAVE $_SERVICE $_END_POINT"
}
###############################################################################
function log_debug() {
    echo -e "\033[0;90mDebug: $*" "\033[0m"
}
###############################################################################
function log_error() {
    echo -e "\033[0;31mError: $*" "\033[0m"
}
###############################################################################
function log_fatal() {
    log_error $*
    exit 1
}
###############################################################################
function ok_or_fatal() {
    if [ $? -ne 0 ]; then
        log_fatal $*
    fi
}
###############################################################################
function get_value_from_toml_file() {
    local _FILE="$1"
    # KEY  = <section1>.<section2>
    local _SECTION="$2"
    local _KEY="$3"
    local _LINE
    local _inside_section=0
    if [ $_SECTION == "." ]; then
        _SECTION=""
        _inside_section=1
    fi
    local _return_next_line=0
    local _TMP_FILE=$(mktemp)
    cat $_FILE >$_TMP_FILE
    # Maybe the file doesnt end with a new line so we added just in case
    echo >>$_TMP_FILE
    while read -r _LINE; do
        # Clean up line from spaces and tabs
        _LINE=$(echo $_LINE | tr -d '[:space:]')
        if [ $_inside_section -eq 1 ]; then
            if [[ "$_LINE" == [* ]]; then
                return 1
            fi
            if [ $_return_next_line -eq 1 ]; then
                # sed sentence remove quotes
                echo $_LINE | sed 's/^[[:space:]]*"//;s/"$//'

                return 0
            fi
            #local _key_splitted=(${_LINE//=/ })
            local _key_name=$(echo $_LINE | cut -f 1 -d "=")
            local _key_value=$(echo $_LINE | cut -f 2- -d "=")
            if [ "$_key_name" == "$_KEY" ]; then
                if [ $_key_value == "[" ]; then
                    _return_next_line=1
                else
                    rm $_TMP_FILE
                    # sed sentence remove quotes
                    echo $_key_value | sed 's/^[[:space:]]*"//;s/"$//'
                    return 0
                fi
            fi
        elif [ "$_LINE" == "[${_SECTION}]" ]; then
            _inside_section=1
        fi

    done <"$_TMP_FILE"
    rm $_TMP_FILE
    return 2

}
###############################################################################
function export_key_from_toml_file_or_fatal() {
    export_key_from_toml_file "$1" "$2" "$3" "$4"
    if [ $? -ne 0 ]; then
        local _EXPORTED_VAR_NAME="$1"
        local _FILE="$2"
        local _SECTION="$3"
        local _KEY="$4"
        log_fatal "$FUNCNAME: key [$_KEY] not found in section [$_SECTION] in file [$_FILE]"
    fi
}
###############################################################################
function export_key_from_toml_file() {
    local _EXPORTED_VAR_NAME="$1"
    local _FILE="$2"
    local _SECTION="$3"
    local _KEY="$4"
    local _VALUE=$(get_value_from_toml_file $_FILE $_SECTION $_KEY)
    if [ -z "$_VALUE" ]; then
        log_debug "$FUNCNAME: key [$_KEY] not found in section [$_SECTION] in file [$_FILE]"
        return 1
    fi
    export $_EXPORTED_VAR_NAME="$_VALUE"
    log_debug "$_EXPORTED_VAR_NAME=${!_EXPORTED_VAR_NAME}  \t\t\t# file:$_FILE section:$_SECTION key:$_KEY"
    return 0
}
###############################################################################
function export_obj_key_from_toml_file_or_fatal() {
    export_obj_key_from_toml_file $*
    if [ $? -ne 0 ]; then
        local _EXPORTED_VAR_NAME="$1"
        local _FILE="$2"
        local _SECTION="$3"
        local _KEY="$4"
        log_fatal "$FUNCNAME: obj_key [$_KEY] not found in section [$_SECTION] in file [$_FILE]"
    fi
}
###############################################################################
function export_obj_key_from_toml_file() {
    local _EXPORTED_VAR_NAME="$1"
    local _FILE="$2"
    local _SECTION="$3"
    local _KEY="$4"
    local _OBJ_KEY="$5"
    log_debug "export_obj_key_from_toml_file: $_EXPORTED_VAR_NAME $_FILE $_SECTION $_KEY $_OBJ_KEY"
    local _VALUE=$(get_value_from_toml_file $_FILE $_SECTION $_KEY)
    if [ -z "$_VALUE" ]; then
        log_debug "export_obj_key_from_toml_file: obj_key $_KEY not found in section [$_SECTION]"
        return 1
    fi
    local _CLEAN_VALUE=$(echo $_VALUE | tr -d '{' | tr -d '}' | tr ',' '\n')
    while read -r _LINE; do
        local _key_splitted=(${_LINE//=/ })

        if [ "${_key_splitted[0]}" == "$_OBJ_KEY" ]; then
            local _KEY_VALUE=${_key_splitted[1]}
            if [ "$_KEY_VALUE" == "[" ]; then
                read -r _LINE
                _KEY_VALUE=$LINE
                echo "zzz $_KEY_VALUE"
            fi
            local _RES=$(echo $_KEY_VALUE | sed 's/^[[:space:]]*"//;s/"$//')
            export $_EXPORTED_VAR_NAME="${_RES}"
            log_debug "$_EXPORTED_VAR_NAME=${!_EXPORTED_VAR_NAME}  \t\t\t# file:$_FILE section:$_SECTION key:$_KEY obj_key:$_OBJ_KEY"
            return 0
        fi
    done <<<"$_CLEAN_VALUE"
    log_debug "export_obj_key_from_toml_file: obj_key $_OBJ_KEY not found in section $_SECTION/ $_KEY = $_VALUE"
    return 1
}
###############################################################################
function export_portnum_from_kurtosis_or_fail() {
    local _EXPORTED_VAR_NAME="$1"
    export_value_from_kurtosis_or_fail $* >/dev/null
    local _VALUE
    eval "_VALUE=\$$1"
    # sed sentece eliminate protocol (xyz://) is have it
    # kurtosis sometimes include protocol but not always
    local _PORT=$(echo "$_VALUE" | sed -E 's|^[a-zA-Z]+://||' | cut -f 2 -d ":")
    if [ -z $_PORT ]; then
        log_fatal "Error getting port number from kurtosis:  $2 $3 -> $_VALUE"
    fi
    export $_EXPORTED_VAR_NAME=$_PORT
    log_debug "$_EXPORTED_VAR_NAME=${!_EXPORTED_VAR_NAME} \t\t\t# Kurtosis $KURTOSIS_ENCLAVE $2 $3"
}
###############################################################################
# Prints the first service of the enclave whose name matches the regex $1 (empty if there is none)
###############################################################################
function find_kurtosis_service() {
    kurtosis enclave inspect $KURTOSIS_ENCLAVE 2>/dev/null | grep -o -E "$1" | head -n 1
}
###############################################################################
function export_forced_values() {
    export global_log_level="debug"
    export l2_rpc_name="localhost"
    export sequencer_name="localhost"
    export deployment_suffix=""
    export enable_aggkit_claim_sponsor="false"
    export zkevm_l2_claimsponsor_address="0x0000000000000000000000000000000000000000"
    export aggkit_pprof_port="6060"
    export aggkit_pprof_enabled="false"
}
###############################################################################
function check_requirements() {
    which kurtosis >/dev/null
    if [ $? -ne 0 ]; then
        log_error "kurtosis is not installed. Please install it:"
        cat <<EOF
            echo "deb [trusted=yes] https://apt.fury.io/kurtosis-tech/ /" | sudo tee /etc/apt/sources.list.d/kurtosis.list
        echo "deb [trusted=yes] https://apt.fury.io/kurtosis-tech/ /" | sudo tee /etc/apt/sources.list.d/kurtosis.list
        sudo apt install kurtosis-cli
        kurtosis version
EOF
        exit 1

    fi
    which cast >/dev/null || log_fatal "cast (foundry) is not installed: it reads the address of the keystores of the GER validators"
    if [ -z $TMP_AGGKIT_FOLDER -o -z $KURTOSIS_ENCLAVE ]; then
        log_fatal "TMP_AGGKIT_FOLDER or KURTOSIS_ENCLAVE is not set. Must be set on file env.sh"
    fi
    kurtosis enclave inspect $KURTOSIS_ENCLAVE >/dev/null
    if [ $? -ne 0 ]; then
        log_error "Error inspecting enclave $KURTOSIS_ENCLAVE"
        echo "You must start kurtosis environment before running this script"
        echo "- start kurtosis:"
        echo "    kurtosis clean --all; kurtosis run --enclave $KURTOSIS_ENCLAVE --args-file params.yml --image-download always ."

        exit 1
    fi
}
###############################################################################
function create_dest_folder() {
    export zkevm_path_rw_data=${TMP_AGGKIT_FOLDER}/runtime
    [ ! -d ${DEST} ] && mkdir -p ${DEST}
    rm -rf "$DEST"/*
    mkdir -p $zkevm_path_rw_data
}
###############################################################################
function download_kurtosis_artifacts() {
    kurtosis files download $KURTOSIS_ENCLAVE aggkit-sequencer-keystore $DEST
    ok_or_fatal "Error downloading kurtosis artifact aggkit-sequencer-keystore to $DEST"
    export zkevm_l2_sequencer_keystore_file_path=$DEST/sequencer.keystore

    kurtosis files download $KURTOSIS_ENCLAVE $AGGKIT_CONFIG_ARTIFACT_NAME $DEST
    ok_or_fatal "Error downloading kurtosis artifact $AGGKIT_CONFIG_ARTIFACT_NAME to $DEST"

    kurtosis files download $KURTOSIS_ENCLAVE aggoracle-keystore $DEST
    ok_or_fatal "Error downloading kurtosis artifact aggoracle to $DEST"
    export zkevm_aggoracle_privatekey_path=$DEST/aggoracle.keystore
}

###############################################################################
function common_export_values_of_aggkit_config() {
    local AGGKIT_CONFIG_FILE=$1
    export_key_from_toml_file_or_fatal zkevm_rollup_manager_block_number $AGGKIT_CONFIG_FILE "." genesisBlockNumber
    export_key_from_toml_file_or_fatal zkevm_l2_keystore_password $AGGKIT_CONFIG_FILE "." SequencerPrivateKeyPassword
    export_key_from_toml_file_or_fatal l1_chain_id $AGGKIT_CONFIG_FILE L1Config chainId
    export_key_from_toml_file_or_fatal pol_token_address $AGGKIT_CONFIG_FILE L1Config polTokenAddress
    export_key_from_toml_file_or_fatal zkevm_rollup_manager_address $AGGKIT_CONFIG_FILE L1Config polygonRollupManagerAddress
    export_key_from_toml_file_or_fatal zkevm_global_exit_root_address $AGGKIT_CONFIG_FILE L1Config polygonZkEVMGlobalExitRootAddress
    export_key_from_toml_file_or_fatal zkevm_rollup_chain_id $AGGKIT_CONFIG_FILE AggOracle.EVMSender.EthTxManager.Etherman L1ChainID
    export_key_from_toml_file_or_fatal zkevm_bridge_address $AGGKIT_CONFIG_FILE L1Config BridgeAddr
    export_key_from_toml_file_or_fatal zkevm_bridge_l2_address $AGGKIT_CONFIG_FILE L2Config BridgeAddr
}

###############################################################################
function common_export_ports_from_kurtosis() {
    # The L1 execution client can be geth or reth
    local _L1_EL=$(find_kurtosis_service 'el-1-(geth|reth)-lighthouse')
    [ -z "$_L1_EL" ] && log_fatal "the L1 service (el-1-...-lighthouse) is not in the enclave $KURTOSIS_ENCLAVE"
    export_portnum_from_kurtosis_or_fail l1_rpc_port $_L1_EL rpc
    export_portnum_from_kurtosis_or_fail agglayer_grpc_port agglayer aglr-grpc
    if [ ! -z $EXPOSE_PORTS ];then
        export_portnum_from_kurtosis_or_fail aggkit_node_rest_api_port $AGGKIT_SERVICE http-rpc rest
    else
        export aggkit_node_rest_api_port=5577
    fi
    export l1_rpc_url="http://localhost:${l1_rpc_port}"
    export agglayer_grpc_url="http://localhost:${agglayer_grpc_port}"
}
###############################################################################
function common_aggsender_committee_override_urls(){
    export aggsender_committee_override_urls=""
    local _VALIDATOR
    for _VALIDATOR in $(kurtosis enclave inspect $KURTOSIS_ENCLAVE | grep aggkit-001-aggsender-validator | sed -r "s/\ +/\ /g" | cut -f 2 -d ' '); do
        export_portnum_from_kurtosis_or_fail _LOCAL_PORT_VALIDATOR $_VALIDATOR validator-grpc
        log_debug "Found validator: $_VALIDATOR port=$_LOCAL_PORT_VALIDATOR"
        aggsender_committee_override_urls="${aggsender_committee_override_urls} \"http://${_VALIDATOR}:5578\" = \"http://localhost:${_LOCAL_PORT_VALIDATOR}\","
    done
    if [ ! -z "$aggsender_committee_override_urls" ]; then
        # Remove last ',' and add the {}
        aggsender_committee_override_urls="{ ${aggsender_committee_override_urls%,} }"
    else
        aggsender_committee_override_urls="{}"
    fi
    log_debug "aggsender_committee_override_urls=${aggsender_committee_override_urls}"
}
###############################################################################
# AggOracleCommittee v2: the GER validators of the enclave (ger_validator_signer services).
# The committee registers their URLs inside the enclave (http://ger-validator-001-00N:8080), that
# can't be reached from the host, so the AggOracle gets a ValidatorURLOverride for each one:
#  - the validators in LOCAL_GER_VALIDATORS -> the ger_validator_signer that runs locally
#  - the rest -> the port that kurtosis publishes on the host for its service
# For each validator it downloads its keystore and renders a config to run it locally.
###############################################################################
function common_ger_validators() {
    export aggoracle_mode="auto"
    export aggoracle_validator_url_overrides="[]"
    export GER_VALIDATORS_LOCAL_CONFIGS=""
    local _SERVICES
    _SERVICES=$(kurtosis enclave inspect $KURTOSIS_ENCLAVE | grep -o -E 'ger-validator-001-[0-9]+' | sort -u)
    if [ -z "$_SERVICES" ]; then
        log_debug "no GER validators in the enclave $KURTOSIS_ENCLAVE: AggOracle.Mode = auto"
        return 0
    fi
    export aggoracle_mode="committee"
    local _OVERRIDES=""
    local _SERVICE _INDEX _DIR _ADDRESS _URL _LOCAL_PORT
    for _SERVICE in $_SERVICES; do
        _INDEX=$((10#${_SERVICE##*-}))
        _DIR=$DEST/ger_validator_$_INDEX
        mkdir -p $_DIR
        kurtosis files download $KURTOSIS_ENCLAVE ger-validator-config-${_INDEX}-001 $_DIR >/dev/null
        ok_or_fatal "Error downloading kurtosis artifact ger-validator-config-${_INDEX}-001 to $_DIR"
        mv $_DIR/config.toml $_DIR/kurtosis.config.toml
        kurtosis files download $KURTOSIS_ENCLAVE ger-validator-${_INDEX}-keystore-001 $_DIR >/dev/null
        ok_or_fatal "Error downloading kurtosis artifact ger-validator-${_INDEX}-keystore-001 to $_DIR"
        export ger_validator_keystore_path=$(realpath $_DIR/gervalidator-${_INDEX}.keystore)
        # The keystores of kurtosis-cdk are made by cast, without the "address" field: decrypt it
        _ADDRESS=$(cast wallet address --keystore $ger_validator_keystore_path \
            --password "$zkevm_l2_keystore_password" 2>/dev/null)
        [ -z "$_ADDRESS" ] && log_fatal "can't get the address of the keystore $ger_validator_keystore_path"

        # The config to run it locally: the finality of the enclave's validator, and a port of its own
        export_key_from_toml_file_or_fatal ger_validator_finality_tag $_DIR/kurtosis.config.toml Finality Tag
        export_key_from_toml_file_or_fatal ger_validator_finality_num_blocks $_DIR/kurtosis.config.toml Finality NumBlocks
        export ger_validator_local_port=$((GER_VALIDATOR_LOCAL_BASE_PORT + _INDEX))
        go run scripts/run_template.go $GER_VALIDATOR_TEMPLATE_FILE >$_DIR/config.toml
        ok_or_fatal "Error generating the config of the GER validator $_INDEX"
        check_rendered_file $GER_VALIDATOR_TEMPLATE_FILE $_DIR/config.toml
        GER_VALIDATORS_LOCAL_CONFIGS="$GER_VALIDATORS_LOCAL_CONFIGS $_INDEX:$(realpath $_DIR/config.toml)"

        if [[ ",$LOCAL_GER_VALIDATORS," == *",$_INDEX,"* ]]; then
            _URL="http://localhost:${ger_validator_local_port}"
            log_debug "GER validator $_INDEX ($_ADDRESS) runs locally: $_URL"
        else
            export_portnum_from_kurtosis_or_fail _KURTOSIS_PORT $_SERVICE rest
            _URL="http://localhost:${_KURTOSIS_PORT}"
            log_debug "GER validator $_INDEX ($_ADDRESS) runs in kurtosis ($_SERVICE): $_URL"
        fi
        _OVERRIDES="${_OVERRIDES} {Address = \"$_ADDRESS\", URL = \"$_URL\"},"
    done
    export aggoracle_validator_url_overrides="[${_OVERRIDES%,} ]"
    log_debug "aggoracle_validator_url_overrides=${aggoracle_validator_url_overrides}"
}
###############################################################################
function print_ger_validators_instructions() {
    [ -z "$GER_VALIDATORS_LOCAL_CONFIGS" ] && return 0
    local _ENTRY _INDEX
    echo "- AggOracleCommittee v2 (AggOracle.Mode = committee). Configs of the GER validators:"
    for _ENTRY in $GER_VALIDATORS_LOCAL_CONFIGS; do
        echo "    validator ${_ENTRY%%:*}: ${_ENTRY#*:}"
    done
    for _INDEX in ${LOCAL_GER_VALIDATORS//,/ }; do
        [[ " $GER_VALIDATORS_LOCAL_CONFIGS" == *" $_INDEX:"* ]] || continue
        echo "  The AggOracle asks the validator $_INDEX to the local one: stop the one of kurtosis"
        echo "    kurtosis service stop $KURTOSIS_ENCLAVE ger-validator-001-00$_INDEX"
    done
    local _DIR
    echo "  Run the local GER validator with the debug image (make build-docker-debug-ger_validator_signer): dlv listens"
    echo "  on 40000 + N, attach to it with the launch.json entry below (host network: the L1 is on localhost)"
    for _INDEX in ${LOCAL_GER_VALIDATORS//,/ }; do
        [[ " $GER_VALIDATORS_LOCAL_CONFIGS" == *" $_INDEX:"* ]] || continue
        _DIR=$(realpath $DEST/ger_validator_$_INDEX)
        echo "    docker run --rm --name ger-validator-${_INDEX}-debug --network host -v $_DIR:$_DIR:ro \\"
        echo "      --entrypoint dlv $GER_VALIDATOR_DEBUG_IMAGE exec /usr/local/bin/ger_validator_signer \\"
        echo "      --headless --listen=:$((GER_VALIDATOR_DEBUG_BASE_PORT + _INDEX)) --api-version=2 --accept-multiclient --continue \\"
        echo "      -- run --cfg $_DIR/config.toml"
    done
    echo "  or without docker, from a terminal (or with the \"Debug ger_validator_signer\" entry of launch.json):"
    for _INDEX in ${LOCAL_GER_VALIDATORS//,/ }; do
        [[ " $GER_VALIDATORS_LOCAL_CONFIGS" == *" $_INDEX:"* ]] || continue
        echo "    (cd tools/ger_validator_signer && go run ./cmd run --cfg $(realpath $DEST/ger_validator_$_INDEX/config.toml))"
    done
    echo " "
}
###############################################################################
function print_ger_validators_launch_json() {
    [ -z "$GER_VALIDATORS_LOCAL_CONFIGS" ] && return 0
    local _INDEX
    for _INDEX in ${LOCAL_GER_VALIDATORS//,/ }; do
        [[ " $GER_VALIDATORS_LOCAL_CONFIGS" == *" $_INDEX:"* ]] || continue
        cat <<EOF
            {
                "name": "Attach ger_validator_signer $_INDEX (docker debug image)",
                "type": "go",
                "request": "attach",
                "mode": "remote",
                "host": "127.0.0.1",
                "port": $((GER_VALIDATOR_DEBUG_BASE_PORT + _INDEX)),
                "substitutePath": [
                    { "from": "\${workspaceFolder}/tools/ger_validator_signer", "to": "/app" },
                ]
            },
            {
                "name": "Debug ger_validator_signer $_INDEX",
                "type": "go",
                "request": "launch",
                "mode": "auto",
                "program": "\${workspaceFolder}/tools/ger_validator_signer/cmd",
                "cwd": "\${workspaceFolder}/tools/ger_validator_signer",
                "args":[
                    "run",
                    "--cfg", "$(realpath $DEST/ger_validator_$_INDEX/config.toml)",
                ]
            },
EOF
    done
}
###############################################################################
function check_rendered_file() {
    local _TEMPLATE="$1"
    local _RENDERED="$2"
    grep "<no value>" $_RENDERED >/dev/null
    if [ $? -ne 1 ]; then
        log_error "some values are not set, check $_TEMPLATE"
        echo ""
        echo "missing keys in rendered template: $_RENDERED"
        echo " "
        grep "<no value>" $_RENDERED
        exit 1
    fi
}
###############################################################################
function check_generated_config_file() {
    check_rendered_file $ORIG_TEMPLATE_FILE $DEST_TEMPLATE_FILE
}
###############################################################################
function parse_command_line_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
        -h | --help)
            echo "Usage: $0"
            echo "  -h: help"
            echo "  -e: kurtosis enclave name (default $KURTOSIS_ENCLAVE) (--enclave)"
            echo "  -p: expose same ports as service in kurtosis (RPC and REST) (--expose-ports) "
            echo "  -s: skip setting up aggsender committee override URLs (--skip-committee-override)"
            echo "  -g: GER validators (AggOracleCommittee v2) to run locally, comma separated indexes, or \"none\""
            echo "      (default $LOCAL_GER_VALIDATORS) (--local-ger-validators)"
            exit 0
            ;;
        -g | --local-ger-validators)
            LOCAL_GER_VALIDATORS=$2
            shift
            shift
            ;;
        -e | --enclave)
            KURTOSIS_ENCLAVE=$2
            shift
            shift
            ;;
        -p | --expose-ports)
            export EXPOSE_PORTS=0
            shift
            ;;
        -s | --skip-committee-override)
            export SKIP_COMMITTEE_OVERRIDE=1
            shift
            ;;
        -*)
            echo "Invalid Option: $1" 1>&2
            exit 1
            ;;
        esac
    done
}
###############################################################################
function main(){
    [ -z $ORIG_TEMPLATE_FILE ] && echo "missing ORIG_TEMPLATE_FILE var" && exit 1
    [ -z $DEST_TEMPLATE_FILE ] && echo "missing DEST_TEMPLATE_FILE var" && exit 1
    [ -z $DEST ] && echo "missing DEST var" && exit 1

    set -o pipefail # enable strict command pipe error detection
    parse_command_line_args $*
    check_requirements
    create_dest_folder
    if [ -z "$SKIP_COMMITTEE_OVERRIDE" ]; then
        log_debug "Setting up aggsender_committee_override_urls with validators in kurtosis enclave"
         common_aggsender_committee_override_urls
    else
        log_debug "skipping setup of aggsender_committee_override_urls with validators in kurtosis enclave, setting it to empty"    
        export aggsender_committee_override_urls="{}"
    fi
    download_kurtosis_artifacts

    export_ports_from_kurtosis
    export_values_of_aggkit_config $DEST/config.toml
    export_forced_values
    # After the L1 RPC and the values of the aggkit config, that the GER validators use
    common_ger_validators
    # Generate config file
    go run scripts/run_template.go $ORIG_TEMPLATE_FILE >$DEST_TEMPLATE_FILE
    ok_or_fatal "Error generating template"

    check_generated_config_file

    echo " "
    echo "file generated at:" $DEST/test.kurtosis.toml

    echo "- to restart kurtosis:"
    echo "    kurtosis clean --all; kurtosis run --enclave $KURTOSIS_ENCLAVE --args-file params.yml --image-download always ."
    echo " "
    echo "- Stop aggkit:"
    echo "    kurtosis service stop $KURTOSIS_ENCLAVE $AGGKIT_SERVICE"
    echo " "
    print_ger_validators_instructions
    echo "- Add next configuration to vscode launch.json"
    echo " -----------------------------------------------------------"
    cat <<EOF
            {
                "name": "Debug aggkit",
                "type": "go",
                "request": "launch",
                "mode": "auto",
                "program": "cmd/",
                "cwd": "\${workspaceFolder}",
                "args":[
                    "run",
                    "-cfg", "$DEST_TEMPLATE_FILE",
                    "-components", "$AGGKIT_LOCAL_COMPONENTS",
                ]
            },
EOF
    print_ger_validators_launch_json
    echo " -----------------------------------------------------------"
    echo " "
    echo " - rembember to clean previous execution data: "
    echo "     rm -Rf ${zkevm_path_rw_data}/*"
}
