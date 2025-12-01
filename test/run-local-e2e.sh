#!/usr/bin/env bash
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $*"; }
log_warn() { echo -e "${ORANGE}[WARN]${NC} $*"; }
log_error() { echo -e "${RED}[ERROR]${NC} $*"; }

trap 'log_error "Script failed at line $LINENO"' ERR

if [ "$#" -lt 3 ]; then
    echo "Usage: $0 <test_type: single-l2-network-op-succinct | single-l2-network-op-succinct-aggoracle-committee | single-l2-network-op-pessimistic | multi-l2-networks-2-chains-op-pessimistic | multi-l2-networks-3-chains-cdk-erigon-pessimistic> <kurtosis_repo_path> <e2e_repo_path>"
    echo ""
    echo "Arguments:"
    echo "  test_type           Type of test to run"
    echo "  kurtosis_repo_path  Path to Kurtosis CDK repo (use '-' to skip setup)"
    echo "  e2e_repo_path       Path to E2E repo (use '-' to skip tests)"
    echo ""
    echo "Examples:"
    echo "  $0 single-l2-network-op-succinct /path/to/kurtosis-repo /path/to/e2e-repo   # Run both setup and tests"
    echo "  $0 single-l2-network-op-succinct /path/to/kurtosis-repo -                   # Run only setup"
    echo "  $0 single-l2-network-op-succinct - /path/to/e2e-repo                        # Run only tests"
    exit 1
fi

TEST_TYPE=$1
KURTOSIS_REPO_PATH=$2
E2E_REPO_PATH=$3

PROJECT_ROOT="$PWD"
log_info "Starting local E2E setup..."

# Set ENCLAVE_NAME based on test type
case "$TEST_TYPE" in
single-l2-network-op-succinct)
    ENCLAVE_NAME="op"
    ;;
single-l2-network-op-succinct-aggoracle-committee)
    ENCLAVE_NAME="op"
    ;;
single-l2-network-op-pessimistic)
    ENCLAVE_NAME="aggkit"
    ;;
multi-l2-networks-2-chains-op-pessimistic)
    ENCLAVE_NAME="aggkit"
    ;;
