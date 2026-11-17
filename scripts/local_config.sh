#!/bin/bash
###############################################################################
# local_config.sh <role> [options]
#
# Generates the config to run ONE aggkit role locally (from VSCode) against a running kurtosis enclave, replacing the
# same role of the enclave (--apply stops it in kurtosis) or running in parallel with it.
#
# Roles:
#   aggoracle   local components "aggoracle". Replaces the AggOracle of aggkit-<network>. With the AggOracleCommittee
#               v2 it sets Mode = committee and the ValidatorURLOverrides of the GER validators (-g: the ones to run
#               locally, default none: all of them stay in kurtosis)
#   aggsender   local components "aggsender". Replaces the aggsender of aggkit-<network> (it keeps the override
#               URLs of the aggsender validators, -s skips them)
#   bridge      local components "bridge". Runs IN PARALLEL with the enclave (it replaces nothing): own data dir and
#               own ports
#   ger-validator  the role aggoracle with at least one local GER validator (-g, default 1). A local GER validator only
#               makes sense with a local AggOracle: it is the one that sends the GERs to it (ValidatorURLOverrides), the
#               AggOracle of the enclave keeps asking the validators of kurtosis
#
# Every role has its own directory $TMP_AGGKIT_FOLDER/<role>-<network>/ (config, data and artifacts of kurtosis) and
# its own ports, so several roles can run locally at the same time.
#
# Config template (chain type), see resolve_template_kind. It is chosen from the enclave, --template forces it:
#   1. aggkit-prover-<network> is in the enclave -> fep. The aggchain proof (FEP) is the only one that has a prover.
#   2. the OP stack of the network (op-el + op-cl) is in the enclave -> fep. The FEP template is a superset of the PP
#      one for an OP chain: AggSender.Mode = Auto resolves the mode (PessimisticProof for pessimistic and
#      ecdsa-multisig) and the sections only used by FEP (OptimisticModeConfig, AggchainProofGen) are not read in the
#      other modes. Tested with ecdsa-multisig + op-reth, no prover (local_config_fep.sh and this script).
#   3. otherwise (no op-node, e.g. a cdk-erigon chain) -> pp, the only one that doesn't need the op-node.
#
# To add a role (aggsender-validator, ...): list it in ROLES and define its role_<name>_* hooks (the name
# with "-" as "_"). The mandatory ones are component, slot and replaces; prepare, check_requirements, print_notes and
# print_launch_extra are optional. A role that is not an aggkit process (the GER validator runs ger_validator_signer)
# needs its own template and launch.json entry instead of the aggkit ones.
###############################################################################

KURTOSIS_ENCLAVE=${KURTOSIS_ENCLAVE:-op}
# Role aggoracle: the GER validators that run locally (comma separated indexes, or none). None by default: the AggOracle
# gets the URLs that kurtosis publishes for all of them
LOCAL_GER_VALIDATORS=${LOCAL_GER_VALIDATORS:-none}
source "$(dirname "$0")/local_config_helper.sh"

# The helper and the templates use paths relative to the root of the repo
REPO_ROOT=$(cd "$(dirname "$0")/.." && pwd)
SCRIPT_REL_PATH="./scripts/$(basename "$0")"

ROLES="aggoracle aggsender bridge"
# Roles that need changes in kurtosis-cdk first
PLANNED_ROLES="aggsender-validator"
# Roles that are another role with some options: the GER validator runs with a local AggOracle
ALIAS_ROLES="ger-validator"

# Ports of the servers of the local aggkit: BASE + 10 * index (REST and admin REST) or BASE + index (pprof, prometheus),
# where index = slot of the role + 10 * (network - 1). Slots: bridge 0, aggsender 1, aggoracle 2 (planned: 3 and up)
LOCAL_REST_BASE_PORT=${LOCAL_REST_BASE_PORT:-5577}
LOCAL_ADMIN_REST_BASE_PORT=${LOCAL_ADMIN_REST_BASE_PORT:-5579}
LOCAL_PPROF_BASE_PORT=${LOCAL_PPROF_BASE_PORT:-6060}
LOCAL_PROMETHEUS_BASE_PORT=${LOCAL_PROMETHEUS_BASE_PORT:-9091}

