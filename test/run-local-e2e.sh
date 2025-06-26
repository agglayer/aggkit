#!/usr/bin/env bash
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

trap 'log_error "Script failed at line $LINENO"' ERR

if [ "$#" -lt 3 ]; then
    echo "Usage: $0 <test_type: single-l2-network-fork12-op-succinct | single-l2-network-fork12-pessimistic | multi-l2-networks-2-chains | multi-l2-networks-3-chains> <kurtosis_repo_path> <e2e_repo_path>"
    echo ""
    echo "Arguments:"
    echo "  test_type           Type of test to run"
    echo "  kurtosis_repo_path  Path to Kurtosis CDK repo (use '-' to skip setup)"
    echo "  e2e_repo_path       Path to E2E repo (use '-' to skip tests)"
    echo ""
    echo "Examples:"
    echo "  $0 single-l2-network-fork12-op-succinct /path/to/kurtosis-repo /path/to/e2e-repo    # Run both setup and tests"
    echo "  $0 single-l2-network-fork12-op-succinct /path/to/kurtosis-repo -                    # Run only setup"
    echo "  $0 single-l2-network-fork12-op-succinct - /path/to/e2e-repo                         # Run only tests"
    exit 1
fi

TEST_TYPE=$1
KURTOSIS_REPO_PATH=$2
E2E_REPO_PATH=$3

PROJECT_ROOT="$PWD"
log_info "Starting local E2E setup..."

# Set ENCLAVE_NAME based on test type
case "$TEST_TYPE" in
single-l2-network-fork12-op-succinct)
    ENCLAVE_NAME="op"
    ;;
single-l2-network-fork12-pessimistic)
    ENCLAVE_NAME="aggkit"
    ;;
multi-l2-networks-2-chains)
    ENCLAVE_NAME="aggkit"
    ;;
multi-l2-networks-3-chains)
    ENCLAVE_NAME="aggkit"
    ;;
*)
    log_error "Unknown test type: $TEST_TYPE"
    exit 1
    ;;
esac

# Run Kurtosis setup if path is provided and not '-'
if [ "$KURTOSIS_REPO_PATH" != "-" ]; then
    if [ ! -d "$KURTOSIS_REPO_PATH" ]; then
        log_error "The provided Kurtosis repo path does not exist: $KURTOSIS_REPO_PATH"
        exit 1
    fi

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

    log_info "Using provided Kurtosis CDK repo at: $KURTOSIS_REPO_PATH"

    pushd "$KURTOSIS_REPO_PATH" >/dev/null
    log_info "Cleaning any existing Kurtosis enclaves..."
    kurtosis clean --all

    log_info "Starting Kurtosis enclave"
    case "$TEST_TYPE" in
    single-l2-network-fork12-op-succinct)
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$PROJECT_ROOT/.github/test_e2e_single_chain_fork12_op_succinct_args.json" .
        ;;
    single-l2-network-fork12-pessimistic)
        jq -s '.[0] * .[1]' "$PROJECT_ROOT/.github/test_e2e_cdk_args_base.json" "$PROJECT_ROOT/.github/test_e2e_gas_token_enabled_args.json" > /tmp/merged_args_1.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_1.json .
        ;;
    multi-l2-networks-2-chains)
        # Create merged args files using jq
        jq -s '.[0] * .[1]' "$PROJECT_ROOT/.github/test_e2e_cdk_args_base.json" "$PROJECT_ROOT/.github/test_e2e_gas_token_enabled_args.json" > /tmp/merged_args_1.json
        jq -s '.[0] * .[1]' "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_2.json" "$PROJECT_ROOT/.github/test_e2e_gas_token_enabled_args.json" > /tmp/merged_args_2.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_1.json .
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_2.json .
        ;;
    multi-l2-networks-3-chains)
        # Create merged args files using jq
        jq -s '.[0] * .[1]' "$PROJECT_ROOT/.github/test_e2e_cdk_args_base.json" "$PROJECT_ROOT/.github/test_e2e_gas_token_enabled_args.json" > /tmp/merged_args_1.json
        jq -s '.[0] * .[1]' "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_2.json" "$PROJECT_ROOT/.github/test_e2e_gas_token_enabled_args.json" > /tmp/merged_args_2.json
        jq -s '.[0] * .[1]' "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_2.json" "$PROJECT_ROOT/.github/test_e2e_multi_chains_args_3.json" > /tmp/merged_args_3.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_1.json .
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_2.json .
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_3.json .
        ;;
    esac
    log_info "$ENCLAVE_NAME enclave started successfully."
    popd >/dev/null

    # Clean up temporary merged args files if they exist
    rm -f /tmp/merged_args_*.json
else
    log_info "Skipping Kurtosis setup (kurtosis_repo_path is '-')"
fi

# Run E2E tests if path is provided and not '-'
if [ "$E2E_REPO_PATH" != "-" ]; then
    if [ ! -d "$E2E_REPO_PATH" ]; then
        log_error "The provided E2E folder does not exist: $E2E_REPO_PATH"
        exit 1
    fi

    log_info "Using provided Agglayer E2E repo at: $E2E_REPO_PATH"

    aggsender_find_imported_bridge_bin="./target/aggsender_find_imported_bridge"
    if [ ! -f "$aggsender_find_imported_bridge_bin" ]; then
        log_error "The aggsender imported bridges monitor tool is not built. Expected path: $aggsender_find_imported_bridge_bin"
        exit 1
    fi

    cp "$aggsender_find_imported_bridge_bin" "$E2E_REPO_PATH/aggsender_find_imported_bridge"
    chmod +x "$E2E_REPO_PATH/aggsender_find_imported_bridge"

    pushd "$E2E_REPO_PATH" >/dev/null

    log_info "Setting up e2e environment..."
    set -a
    source ./tests/.env
    set +a

    export BATS_LIB_PATH="$PWD/core/helpers/lib"
    export PROJECT_ROOT="$PWD"
    export ENCLAVE="$ENCLAVE_NAME"
    export AGGSENDER_IMPORTED_BRIDGE_PATH="$E2E_REPO_PATH/aggsender_find_imported_bridge"

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
    log_info "Skipping E2E tests (e2e_repo_path is '-')"
fi