multi-l2-networks-3-chains-cdk-erigon-pessimistic)
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
        make build-docker-ci
        make build-tools
        chmod +x "./target/aggsender_find_imported_bridge"
        popd >/dev/null
    else
        log_info "Docker image aggkit:local already exists."
    fi
    if docker run --entrypoint /bin/sh --rm aggkit:local > /dev/null 2>&1 ; then
        log_info "Docker image aggkit:local ✅🖥️ have shell."
    else
        log_warn "Docker image aggkit:local ❌🖥️ have no shell (you can generate with shell using make build-docker-ci)."
    fi

    log_info "Using provided Kurtosis CDK repo at: $KURTOSIS_REPO_PATH"

    pushd "$KURTOSIS_REPO_PATH" >/dev/null
    log_info "Cleaning any existing Kurtosis enclaves..."
    kurtosis clean --all

    log_info "Starting Kurtosis enclave"
    case "$TEST_TYPE" in
    single-l2-network-op-succinct)
        jq -s '.[0] * .[1] * .[2]' \
            "$PROJECT_ROOT/.github/test_e2e_op_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_op_succinct_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_single_chain_op_succinct_args.json" > /tmp/single_op_succinct_args.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file "/tmp/single_op_succinct_args.json" .
        ;;
    single-l2-network-op-succinct-aggoracle-committee)
        jq -s '.[0] * .[1] * .[2]' \
            "$PROJECT_ROOT/.github/test_e2e_op_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_op_succinct_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_single_chain_op_succinct_aggoracle_committee_args.json" > /tmp/single_aggoracle_committee_op_succinct.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file "/tmp/single_aggoracle_committee_op_succinct.json" .
        ;;
    single-l2-network-op-pessimistic)
        jq -s '.[0] * .[1]' \
        "$PROJECT_ROOT/.github/test_e2e_op_args_base.json" \
        "$PROJECT_ROOT/.github/test_e2e_op_args_chain_1.json" > /tmp/single_op_pessimistic_args.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/single_op_pessimistic_args.json .
        ;;
    multi-l2-networks-2-chains-op-pessimistic)
        jq -s '.[0] * .[1]' \
            "$PROJECT_ROOT/.github/test_e2e_op_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_op_args_chain_1.json" > /tmp/merged_args_1.json
        jq -s '.[0] * .[1]' \
            "$PROJECT_ROOT/.github/test_e2e_op_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_op_args_chain_2.json" > /tmp/merged_args_2.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_1.json .
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_2.json .
        ;;
    multi-l2-networks-3-chains-cdk-erigon-pessimistic)
        jq -s '.[0] * .[1]' \
            "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_custom_gas_token.json" > /tmp/merged_args_1.json
        jq -s '.[0] * .[1] * .[2]' \
            "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_args_base.json" \
             "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_custom_gas_token.json" \
             "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_multi_chains_args_2.json" > /tmp/merged_args_2.json
        jq -s '.[0] * .[1] * .[2]' \
            "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_args_base.json" \
            "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_custom_gas_token.json" \
            "$PROJECT_ROOT/.github/test_e2e_cdk_erigon_multi_chains_args_3.json" > /tmp/merged_args_3.json
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_1.json .
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_2.json .
        kurtosis run --enclave "$ENCLAVE_NAME" --args-file /tmp/merged_args_3.json .
        ;;
    esac
    log_info "$ENCLAVE_NAME enclave started successfully."
    popd >/dev/null

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

    aggsender_find_imported_bridge_bin="$PROJECT_ROOT/target/aggsender_find_imported_bridge"
    if [ ! -f "$aggsender_find_imported_bridge_bin" ]; then
        log_error "The aggsender imported bridges monitor tool is not built. Expected path: $aggsender_find_imported_bridge_bin"
        exit 1
    fi
    cp "$aggsender_find_imported_bridge_bin" "$E2E_REPO_PATH/aggsender_find_imported_bridge"

    log_info "Using provided E2E repo at: $E2E_REPO_PATH"
    pushd "$E2E_REPO_PATH" >/dev/null

    log_info "Setting up e2e environment..."
    set -a
    source ./tests/.env
    set +a

    export BATS_LIB_PATH="$PWD/core/helpers/lib"
    export PROJECT_ROOT="$PWD"
    export ENCLAVE_NAME="$ENCLAVE_NAME"
    export AGGSENDER_IMPORTED_BRIDGE_PATH="$E2E_REPO_PATH/aggsender_find_imported_bridge"

    log_info "Running BATS E2E tests..."
    case "$TEST_TYPE" in
    single-l2-network-op-succinct)
        bats ./tests/aggkit/latest-n-injected-ger.bats -f "Test invalid GER injection case B2 (FEP mode)" || exit 1
        bats ./tests/aggkit/bridge-e2e.bats || exit 1
        bats ./tests/aggkit/e2e-pp.bats || exit 1
        bats ./tests/aggkit/bridge-sovereign-chain-e2e.bats || exit 1
        bats ./tests/aggkit/bridge-e2e-nightly.bats || exit 1
        bats ./tests/aggkit/internal-claims.bats || exit 1
        bats ./tests/aggkit/claim-reetrancy.bats || exit 1
        bats ./tests/aggkit/aggsender-committee-updates.bats || exit 1
        bats ./tests/op/optimistic-mode.bats || exit 1
        ;;
    single-l2-network-op-succinct-aggoracle-committee)
        bats ./tests/aggkit/bridge-e2e-aggoracle-committee.bats || exit 1
        ;;
    single-l2-network-op-pessimistic)
        bats ./tests/aggkit/latest-n-injected-ger.bats -f "Test invalid GER injection case B2 (PP mode)" || exit 1
        bats ./tests/aggkit/bridge-e2e.bats || exit 1
        bats ./tests/aggkit/e2e-pp.bats || exit 1
        bats ./tests/aggkit/bridge-sovereign-chain-e2e.bats || exit 1
        bats ./tests/op/optimistic-mode.bats || exit 1
        bats ./tests/aggkit/bridge-e2e-nightly.bats || exit 1
        bats ./tests/aggkit/internal-claims.bats || exit 1
        bats ./tests/aggkit/claim-reetrancy.bats || exit 1
        bats ./tests/aggkit/aggsender-committee-updates.bats || exit 1
        ;;
    multi-l2-networks-2-chains-op-pessimistic)
        bats ./tests/aggkit/bridge-e2e-2-chains.bats || exit 1
        ;;
    multi-l2-networks-3-chains-cdk-erigon-pessimistic)
        bats ./tests/aggkit/bridge-e2e-3-chains.bats || exit 1
        ;;
    esac
    rm -f aggsender_find_imported_bridge combined.json rollup_params.json
    popd >/dev/null
    log_info "E2E tests executed."
else
    log_info "Skipping E2E tests (e2e_repo_path is '-')"
fi
