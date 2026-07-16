#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

# Pinned Kurtosis CDK commit for the exit-certificate environment.
KURTOSIS_CDK_COMMIT="507e4e8e6581ab5ecbb44444f9b7f1d05e3dcd1c"
KURTOSIS_CDK_REPO_URL="https://github.com/0xPolygon/kurtosis-cdk.git"
ENCLAVE_NAME="aggkit"
ARGS_FILE="$SCRIPT_DIR/single_op_pessimistic_args.json"

# Variable for the temporary clone
TEMP_KURTOSIS_DIR=""

trap 'log_error "Script failed at line $LINENO"' ERR

usage() {
    cat >&2 <<EOF
Usage: $0 <command>

Manage the exit-certificate Kurtosis environment.

Commands:
  start   Clone Kurtosis CDK (pinned commit) if needed and run the
          '$ENCLAVE_NAME' enclave with $(basename "$ARGS_FILE").
          Does nothing if the enclave already exists.
  stop    Remove the '$ENCLAVE_NAME' enclave if it exists.
  restart Remove the '$ENCLAVE_NAME' enclave (if any) and start it again.
  status  Show whether the '$ENCLAVE_NAME' enclave exists and, if so,
          print 'kurtosis enclave inspect $ENCLAVE_NAME'.

Examples:
  $0 start
  $0 stop
  $0 restart
  $0 status
EOF
    exit 1
}

name_temp_kurtosis_dir() {
    local _commit="$1"
    echo "${TMPDIR:-/tmp}/kurtosis_${_commit}"
}

# Return 0 if the Kurtosis enclave exists, 1 otherwise
enclave_exists() {
    kurtosis enclave inspect "$ENCLAVE_NAME" >/dev/null 2>&1
}

# Clone Kurtosis CDK repo to temporary directory at the pinned commit
clone_kurtosis_repo() {
    local expected_commit="$1"
    local dir
    dir=$(name_temp_kurtosis_dir "$expected_commit")

    if [ -d "$dir" ]; then
        log_info "Temporary directory for Kurtosis already exists: $dir"
        log_info "Reusing existing directory..."
        echo "$dir"
        return
    fi
    TEMP_KURTOSIS_DIR="$dir"
    mkdir -p "$TEMP_KURTOSIS_DIR"
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

start_environment() {
    log_info "Starting exit-certificate Kurtosis environment..."

    if enclave_exists; then
        log_warn "Enclave '$ENCLAVE_NAME' already exists. Nothing to do."
        log_warn "Run '$0 stop' first if you want to recreate it."
        return
    fi

    if [ ! -f "$ARGS_FILE" ]; then
        log_error "Args file not found: $ARGS_FILE"
        exit 1
    fi

    local kurtosis_repo_path
    local kurtosis_repo_status
    local default_dir
    default_dir=$(name_temp_kurtosis_dir "$KURTOSIS_CDK_COMMIT")
    if [ -d "$default_dir" ]; then
        log_info "Found existing Kurtosis CDK repo at default location: $default_dir"
        kurtosis_repo_path="$default_dir"
        kurtosis_repo_status="reused (already present, not cloned)"
    else
        kurtosis_repo_path=$(clone_kurtosis_repo "$KURTOSIS_CDK_COMMIT")
        if [ -n "$TEMP_KURTOSIS_DIR" ]; then
            kurtosis_repo_status="cloned (commit $KURTOSIS_CDK_COMMIT)"
        else
            kurtosis_repo_status="reused (already present, not cloned)"
        fi
    fi

    log_info "Using Kurtosis CDK repo at: $kurtosis_repo_path"

    # Sync local aggkit-config.toml into the Kurtosis template if it exists locally.
    local local_config_file="$SCRIPT_DIR/config/aggkit-config.toml"
    local kurtosis_template_file="$kurtosis_repo_path/static_files/chain/shared/aggkit/config.toml"
    local config_overwritten="no"
    local config_source="-"

    if [ -f "$local_config_file" ]; then
        if [ -f "$kurtosis_template_file" ]; then
            log_warn "OVERWRITING Kurtosis aggkit-config.toml template with local config:"
            log_warn "  source (local):       $local_config_file"
            log_warn "  destination (kurtosis): $kurtosis_template_file"
            cp "$local_config_file" "$kurtosis_template_file"
            log_info "Kurtosis aggkit-config.toml template overwritten successfully."
            config_overwritten="yes"
            config_source="$local_config_file"
        else
            log_warn "Kurtosis template not found at: $kurtosis_template_file"
            log_warn "Skipping aggkit-config.toml sync."
        fi
    else
        log_info "No local aggkit-config.toml found at: $local_config_file"
        log_info "Using the Kurtosis CDK default aggkit-config.toml template."
    fi

    pushd "$kurtosis_repo_path" >/dev/null
    log_info "Starting Kurtosis enclave '$ENCLAVE_NAME' with args file: $ARGS_FILE"
    kurtosis run --enclave "$ENCLAVE_NAME" --args-file "$ARGS_FILE" . 
    log_info "$ENCLAVE_NAME enclave started successfully."
    popd >/dev/null

    # Summary of what was set up
    log_info "================ Environment summary ================"
    log_info "Kurtosis CDK repo path : $kurtosis_repo_path"
    log_info "Kurtosis CDK repo      : $kurtosis_repo_status"
    if [ "$config_overwritten" = "yes" ]; then
        log_info "aggkit config          : OVERWRITTEN"
        log_info "  with local file      : $config_source"
        log_info "  into kurtosis file   : $kurtosis_template_file"
    else
        log_info "aggkit config          : NOT overwritten (using Kurtosis default)"
    fi
    log_info "===================================================="
}

stop_environment() {
    log_info "Stopping exit-certificate Kurtosis environment..."

    log_info "Removing all enclaves"
    kurtosis clean --all
    log_info "All enclaves removed successfully."
}

restart_environment() {
    log_info "Restarting exit-certificate Kurtosis environment..."
    stop_environment
    start_environment
}

status_environment() {
    if enclave_exists; then
        log_info "Enclave '$ENCLAVE_NAME' exists."
        kurtosis enclave inspect "$ENCLAVE_NAME"
    else
        log_warn "Enclave '$ENCLAVE_NAME' does not exist."
    fi
}

if [ "$#" -ne 1 ]; then
    usage
fi

case "$1" in
    start)
        start_environment
        ;;
    stop)
        stop_environment
        ;;
    restart)
        restart_environment
        ;;
    status)
        status_environment
        ;;
    *)
        usage
        ;;
esac
