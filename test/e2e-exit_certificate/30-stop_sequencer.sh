#!/usr/bin/env bash
# Stops the whole L2 OP stack (execution client, consensus/op-node, batcher and
# proposer) of a running Kurtosis enclave to simulate that the L2 network has
# been deprecated and halted. The exit-certificate claim flow must keep working
# against L1 even though the L2 is no longer producing blocks nor reachable.
#
# Services are discovered dynamically from the enclave: every service whose name
# matches the OP stack pattern for the target network is stopped. This is robust
# against naming variations between Kurtosis package versions.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

usage() {
    cat >&2 <<EOF
Usage: $0 [OPTIONS] [NETWORK_INDEX]

Stops the whole L2 OP stack (op-el, op-cl/op-node, op-batcher, op-proposer) of a
running Kurtosis enclave to simulate a deprecated/halted L2 network.

Arguments:
  NETWORK_INDEX   L2 network index to target (default: 1 -> service suffix 001)

Options:
  -e, --enclave   ENCLAVE    Kurtosis enclave name (default: \$KURTOSIS_ENCLAVE or "aggkit")
  -h, --help                 Show this help

Environment variables (override defaults):
  KURTOSIS_ENCLAVE      Enclave name (default: aggkit)
  L2_SERVICE_PATTERN    Regex matching the L2 OP stack service names to stop
                        (default: ^op-.*-NNN\$, where NNN is the network suffix)

Examples:
  $0                          # Network 1, enclave "aggkit"
  $0 2                        # Network 2 (services *-002)
  $0 --enclave op 1
EOF
    exit 1
}

KURTOSIS_ENCLAVE="${KURTOSIS_ENCLAVE:-aggkit}"
NETWORK_INDEX=1

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -e|--enclave)
            [[ $# -lt 2 ]] && { log_error "--enclave requires a value"; usage; }
            KURTOSIS_ENCLAVE="$2"; shift 2 ;;
        [0-9]*)
            NETWORK_INDEX="$1"; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

command -v kurtosis &>/dev/null || { log_error "Missing required tool: kurtosis"; exit 1; }

NETWORK_SUFFIX=$(printf '%03d' "$NETWORK_INDEX")
# By default match every OP stack service for this network: op-el-*, op-cl-*,
# op-batcher-*, op-proposer-*, ... all sharing the -NNN suffix.
L2_SERVICE_PATTERN="${L2_SERVICE_PATTERN:-^op-.*-${NETWORK_SUFFIX}\$}"

log_info "Enclave:         $KURTOSIS_ENCLAVE"
log_info "Network suffix:  $NETWORK_SUFFIX"
log_info "Service pattern: $L2_SERVICE_PATTERN"

if ! kurtosis enclave inspect "$KURTOSIS_ENCLAVE" >/dev/null 2>&1; then
    log_error "Enclave '$KURTOSIS_ENCLAVE' does not exist or is not running."
    log_error "Check with: kurtosis enclave ls"
    exit 1
fi

# Discover the L2 OP stack services from the "User Services" table of
# kurtosis enclave inspect. Service rows start with a 12-hex UUID in column 1
# and the service name in column 2 (multi-port continuation lines have no UUID,
# so they are skipped). Restrict to the User Services section to avoid matching
# the Files Artifacts table, then filter by pattern.
mapfile -t SERVICES < <(
    kurtosis enclave inspect "$KURTOSIS_ENCLAVE" 2>/dev/null \
        | sed -n '/User Services/,$p' \
        | awk 'NF && $1 ~ /^[0-9a-f]{12}$/ {print $2}' \
        | grep -E "$L2_SERVICE_PATTERN" || true
)

if [[ ${#SERVICES[@]} -eq 0 ]]; then
    log_warn "No services matching '$L2_SERVICE_PATTERN' found in enclave '$KURTOSIS_ENCLAVE'."
    log_warn "Nothing to stop. Inspect with: kurtosis enclave inspect $KURTOSIS_ENCLAVE"
    exit 0
fi

log_info "🛑 Stopping ${#SERVICES[@]} L2 service(s) to simulate a deprecated/halted network:"
for svc in "${SERVICES[@]}"; do
    log_info "  - $svc"
done

failed=0
for svc in "${SERVICES[@]}"; do
    if kurtosis service stop "$KURTOSIS_ENCLAVE" "$svc"; then
        log_info "Stopped '$svc'."
    else
        log_error "Failed to stop service '$svc'."
        failed=1
    fi
done

if [[ $failed -ne 0 ]]; then
    log_error "One or more L2 services could not be stopped."
    exit 1
fi

log_info "✅ L2 network stopped: all matching OP stack services have been halted."
