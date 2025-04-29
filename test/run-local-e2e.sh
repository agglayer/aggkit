#!/usr/bin/env bash

set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*" | tee -a "$LOG_FILE"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*" | tee -a "$LOG_FILE"
}

trap 'log_error "Script failed at line $LINENO"' ERR

if [ "$#" -ne 3 ]; then
    echo "Usage: $0 <test_type: single-l2-network-fork12-op-succinct | single-l2-network-fork12-pessimistic | multi-l2-networks> <path_to_kurtosis_cdk_repo> <path_to_e2e_repo>"
    exit 1
fi

TEST_TYPE=$1
KURTOSIS_FOLDER=$2
E2E_FOLDER=$3

PROJECT_ROOT="$PWD"
ROOT_FOLDER="/tmp/aggkit-e2e-run"
LOG_FOLDER="$ROOT_FOLDER/logs"
LOG_FILE="$LOG_FOLDER/run-local-e2e.log"

rm -rf "$ROOT_FOLDER"
mkdir -p "$LOG_FOLDER"

# exec > >(tee -a "$LOG_FILE") 2>&1

log_info "Starting local E2E setup..."

# Build aggkit Docker Image if it doesn't exist
if [ "$(docker images -q aggkit:local | wc -l)" -eq 0 ]; then
    log_info "Building aggkit:local docker image..."
    pushd "$PROJECT_ROOT" > /dev/null
    make build-docker
    make build-tools
    chmod +x "./target/aggsender_find_imported_bridge"
    export AGGSENDER_IMPORTED_BRIDGE_PATH="./target/aggsender_find_imported_bridge"
    popd > /dev/null
else
    log_info "Docker image aggkit:local already exists."
fi

log_info "Using provided Kurtosis CDK repo at: $KURTOSIS_FOLDER"

pushd "$KURTOSIS_FOLDER" > /dev/null
log_info "Cleaning any existing Kurtosis enclaves..."
kurtosis clean --all

# Start Kurtosis Enclave 
log_info "Starting Kurtosis enclave"

if [ "$TEST_TYPE" == "single-l2-network-fork12-op-succinct" ]; then
    ENCLAVE_NAME="op"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_single_chain_fork12_op_succinct_args.json" .
elif [ "$TEST_TYPE" == "single-l2-network-fork12-pessimistic" ]; then
    ENCLAVE_NAME="aggkit"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_single_chain_fork12_pessimistic_args.json" .
elif [ "$TEST_TYPE" == "multi-l2-networks" ]; then
    ENCLAVE_NAME="aggkit"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_1.json" .
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_2.json" .
else
    log_error "Unknown test type: $TEST_TYPE"
    exit 1
fi

log_info "$ENCLAVE_NAME enclave started successfully."
popd > /dev/null

log_info "Using provided Agglayer E2E repo at: $E2E_FOLDER"

pushd "$E2E_FOLDER" > /dev/null

# Setup environment
log_info "Setting up e2e environment..."
set -a
source ./tests/.env
set +a

export BATS_LIB_PATH="$PWD/core/helpers/lib"
export PROJECT_ROOT="$PWD"
export ENCLAVE="$ENCLAVE_NAME"
export DISABLE_L2_FUND="true"

log_info "Running BATS E2E tests..."
if [ "$TEST_TYPE" == "single-l2-network-fork12-op-succinct" ]; then
    bats ./tests/aggkit/bridge-e2e.bats
elif [ "$TEST_TYPE" == "single-l2-network-fork12-pessimistic" ]; then
    bats ./tests/aggkit/bridge-e2e.bats ./tests/aggkit/bridge-e2e-custom-gas.bats
elif [ "$TEST_TYPE" == "multi-l2-networks" ]; then
    bats ./tests/aggkit/bridge-l2_to_l2-e2e.bats
fi

popd > /dev/null
log_info "E2E tests executed. Logs saved to $LOG_FILE"
