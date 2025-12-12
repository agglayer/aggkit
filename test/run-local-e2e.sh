#!/usr/bin/env bash
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn() { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# Variables for temporary clones
TEMP_KURTOSIS_DIR=""
TEMP_E2E_DIR=""

cleanup_temp_dirs() {
    if [ -n "$TEMP_KURTOSIS_DIR" ] && [ -d "$TEMP_KURTOSIS_DIR" ]; then
        log_info "Cleaning up temporary Kurtosis clone at: $TEMP_KURTOSIS_DIR"
        rm -rf "$TEMP_KURTOSIS_DIR"
    fi
    if [ -n "$TEMP_E2E_DIR" ] && [ -d "$TEMP_E2E_DIR" ]; then
        log_info "Cleaning up temporary E2E clone at: $TEMP_E2E_DIR"
        rm -rf "$TEMP_E2E_DIR"
    fi
}

trap 'cleanup_temp_dirs; log_error "Script failed at line $LINENO"' ERR
trap 'cleanup_temp_dirs' EXIT

if [ "$#" -lt 1 ]; then
    echo "Usage: $0 <test_type> [kurtosis_repo_path] [e2e_repo_path]"
    echo ""
    echo "Arguments:"
    echo "  test_type           Type of test to run (required)"
    echo "                      Options: single-l2-network-op-succinct"
    echo "                               single-l2-network-op-succinct-aggoracle-committee"
    echo "                               single-l2-network-op-pessimistic"
    echo "                               multi-l2-networks-2-chains-op-pessimistic"
    echo "                               multi-l2-networks-3-chains-cdk-erigon-pessimistic"
    echo "  kurtosis_repo_path  Path to Kurtosis CDK repo (optional)"
    echo "                      - If not provided: Will prompt to clone temporarily"
    echo "                      - Use '-' to skip Kurtosis setup entirely"
    echo "  e2e_repo_path       Path to E2E repo (optional)"
    echo "                      - If not provided: Will prompt to clone temporarily"
    echo "                      - Use '-' to skip E2E tests entirely"
    echo ""
    echo "Examples:"
    echo "  $0 single-l2-network-op-succinct                                     # Prompt to clone both repos"
    echo "  $0 single-l2-network-op-succinct /path/to/kurtosis /path/to/e2e     # Use existing repos"
    echo "  $0 single-l2-network-op-succinct /path/to/kurtosis                  # Use Kurtosis, prompt for E2E"
    echo "  $0 single-l2-network-op-succinct - /path/to/e2e                     # Skip Kurtosis, use E2E repo"
    echo "  $0 single-l2-network-op-succinct /path/to/kurtosis -                # Use Kurtosis, skip E2E"
    exit 1
fi

TEST_TYPE=$1
KURTOSIS_REPO_PATH="${2:-}"
E2E_REPO_PATH="${3:-}"

PROJECT_ROOT="$PWD"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKFLOW_FILE="$PROJECT_ROOT/.github/workflows/test-e2e.yml"
KURTOSIS_CDK_REPO_URL="https://github.com/0xPolygon/kurtosis-cdk.git"
E2E_REPO_URL="https://github.com/agglayer/e2e.git"

log_info "Starting local E2E setup..."

# Extract KURTOSIS_CDK_COMMIT from the workflow file
get_expected_kurtosis_commit() {
    if [ ! -f "$WORKFLOW_FILE" ]; then
        log_error "Workflow file not found: $WORKFLOW_FILE"
        exit 1
    fi

    local commit
    commit=$(grep -E '^\s*KURTOSIS_CDK_COMMIT:\s*"[a-f0-9]+"' "$WORKFLOW_FILE" | sed -E 's/.*"([a-f0-9]+)".*/\1/')

    if [ -z "$commit" ]; then
        log_error "Could not extract KURTOSIS_CDK_COMMIT from workflow file"
        exit 1
    fi

    echo "$commit"
}

# Clone Kurtosis CDK repo to temporary directory
clone_kurtosis_repo() {
    local expected_commit="$1"

    TEMP_KURTOSIS_DIR=$(mktemp -d)
    log_info "Cloning Kurtosis CDK repo to temporary directory..."
    log_info "Temporary directory: $TEMP_KURTOSIS_DIR"

    if ! git clone --quiet "$KURTOSIS_CDK_REPO_URL" "$TEMP_KURTOSIS_DIR"; then
        log_error "Failed to clone Kurtosis CDK repository"
        exit 1
    fi

    log_info "Successfully cloned Kurtosis CDK repository"
    log_info "Checking out commit $expected_commit..."

    if ! git -C "$TEMP_KURTOSIS_DIR" checkout --quiet "$expected_commit"; then
        log_error "Failed to checkout commit $expected_commit"
        exit 1
    fi

    log_info "Successfully checked out expected commit"
    echo "$TEMP_KURTOSIS_DIR"
}

# Clone E2E repo to temporary directory
clone_e2e_repo() {
    TEMP_E2E_DIR=$(mktemp -d)
    log_info "Cloning E2E test repo to temporary directory..."
    log_info "Temporary directory: $TEMP_E2E_DIR"

    if ! git clone --quiet "$E2E_REPO_URL" "$TEMP_E2E_DIR"; then
        log_error "Failed to clone E2E test repository"
        exit 1
    fi

    log_info "Successfully cloned E2E test repository"
    echo "$TEMP_E2E_DIR"
}

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

# Handle Kurtosis repo path
if [ "$KURTOSIS_REPO_PATH" = "-" ]; then
    log_info "Skipping Kurtosis setup (kurtosis_repo_path is '-')"
elif [ -z "$KURTOSIS_REPO_PATH" ]; then
    # No path provided, ask user if they want to clone
    echo ""
    log_warn "No Kurtosis CDK repository path provided."
    read -p "Do you want to clone the Kurtosis CDK repo temporarily? [Y/n] " -n 1 -r
    echo ""

    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
        EXPECTED_COMMIT=$(get_expected_kurtosis_commit)
        KURTOSIS_REPO_PATH=$(clone_kurtosis_repo "$EXPECTED_COMMIT")
    else
        log_info "Skipping Kurtosis setup"
        KURTOSIS_REPO_PATH="-"
    fi
fi

# Run Kurtosis setup if path is valid and not '-'
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

    log_info "Using Kurtosis CDK repo at: $KURTOSIS_REPO_PATH"

    # Sync aggkit-config.toml if it exists locally
    LOCAL_CONFIG_DIR="$SCRIPT_DIR/config"
    LOCAL_CONFIG_FILE="$LOCAL_CONFIG_DIR/aggkit-config.toml"
    KURTOSIS_TEMPLATE_FILE="$KURTOSIS_REPO_PATH/templates/aggkit/aggkit-config.toml"

    if [ -f "$LOCAL_CONFIG_FILE" ]; then
        if [ -f "$KURTOSIS_TEMPLATE_FILE" ]; then
            log_info "Syncing local aggkit-config.toml to Kurtosis template..."
            cp "$LOCAL_CONFIG_FILE" "$KURTOSIS_TEMPLATE_FILE"
        else
            log_warn "Kurtosis template not found at: $KURTOSIS_TEMPLATE_FILE"
        fi
    fi

    # Verify kurtosis-cdk is at the expected commit (only for user-provided paths)
    if [ -z "$TEMP_KURTOSIS_DIR" ]; then
        # User provided an existing repo - verify commit
        EXPECTED_COMMIT=$(get_expected_kurtosis_commit)
        CURRENT_COMMIT=$(git -C "$KURTOSIS_REPO_PATH" rev-parse HEAD)

        if [ "$CURRENT_COMMIT" != "$EXPECTED_COMMIT" ]; then
            log_warn "Kurtosis repo is not at the expected commit!"
            log_warn "  Expected: $EXPECTED_COMMIT"
            log_warn "  Current:  $CURRENT_COMMIT"
            echo ""
            read -p "Do you want to checkout the expected commit? [y/N] " -n 1 -r
            echo ""

            if [[ $REPLY =~ ^[Yy]$ ]]; then
                log_info "Checking out commit $EXPECTED_COMMIT..."
                git -C "$KURTOSIS_REPO_PATH" fetch origin
                git -C "$KURTOSIS_REPO_PATH" checkout "$EXPECTED_COMMIT"
                log_info "Successfully checked out expected commit."
            else
                log_warn "Continuing with current commit. Tests may not match CI behavior."
            fi
        else
            log_info "Kurtosis repo is at expected commit: $EXPECTED_COMMIT"
        fi
    fi

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
fi

# Handle E2E repo path
if [ "$E2E_REPO_PATH" = "-" ]; then
    log_info "Skipping E2E tests (e2e_repo_path is '-')"
elif [ -z "$E2E_REPO_PATH" ]; then
    # No path provided, ask user if they want to clone
    echo ""
    log_warn "No E2E test repository path provided."
    read -p "Do you want to clone the E2E test repo temporarily? [Y/n] " -n 1 -r
    echo ""

    if [[ ! $REPLY =~ ^[Nn]$ ]]; then
        E2E_REPO_PATH=$(clone_e2e_repo)
    else
        log_info "Skipping E2E tests"
        E2E_REPO_PATH="-"
    fi
fi

# Run E2E tests if path is valid and not '-'
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
fi