ROLE=""
APPLY=""
RESTORE=""
TEMPLATE_FORCED=""
GER_OPTION_SET=""
TEMPLATE_KIND=""
TEMPLATE_REASON=""

###############################################################################
# Roles. Hooks: role_<role>_<hook>
#  component: the value of -components of the local aggkit
#  slot: number that gives the ports of the role (0..9)
#  replaces: the service of the enclave that runs this component (empty: the role runs in parallel, replaces nothing)
#  check_requirements (optional): tools that the role needs
#  prepare (optional): exports the template values of the role, after the common ones
#  print_notes, print_launch_extra (optional): extra instructions and launch.json entries
###############################################################################
function role_aggoracle_component() { echo "aggoracle"; }
function role_aggoracle_slot() { echo 2; }
function role_aggoracle_replaces() { echo "$AGGKIT_SERVICE"; }
function role_aggoracle_check_requirements() { check_cast_installed; }
function role_aggoracle_prepare() { common_ger_validators; }
function role_aggoracle_print_notes() { print_ger_validators_instructions; }
function role_aggoracle_print_launch_extra() { print_ger_validators_launch_json; }
###############################################################################
function role_aggsender_component() { echo "aggsender"; }
function role_aggsender_slot() { echo 1; }
function role_aggsender_replaces() { echo "$AGGKIT_SERVICE"; }
function role_aggsender_prepare() {
    if [ -z "$SKIP_COMMITTEE_OVERRIDE" ]; then
        log_debug "Setting up aggsender_committee_override_urls with validators in kurtosis enclave"
        common_aggsender_committee_override_urls
    else
        log_debug "skipping setup of aggsender_committee_override_urls, setting it to empty"
        export aggsender_committee_override_urls="{}"
    fi
}
###############################################################################
function role_bridge_component() { echo "bridge"; }
function role_bridge_slot() { echo 0; }
# In parallel: kurtosis keeps its own bridge service (aggkit-<network>-bridge, or the main service when it has the bridge
# component) and this one has its own storage and ports
function role_bridge_replaces() { echo ""; }
###############################################################################
function role_fn() {
    echo "role_${ROLE//-/_}_$1"
}
###############################################################################
function role_call() {
    "$(role_fn "$1")"
}
###############################################################################
function role_call_optional() {
    local _FN
    _FN=$(role_fn "$1")
    if declare -F "$_FN" >/dev/null; then
        "$_FN"
    fi
    return 0
}
###############################################################################
function usage() {
    cat <<EOF
Usage: $SCRIPT_REL_PATH <role> [options]

Generates the config to run one aggkit role locally (from VSCode) against a kurtosis enclave.

Roles:
  aggoracle   components aggoracle. Replaces the AggOracle of aggkit-<network> (AggOracleCommittee v2 aware)
  aggsender   components aggsender. Replaces the aggsender of aggkit-<network>
  bridge      components bridge. Runs in parallel with the enclave, it replaces nothing
  ger-validator  the role aggoracle with local GER validators (-g, default 1): a local GER validator needs a local
              AggOracle, the one of the enclave only asks the validators of kurtosis

Options:
  -h, --help                   this help
  -e, --enclave <name>         kurtosis enclave (default $KURTOSIS_ENCLAVE)
  -n, --network <suffix>       L2 network: suffix of its services and artifacts (default $AGGKIT_NETWORK: aggkit-$AGGKIT_NETWORK,
                               aggkit-config-$AGGKIT_NETWORK, ...)
  -t, --template <fep|pp>      config template (default: chosen from the enclave)
  -p, --expose-ports           REST port of the local aggkit = the one that kurtosis publishes for aggkit-<network>
                               (to replace it; not for the role bridge)
  -s, --skip-committee-override  role aggsender: skip the override URLs of the aggsender validators
  -g, --local-ger-validators <list>  roles aggoracle and ger-validator: GER validators to run locally, comma
                               separated indexes, or none (default: none for aggoracle, 1 for ger-validator)
      --apply                  stop in kurtosis what the role replaces (kurtosis can't stop one component of a
                               service: if the service runs more components it stops nothing, explains what to do and
                               exits with 2)
      --restore                start again in kurtosis what the role replaced (it generates nothing)

Everything the role needs lives in \$TMP_AGGKIT_FOLDER/<role>-<network>/ (now $TMP_AGGKIT_FOLDER/<role>-$AGGKIT_NETWORK/).
Exit codes: 0 ok, 1 error, 2 --apply can't stop what the role replaces.
EOF
}
###############################################################################
function need_option_value() {
    # $1 option, $2 number of remaining arguments
    if [ "$2" -lt 2 ]; then
        log_fatal "the option $1 needs a value (see --help)"
    fi
}
###############################################################################
function parse_args() {
    while [[ $# -gt 0 ]]; do
        case $1 in
        -h | --help)
            usage
            exit 0
            ;;
        -e | --enclave)
            need_option_value "$1" $#
            KURTOSIS_ENCLAVE=$2
            shift 2
            ;;
        -n | --network)
            need_option_value "$1" $#
            AGGKIT_NETWORK=$2
            shift 2
            ;;
        -t | --template)
            need_option_value "$1" $#
            TEMPLATE_FORCED=$2
            shift 2
            ;;
        -p | --expose-ports)
            export EXPOSE_PORTS=0
            shift
            ;;
        -s | --skip-committee-override)
            export SKIP_COMMITTEE_OVERRIDE=1
            shift
            ;;
        -g | --local-ger-validators)
            need_option_value "$1" $#
            LOCAL_GER_VALIDATORS=$2
            GER_OPTION_SET=1
            shift 2
            ;;
        --apply)
            APPLY=1
            shift
            ;;
        --restore)
            RESTORE=1
            shift
            ;;
        -*)
            log_error "Invalid option: $1 (see --help)"
            exit 1
            ;;
        *)
            [ -n "$ROLE" ] && log_fatal "only one role is allowed ($ROLE and $1)"
            ROLE=$1
            shift
            ;;
        esac
    done
}
###############################################################################
function validate_args() {
    if [ -z "$ROLE" ]; then
        usage
        log_fatal "missing the role: one of $ROLES $ALIAS_ROLES"
    fi
    if [ "$ROLE" == "ger-validator" ]; then
        resolve_ger_validator_role
    fi
    if [[ " $PLANNED_ROLES " == *" $ROLE "* ]]; then
        log_fatal "the role $ROLE is not available yet: it needs changes in kurtosis-cdk. Roles: $ROLES"
    fi
    if [[ " $ROLES " != *" $ROLE "* ]]; then
        log_fatal "unknown role [$ROLE]. Roles: $ROLES $ALIAS_ROLES"
    fi
    if [ -n "$APPLY" ] && [ -n "$RESTORE" ]; then
        log_fatal "--apply and --restore can't be used together"
    fi
    if [ -n "$TEMPLATE_FORCED" ] && [ -z "$(template_file_for_kind "$TEMPLATE_FORCED")" ]; then
        log_fatal "invalid template [$TEMPLATE_FORCED]: fep or pp"
    fi
    if ! [[ "$LOCAL_GER_VALIDATORS" =~ ^(none|[0-9]+(,[0-9]+)*)$ ]]; then
        log_fatal "invalid list of GER validators [$LOCAL_GER_VALIDATORS]: comma separated indexes (1,2) or none"
    fi
    if [ -n "$GER_OPTION_SET" ] && [ "$ROLE" != "aggoracle" ]; then
        log_warn "-g only matters for the role aggoracle: ignored"
    fi
    if [ -n "$SKIP_COMMITTEE_OVERRIDE" ] && [ "$ROLE" != "aggsender" ]; then
        log_warn "-s only matters for the role aggsender: ignored"
    fi
    if [ -n "$EXPOSE_PORTS" ] && [ -z "$(role_call replaces)" ]; then
        log_warn "-p ignored: the role $ROLE runs in parallel with the enclave, it needs ports of its own"
        unset EXPOSE_PORTS
    fi
}
###############################################################################
# Role ger-validator = role aggoracle with local GER validators. The GER validator is not replaced in the enclave: the
# local AggOracle sends the requests of those validators to the local ones (ValidatorURLOverrides) and the ones of
# kurtosis are only asked by the AggOracle of the enclave (stopped with --apply, or not deployed)
###############################################################################
function resolve_ger_validator_role() {
    if [ -z "$GER_OPTION_SET" ]; then
        LOCAL_GER_VALIDATORS=1
    elif [ "$LOCAL_GER_VALIDATORS" == "none" ]; then
        log_fatal "the role ger-validator runs at least one GER validator locally: -g none is the role aggoracle"
    fi
    log_info "role ger-validator = role aggoracle with the local GER validators $LOCAL_GER_VALIDATORS: the local" \
        "AggOracle is the one that asks them"
    ROLE=aggoracle
}
###############################################################################
# Sets TEMPLATE_KIND and TEMPLATE_REASON. See the rules at the top of this file
###############################################################################
function resolve_template_kind() {
    if [ -n "$TEMPLATE_FORCED" ]; then
        TEMPLATE_KIND=$TEMPLATE_FORCED
        TEMPLATE_REASON="forced with --template"
    elif [ -n "$(find_kurtosis_service "aggkit-prover-${AGGKIT_NETWORK}")" ]; then
        TEMPLATE_KIND=fep
        TEMPLATE_REASON="aggkit-prover-${AGGKIT_NETWORK} is in the enclave (aggchain proof)"
    elif [ -n "$(find_op_el_service)" ] && [ -n "$(find_op_cl_service)" ]; then
        TEMPLATE_KIND=fep
        TEMPLATE_REASON="no prover, but the OP stack (op-el + op-cl) is: the FEP template covers it"
    else
        TEMPLATE_KIND=pp
        TEMPLATE_REASON="no prover and no op-node in the enclave: the PP template only needs the L2 RPC"
    fi
    ORIG_TEMPLATE_FILE=$(template_file_for_kind "$TEMPLATE_KIND")
    log_debug "template: $TEMPLATE_KIND ($TEMPLATE_REASON) $ORIG_TEMPLATE_FILE"
}
###############################################################################
# The directories of the role, all inside $TMP_AGGKIT_FOLDER/<role>-<network>/ (absolute paths, so the config works from
# any cwd):
#   aggkit-<role>-<network>.toml  the config
#   data/                         PathRWData: sqlite databases...
#   kurtosis/                     artifacts downloaded from the enclave (keystores, config of kurtosis, GER validators)
###############################################################################
function create_role_dirs() {
    ROLE_DIR=$(realpath -m "$TMP_AGGKIT_FOLDER")/${ROLE}-${AGGKIT_NETWORK}
    DEST=$ROLE_DIR/kurtosis
    DATA_DIR=$ROLE_DIR/data
    CONFIG_FILE=$ROLE_DIR/aggkit-${ROLE}-${AGGKIT_NETWORK}.toml
    export DEST
    export zkevm_path_rw_data=$DATA_DIR
    mkdir -p "$DEST" "$DATA_DIR"
    ok_or_fatal "can't create the directories of the role in $ROLE_DIR"
}
###############################################################################
# Ports of the servers of the local aggkit, different for each role and network. -p uses the one of kurtosis for the REST.
###############################################################################
function assign_role_ports() {
    local _INDEX=$(($(role_call slot) + 10 * (10#$AGGKIT_NETWORK - 1)))
    export aggkit_node_rest_api_port=$((LOCAL_REST_BASE_PORT + 10 * _INDEX))
    export aggkit_admin_rest_port=$((LOCAL_ADMIN_REST_BASE_PORT + 10 * _INDEX))
    export aggkit_pprof_port=$((LOCAL_PPROF_BASE_PORT + _INDEX))
    export aggkit_prometheus_port=$((LOCAL_PROMETHEUS_BASE_PORT + _INDEX))
}
###############################################################################
function warn_if_port_in_use() {
    local _NAME=$1
    local _PORT=$2
    command -v ss >/dev/null || return 0
    if [ -n "$(ss -H -ltn "sport = :$_PORT" 2>/dev/null)" ]; then
        log_warn "the port $_PORT ($_NAME) is already in use: another local aggkit (the same role?) or a service of kurtosis"
    fi
}
###############################################################################
function check_role_ports() {
    # With -p the REST port is the one of the kurtosis service, that is in use until it is stopped
    [ -z "$EXPOSE_PORTS" ] && warn_if_port_in_use "REST" "$aggkit_node_rest_api_port"
    warn_if_port_in_use "admin REST" "$aggkit_admin_rest_port"
    warn_if_port_in_use "prometheus" "$aggkit_prometheus_port"
}
###############################################################################
# State of the service of kurtosis: RUNNING, STOPPED... (empty if it isn't in the enclave)
###############################################################################
function get_service_status() {
    local _INSPECT
    _INSPECT=$(kurtosis enclave inspect "$KURTOSIS_ENCLAVE" 2>/dev/null) || return 1
    echo "$_INSPECT" | sed -E 's/\x1b\[[0-9;]*m//g' | awk -v svc="$1" '$2 == svc { print $NF; exit }'
}
###############################################################################
# Components that run in the service (the --components of its cmd), comma separated. Empty if they can't be read
###############################################################################
function get_service_components() {
    local _INSPECT
    _INSPECT=$(kurtosis service inspect "$KURTOSIS_ENCLAVE" "$1" 2>/dev/null) || return 1
    # The cmd can come as --components=a,b or as --components a,b, in a list (yaml or json) or in a line: the quotes and
    # brackets become spaces, so the arguments are separated by spaces (the commas of the components have none)
    echo "$_INSPECT" | sed -E 's/\x1b\[[0-9;]*m//g' | tr "\"'[]" '    ' | tr '\n' ' ' |
        grep -o -E -- '--?components(=| *,? *)[A-Za-z0-9_-]+(,[A-Za-z0-9_-]+)*' | head -n 1 |
        sed -E 's/^--?components(=| *,? *)//'
}
###############################################################################
# What the role replaces in kurtosis. Sets:
#  REPL_SERVICE     the service (empty: the role runs in parallel)
#  REPL_STATUS      RUNNING/STOPPED... of the service
#  REPL_COMPONENTS  components of the service (comma separated)
#  REPL_STATE       one of:
#    parallel          the role replaces nothing
#    missing           the service isn't in the enclave
#    unknown           the state or the components of the service can't be read
#    component-absent  the service doesn't run the component of the role: nothing to stop
#    stoppable         the service runs only the component of the role: stopping it is safe
#    shared            the service runs the component of the role and others: it can't be stopped without them
###############################################################################
function analyze_replacement() {
    REPL_SERVICE=$(role_call replaces)
    REPL_STATUS=""
    REPL_COMPONENTS=""
    if [ -z "$REPL_SERVICE" ]; then
        REPL_STATE=parallel
        return 0
    fi
    if ! REPL_STATUS=$(get_service_status "$REPL_SERVICE"); then
        # kurtosis enclave inspect failed: it is not the same as a service that isn't there
        REPL_STATE=unknown
        return 0
    fi
    if [ -z "$REPL_STATUS" ]; then
        REPL_STATE=missing
        return 0
    fi
    REPL_COMPONENTS=$(get_service_components "$REPL_SERVICE")
    if [ -z "$REPL_COMPONENTS" ]; then
        REPL_STATE=unknown
        return 0
    fi
    local _COMPONENT
    _COMPONENT=$(role_call component)
    if [[ ",$REPL_COMPONENTS," != *",$_COMPONENT,"* ]]; then
        REPL_STATE=component-absent
    elif [ "$REPL_COMPONENTS" == "$_COMPONENT" ]; then
        REPL_STATE=stoppable
    else
        REPL_STATE=shared
    fi
}
###############################################################################
# The kurtosis commands for the roles of the other components of the service (to run them locally too)
###############################################################################
function print_shared_service_guidance() {
    local _COMPONENT
    _COMPONENT=$(role_call component)
    echo "  $REPL_SERVICE runs --components=$REPL_COMPONENTS: kurtosis can't stop only $_COMPONENT, stopping the service"
    echo "  would stop the others too. Two ways:"
    echo "    1) redeploy the enclave with aggkit_components without $_COMPONENT (args file of kurtosis-cdk):"
    echo "         aggkit_components: $(echo ",$REPL_COMPONENTS," | sed -E "s/,$_COMPONENT,/,/; s/^,//; s/,\$//")"
    echo "       then $_COMPONENT doesn't run in kurtosis and there is nothing to stop"
    echo "    2) stop the whole service and run the rest of its components locally too, a role each:"
    echo "         kurtosis service stop $KURTOSIS_ENCLAVE $REPL_SERVICE"
    local _OTHER
    for _OTHER in ${REPL_COMPONENTS//,/ }; do
        [ "$_OTHER" == "$_COMPONENT" ] && continue
        if [[ " $ROLES " == *" $_OTHER "* ]]; then
            echo "         $SCRIPT_REL_PATH $_OTHER -e $KURTOSIS_ENCLAVE -n $AGGKIT_NETWORK"
        else
            echo "         (the component $_OTHER has no local role yet: it would stop running)"
        fi
    done
}
###############################################################################
# What to do in kurtosis with what the role replaces: the commands (--apply and --restore do them)
###############################################################################
function print_replacement_plan() {
    local _COMPONENT
    _COMPONENT=$(role_call component)
    if [[ "$REPL_STATE" == "stoppable" || "$REPL_STATE" == "shared" ]] && [ "$REPL_STATUS" != "RUNNING" ]; then
        echo "- $REPL_SERVICE is $REPL_STATUS (it runs --components=$REPL_COMPONENTS): there is nothing to stop"
        echo "- To start it again ($SCRIPT_REL_PATH $ROLE --restore), after stopping the local one:"
        echo "    kurtosis service start $KURTOSIS_ENCLAVE $REPL_SERVICE"
        echo " "
        return 0
    fi
    case "$REPL_STATE" in
    parallel)
        echo "- It runs in parallel with the enclave: it replaces nothing, so there is nothing to stop or to restore"
        ;;
    missing)
        echo "- $REPL_SERVICE is not in the enclave $KURTOSIS_ENCLAVE: there is nothing to stop"
        ;;
    unknown)
        log_warn "can't read the state or the --components of $REPL_SERVICE (kurtosis service inspect $KURTOSIS_ENCLAVE $REPL_SERVICE)"
        echo "- If $REPL_SERVICE runs $_COMPONENT, stop it (it stops all of its components) and start it again later:"
        echo "    kurtosis service stop $KURTOSIS_ENCLAVE $REPL_SERVICE"
        echo "    kurtosis service start $KURTOSIS_ENCLAVE $REPL_SERVICE"
        ;;
    component-absent)
        echo "- $REPL_SERVICE runs only $REPL_COMPONENTS: $_COMPONENT doesn't run in kurtosis, there is nothing to stop"
        ;;
    stoppable)
        echo "- $REPL_SERVICE (state $REPL_STATUS) runs only $_COMPONENT: stop it so it doesn't run twice ($SCRIPT_REL_PATH $ROLE --apply):"
        echo "    kurtosis service stop $KURTOSIS_ENCLAVE $REPL_SERVICE"
        echo "- To restore it ($SCRIPT_REL_PATH $ROLE --restore), after stopping the local one:"
        echo "    kurtosis service start $KURTOSIS_ENCLAVE $REPL_SERVICE"
        ;;
    shared)
        echo "- $_COMPONENT can't be stopped alone in kurtosis ($SCRIPT_REL_PATH $ROLE --apply would not stop anything):"
        print_shared_service_guidance
        echo "- To restore the service: kurtosis service start $KURTOSIS_ENCLAVE $REPL_SERVICE"
        ;;
    esac
    echo " "
}
###############################################################################
# --apply: stops in kurtosis what the role replaces. Returns 0 (done, or nothing to stop), 1 (error) or 2 (it can't be
# stopped without stopping other components)
###############################################################################
function apply_replacement() {
    local _COMPONENT
    _COMPONENT=$(role_call component)
    echo "--apply:"
    case "$REPL_STATE" in
    parallel)
        echo "  the role $ROLE runs in parallel with the enclave: nothing to stop"
        return 0
        ;;
    missing)
        echo "  $REPL_SERVICE is not in the enclave $KURTOSIS_ENCLAVE: nothing to stop"
        return 0
        ;;
    unknown)
        log_error "can't read the state or the components of $REPL_SERVICE: not stopping anything. Check them with:"
        echo "    kurtosis service inspect $KURTOSIS_ENCLAVE $REPL_SERVICE"
        return 1
        ;;
    component-absent)
        echo "  $REPL_SERVICE runs only $REPL_COMPONENTS: $_COMPONENT doesn't run in kurtosis, nothing to stop"
        return 0
        ;;
    esac
    # stoppable or shared
    if [ "$REPL_STATUS" != "RUNNING" ]; then
        echo "  $REPL_SERVICE is already $REPL_STATUS: nothing to stop"
        return 0
    fi
    if [ "$REPL_STATE" == "shared" ]; then
        log_error "not stopping $REPL_SERVICE: $_COMPONENT shares the service with other components ($REPL_COMPONENTS)"
        print_shared_service_guidance
        return 2
    fi
    kurtosis service stop "$KURTOSIS_ENCLAVE" "$REPL_SERVICE" >/dev/null
    if [ $? -ne 0 ]; then
        log_error "kurtosis service stop $KURTOSIS_ENCLAVE $REPL_SERVICE failed"
        return 1
    fi
    echo "  stopped $REPL_SERVICE (state now: $(get_service_status "$REPL_SERVICE")). Start the local $ROLE now"
    echo "  To start it again: $SCRIPT_REL_PATH $ROLE --restore"
    return 0
}
###############################################################################
# --restore: starts in kurtosis what the role replaced
###############################################################################
function restore_replacement() {
    local _SERVICE _STATUS _COMPONENTS _OTHER
    _SERVICE=$(role_call replaces)
    if [ -z "$_SERVICE" ]; then
        echo "--restore: the role $ROLE runs in parallel with the enclave: it replaced nothing, nothing to start"
        echo "  (stop the local aggkit of the role from VSCode if it is running)"
        return 0
    fi
    _STATUS=$(get_service_status "$_SERVICE")
    if [ -z "$_STATUS" ]; then
        log_error "$_SERVICE is not in the enclave $KURTOSIS_ENCLAVE"
        return 1
    fi
    _COMPONENTS=$(get_service_components "$_SERVICE")
    if [ "$_STATUS" == "RUNNING" ]; then
        echo "--restore: $_SERVICE is already RUNNING: nothing to start"
    else
        kurtosis service start "$KURTOSIS_ENCLAVE" "$_SERVICE" >/dev/null
        if [ $? -ne 0 ]; then
            log_error "kurtosis service start $KURTOSIS_ENCLAVE $_SERVICE failed"
            return 1
        fi
        echo "--restore: started $_SERVICE (state now: $(get_service_status "$_SERVICE"))"
    fi
    echo "  Stop the local aggkit of the roles that run the same components, so they don't run twice:"
    for _OTHER in ${_COMPONENTS//,/ }; do
        [[ " $ROLES " == *" $_OTHER "* ]] && echo "    role $_OTHER ($TMP_AGGKIT_FOLDER/$_OTHER-$AGGKIT_NETWORK)"
    done
    return 0
}
###############################################################################
function print_launch_json_entry() {
    cat <<EOF
            {
                "name": "Debug aggkit $ROLE-$AGGKIT_NETWORK",
                "type": "go",
                "request": "launch",
                "mode": "auto",
                "program": "cmd/",
                "cwd": "\${workspaceFolder}",
                "args":[
                    "run",
                    "-cfg", "$CONFIG_FILE",
                    "-components", "$(role_call component)",
                ]
            },
EOF
}
###############################################################################
function print_summary() {
    echo " "
    echo "Role $ROLE (enclave $KURTOSIS_ENCLAVE, network $AGGKIT_NETWORK, template $TEMPLATE_KIND: $TEMPLATE_REASON)"
    echo "- Config generated at:"
    echo "    $CONFIG_FILE"
    echo "- Data dir (PathRWData): $DATA_DIR"
    echo "  Ports of the local aggkit: REST $aggkit_node_rest_api_port, admin REST $aggkit_admin_rest_port," \
        "pprof $aggkit_pprof_port (disabled), prometheus $aggkit_prometheus_port"
    echo " "
    print_replacement_plan
    role_call_optional print_notes
    echo "- Add next configuration to vscode launch.json"
    echo " -----------------------------------------------------------"
    print_launch_json_entry
    role_call_optional print_launch_extra
    echo " -----------------------------------------------------------"
    echo " "
    echo "- To clean the data of the previous executions of the role (the aggkit doesn't create the directory):"
    echo "    rm -rf \"$DATA_DIR\" && mkdir -p \"$DATA_DIR\""
    if [ "$ROLE" == "bridge" ]; then
        echo " "
        echo "- Check it: curl http://localhost:$aggkit_node_rest_api_port/bridge/v1/sync-status"
    fi
}
###############################################################################
# The service of aggkit of the network must be in the enclave (a wrong -n or -e is the usual mistake)
###############################################################################
function check_network_in_enclave() {
    local _STATUS
    _STATUS=$(get_service_status "$AGGKIT_SERVICE") || log_fatal "can't read the services of the enclave $KURTOSIS_ENCLAVE"
    if [ -z "$_STATUS" ]; then
        log_fatal "the service $AGGKIT_SERVICE is not in the enclave $KURTOSIS_ENCLAVE: check -e/--enclave and -n/--network"
    fi
}
###############################################################################
function main() {
    set -o pipefail # enable strict command pipe error detection
    parse_args "$@"
    set_network_names
    validate_args
    cd "$REPO_ROOT" || log_fatal "can't cd to $REPO_ROOT"

    check_kurtosis_installed
    check_enclave
    check_network_in_enclave
    if [ -n "$RESTORE" ]; then
        restore_replacement
        exit $?
    fi
    role_call_optional check_requirements

    resolve_template_kind
    create_role_dirs
    assign_role_ports
    # Defaults of the values that only some roles set (the templates have them all)
    export aggoracle_mode="auto"
    export aggoracle_validator_url_overrides="[]"
    export aggsender_committee_override_urls="{}"
    check_role_ports
    download_kurtosis_artifacts

    export_ports_from_kurtosis
    export_values_of_aggkit_config "$DEST/config.toml"
    export_forced_values
    # After the L1 RPC and the values of the aggkit config, that the GER validators use
    role_call_optional prepare

    go run scripts/run_template.go "$ORIG_TEMPLATE_FILE" >"$CONFIG_FILE"
    ok_or_fatal "Error generating template"
    check_rendered_file "$ORIG_TEMPLATE_FILE" "$CONFIG_FILE"
    # It has the password of the keystores
    chmod 600 "$CONFIG_FILE"

    analyze_replacement
    print_summary
    if [ -n "$APPLY" ]; then
        apply_replacement
        exit $?
    fi
}

# Not run when it is sourced (to test the functions)
if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
