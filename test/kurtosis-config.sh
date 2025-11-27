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

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOCAL_CONFIG_DIR="$SCRIPT_DIR/config"
LOCAL_CONFIG_FILE="$LOCAL_CONFIG_DIR/aggkit-config.toml"
KURTOSIS_TEMPLATE_REL_PATH="templates/aggkit/aggkit-config.toml"
WORKFLOW_FILE="$PROJECT_ROOT/.github/workflows/test-e2e.yml"

usage() {
    echo "Usage: $0 <kurtosis_repo_path>"
    echo ""
    echo "Syncs the aggkit-config.toml template between the local test/config folder"
    echo "and the Kurtosis CDK repository."
    echo ""
    echo "Arguments:"
    echo "  kurtosis_repo_path  Path to Kurtosis CDK repo"
    echo ""
    echo "Behavior:"
    echo "  - Verifies Kurtosis repo is at the expected commit from test-e2e.yml"
    echo "  - If local copy exists: Replaces the Kurtosis template with local copy"
    echo "  - If no local copy: Prompts to copy template from Kurtosis repo"
    echo ""
    echo "Examples:"
    echo "  $0 /path/to/kurtosis-cdk"
    exit 1
}

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

# Get current commit of Kurtosis repo
get_kurtosis_current_commit() {
    local repo_path="$1"
    git -C "$repo_path" rev-parse HEAD
}

# Check if Kurtosis repo is at expected commit
check_kurtosis_commit() {
    local repo_path="$1"
    local expected_commit="$2"

    local current_commit
    current_commit=$(get_kurtosis_current_commit "$repo_path")

    if [ "$current_commit" != "$expected_commit" ]; then
        log_warn "Kurtosis repo is not at the expected commit!"
        log_warn "  Expected: $expected_commit"
        log_warn "  Current:  $current_commit"
        echo ""
        read -p "Do you want to checkout the expected commit? [y/N] " -n 1 -r
        echo ""

        if [[ $REPLY =~ ^[Yy]$ ]]; then
            log_info "Checking out commit $expected_commit..."
            git -C "$repo_path" fetch origin
            git -C "$repo_path" checkout "$expected_commit"
            log_info "Successfully checked out expected commit."
        else
            log_warn "Continuing with current commit. Tests may not match CI behavior."
        fi
    else
        log_info "Kurtosis repo is at expected commit: $expected_commit"
    fi
}

if [ "$#" -ne 1 ]; then
    usage
fi

KURTOSIS_REPO_PATH="$1"
KURTOSIS_TEMPLATE_FILE="$KURTOSIS_REPO_PATH/$KURTOSIS_TEMPLATE_REL_PATH"

# Validate Kurtosis repo path
if [ ! -d "$KURTOSIS_REPO_PATH" ]; then
    log_error "The provided Kurtosis repo path does not exist: $KURTOSIS_REPO_PATH"
    exit 1
fi

# Verify it's a git repository
if [ ! -d "$KURTOSIS_REPO_PATH/.git" ]; then
    log_error "The provided path is not a git repository: $KURTOSIS_REPO_PATH"
    exit 1
fi

# Get expected commit and verify Kurtosis repo
EXPECTED_COMMIT=$(get_expected_kurtosis_commit)
log_info "Expected Kurtosis CDK commit from workflow: $EXPECTED_COMMIT"
check_kurtosis_commit "$KURTOSIS_REPO_PATH" "$EXPECTED_COMMIT"

# Check if Kurtosis template exists
if [ ! -f "$KURTOSIS_TEMPLATE_FILE" ]; then
    log_error "Kurtosis template not found at: $KURTOSIS_TEMPLATE_FILE"
    exit 1
fi

# Check if local config directory exists
if [ ! -d "$LOCAL_CONFIG_DIR" ]; then
    log_error "Local config directory does not exist: $LOCAL_CONFIG_DIR"
    exit 1
fi

# Main logic
if [ -f "$LOCAL_CONFIG_FILE" ]; then
    # Local copy exists - replace Kurtosis template with local copy
    log_info "Local config found at: $LOCAL_CONFIG_FILE"
    log_info "Replacing Kurtosis template with local copy..."

    cp "$LOCAL_CONFIG_FILE" "$KURTOSIS_TEMPLATE_FILE"

    log_info "Successfully replaced: $KURTOSIS_TEMPLATE_FILE"
else
    # No local copy - ask user if they want to copy from Kurtosis
    log_warn "No local config found at: $LOCAL_CONFIG_FILE"
    log_info "Kurtosis template found at: $KURTOSIS_TEMPLATE_FILE"

    echo ""
    read -p "Do you want to copy the template from Kurtosis repo to local config? [y/N] " -n 1 -r
    echo ""

    if [[ $REPLY =~ ^[Yy]$ ]]; then
        cp "$KURTOSIS_TEMPLATE_FILE" "$LOCAL_CONFIG_FILE"
        log_info "Successfully copied template to: $LOCAL_CONFIG_FILE"
        log_info "You can now edit the local copy and run this script again to sync changes back."
    else
        log_info "Skipping copy. No changes made."
    fi
fi
