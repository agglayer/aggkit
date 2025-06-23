#!/usr/bin/env bash
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

trap 'log_error "Script failed at line $LINENO"' ERR

if [ "$#" -lt 2 ]; then
    echo "Usage: $0 <test_type: single-l2-network-fork12-op-succinct | single-l2-network-fork12-pessimistic | multi-l2-networks-2-chains | multi-l2-networks-3-chains> <kurtosis_repo> [e2e_repo]"
    exit 1
fi

TEST_TYPE=$1
KURTOSIS_FOLDER=$2
E2E_FOLDER=${3:-""}

PROJECT_ROOT="$PWD"
log_info "Starting local E2E setup..."

if [ "$(docker images -q aggkit:local | wc -l)" -eq 0 ]; then
    log_info "Building aggkit:local docker image..."
    pushd "$PROJECT_ROOT" >/dev/null
    make build-docker
    make build-tools
    chmod +x "./target/aggsender_find_imported_bridge"
    popd >/dev/null
else
    log_info "Docker image aggkit:local already exists."
fi

log_info "Using provided Kurtosis CDK repo at: $KURTOSIS_FOLDER"

pushd "$KURTOSIS_FOLDER" >/dev/null
log_info "Cleaning any existing Kurtosis enclaves..."
kurtosis clean --all

log_info "Starting Kurtosis enclave"
case "$TEST_TYPE" in
single-l2-network-fork12-op-succinct)
    ENCLAVE_NAME="op"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_single_chain_fork12_op_succinct_args.json" .
    ;;
single-l2-network-fork12-pessimistic)
    ENCLAVE_NAME="aggkit"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_single_chain_fork12_pessimistic_args.json" .
    ;;
multi-l2-networks-2-chains)
    ENCLAVE_NAME="aggkit"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_1.json" .
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_2.json" .
    ;;
multi-l2-networks-3-chains)
    ENCLAVE_NAME="aggkit"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_3.json" .
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_4.json" .
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_5.json" .
    ;;
*)
    log_error "Unknown test type: $TEST_TYPE"
    exit 1
    ;;
esac
log_info "$ENCLAVE_NAME enclave started successfully."
popd >/dev/null

if [ -n "$E2E_FOLDER" ]; then
    if [ ! -d "$E2E_FOLDER" ]; then
        log_error "The provided E2E folder does not exist: $E2E_FOLDER"
        exit 1
    fi
    
    log_info "Using provided Agglayer E2E repo at: $E2E_FOLDER"

    imported_bridges_tool="./target/aggsender_find_imported_bridge"
    if [ ! -f "$imported_bridges_tool" ]; then
        log_error "The aggsender imported bridges monitor tool is not built. Expected path: $imported_bridges_tool"
        exit 1
    fi

    cp "$imported_bridges_tool" "$E2E_FOLDER/aggsender_find_imported_bridge"
    chmod +x "$E2E_FOLDER/aggsender_find_imported_bridge"

    pushd "$E2E_FOLDER" >/dev/null

    log_info "Setting up e2e environment..."
    set -a
    source ./tests/.env
    set +a

    export BATS_LIB_PATH="$PWD/core/helpers/lib"
    export PROJECT_ROOT="$PWD"
    export ENCLAVE="$ENCLAVE_NAME"
    export AGGSENDER_IMPORTED_BRIDGE_PATH="$E2E_FOLDER/aggsender_find_imported_bridge"

    log_info "Running BATS E2E tests..."
    case "$TEST_TYPE" in
    single-l2-network-fork12-op-succinct)
        bats \
            ./tests/aggkit/bridge-e2e.bats \
            ./tests/aggkit/e2e-pp.bats \
            ./tests/aggkit/bridge-sovereign-chain-e2e.bats \
            ./tests/aggkit/claim-call-data.bats
        ;;
    single-l2-network-fork12-pessimistic)
        bats \
            ./tests/aggkit/bridge-e2e-custom-gas.bats \
            ./tests/aggkit/bridge-e2e.bats \
            ./tests/aggkit/e2e-pp.bats \
            ./tests/aggkit/claim-call-data.bats
        ;;
    multi-l2-networks-2-chains)
        bats ./tests/aggkit/bridge-e2e-2-l2s.bats
        ;;
    multi-l2-networks-3-chains)
        bats ./tests/aggkit/bridge-e2e-3-chains.bats
        ;;
    esac
    rm -f aggsender_find_imported_bridge combined.json rollup_params.json
    popd >/dev/null
    log_info "E2E tests executed."
else
    log_info "E2E_FOLDER not provided, skipping tests."
fi
