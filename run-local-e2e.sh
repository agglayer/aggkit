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

if [ "$#" -ne 1 ]; then
    echo "Usage: $0 <test_type: single-chain | multi-chain>"
    exit 1
fi
TEST_TYPE=$1

SCRIPT_DIR="$(cd -- "$(dirname -- "$0")" && pwd)"
ROOT_FOLDER="/tmp/aggkit-e2e-run"
KURTOSIS_FOLDER="$ROOT_FOLDER/kurtosis-cdk"
E2E_FOLDER="$ROOT_FOLDER/agglayer-e2e"
LOG_FOLDER="$ROOT_FOLDER/logs"
LOG_FILE="$LOG_FOLDER/run-local-e2e.log"

rm -rf "$ROOT_FOLDER"
mkdir -p "$KURTOSIS_FOLDER" "$E2E_FOLDER" "$LOG_FOLDER"

exec > >(tee -a "$LOG_FILE") 2>&1

log_info "Starting local E2E setup..."

ENCLAVE_NAME="aggkit"
KURTOSIS_CDK_REPO="https://github.com/0xPolygon/kurtosis-cdk.git"
AGGLAYER_E2E_REPO="https://github.com/agglayer/e2e.git"

# Build aggkit Docker Image if it doesn't exist
if [ "$(docker images -q aggkit:local | wc -l)" -eq 0 ]; then
    log_info "Building aggkit:local docker image..."
    pushd "$SCRIPT_DIR/.." > /dev/null
    make build-docker
    popd > /dev/null
else
    log_info "Docker image aggkit:local already exists."
fi

log_info "Cloning kurtosis-cdk repo..."
git clone "$KURTOSIS_CDK_REPO" "$KURTOSIS_FOLDER"
pushd "$KURTOSIS_FOLDER" > /dev/null
git checkout arpit/bridge-service
log_info "Cleaning any existing Kurtosis enclaves..."
kurtosis clean --all

# Start Kurtosis Enclave 
log_info "Starting Kurtosis enclave: $ENCLAVE_NAME"

if [ "$TEST_TYPE" == "single-chain" ]; then
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$SCRIPT_DIR/.github/test_e2e_single_chain_args.json" .
elif [ "$TEST_TYPE" == "multi-chain" ]; then
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$SCRIPT_DIR/.github/test_e2e_multi_chain_args_1.json" .
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$SCRIPT_DIR/.github/test_e2e_multi_chain_args_2.json" .

    make build-tools
    chmod +x "./target/aggsender_find_imported_bridge"
    export AGGSENDER_IMPORTED_BRIDGE_PATH="./target/aggsender_find_imported_bridge"
else
    log_error "Unknown test type: $TEST_TYPE"
    exit 1
fi

log_info "Aggkit enclave started successfully."
popd > /dev/null

log_info "Cloning agglayer e2e repo..."
git clone "$AGGLAYER_E2E_REPO" "$E2E_FOLDER"
pushd "$E2E_FOLDER" > /dev/null

# Setup environment
log_info "Setting up e2e environment..."
set -a
source ./tests/.env
set +a

export BATS_LIB_PATH="$PWD/core/helpers/lib"
export PROJECT_ROOT="$PWD"
export ENCLAVE="$ENCLAVE_NAME"

log_info "Running BATS E2E tests..."
if [ "$TEST_TYPE" == "single-chain" ]; then
    git checkout 39fb3daa4795c54e6ddd827b8017587de0db5018
    bats ./tests/aggkit/bridge-e2e.bats ./tests/aggkit/bridge-e2e-msg.bats ./tests/aggkit/e2e-pp.bats
elif [ "$TEST_TYPE" == "multi-chain" ]; then
    git checkout 22665185bde0d441089c49ba68cd70ca369cc5d9
    bats ./tests/aggkit/bridge-l2_to_l2-e2e.bats
fi

popd > /dev/null
log_info "E2E tests executed. Logs saved to $LOG_FILE"
