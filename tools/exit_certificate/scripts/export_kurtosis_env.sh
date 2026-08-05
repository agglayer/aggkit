#!/usr/bin/env bash
# Resolves connection info (RPC URLs, bridge address, agglayer URLs, ...) from a
# running Kurtosis enclave and prints `export VAR=value` lines on stdout so they
# can be loaded into the current shell. Logs go to stderr.
#
# Usage (load the variables into your shell):
#   source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
#   # or:
#   eval "$(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)"
#
# Afterwards bridge_l1_to_l2.sh (and other scripts) reuse L1_RPC_URL, L2_RPC_URL,
# BRIDGE_ADDR, ... directly instead of querying Kurtosis again.
#
# Requires: kurtosis.
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn()  { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

usage() {
    cat >&2 <<EOF
Usage: $0 [OPTIONS] [NETWORK_INDEX]

Resolves Kurtosis connection info and prints \`export VAR=value\` lines on stdout.
Load it into your shell with: source <($0 NETWORK_INDEX)

Arguments:
  NETWORK_INDEX   L2 network index to target (default: 1 → service suffix 001)

Options:
  -e, --enclave   ENCLAVE    Kurtosis enclave name (default: \$KURTOSIS_ENCLAVE or "aggkit")
  -h, --help                 Show this help

Environment variables (override defaults):
  KURTOSIS_ENCLAVE                  Enclave name
  KURTOSIS_ARTIFACT_AGGKIT_CONFIG   Aggkit config artifact name (default: aggkit-config)
  L1_SERVICE                        Kurtosis L1 execution service (default: el-1-geth-lighthouse)
  L2_SERVICE_PREFIX                 Kurtosis L2 execution service prefix (default: op-el-1-op-geth-op-node)
  AGGLAYER_SERVICE                  Kurtosis agglayer service name (default: agglayer)
  AGGKIT_BRIDGE_SERVICE_PREFIX      Aggkit bridge service prefix; service name is
                                     <prefix>-<suffix>-bridge (default: aggkit)

Exported variables:
  KURTOSIS_ENCLAVE   NETWORK_INDEX   L1_RPC_URL   L2_RPC_URL   BRIDGE_ADDR
  AGGLAYER_ADMIN_URL   AGGLAYER_GRPC_URL   BRIDGE_SERVICE_URL

Examples:
  source <($0)                # Network 1, enclave "aggkit"
  source <($0 2)              # Network 2 (service op-geth-002)
  eval "\$($0 --enclave op 1)"
EOF
    exit 1
}

# Defaults (can be overridden by env vars)
KURTOSIS_ENCLAVE="${KURTOSIS_ENCLAVE:-aggkit}"
KURTOSIS_ARTIFACT_AGGKIT_CONFIG="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG:-aggkit-config}"
L1_SERVICE="${L1_SERVICE:-el-1-geth-lighthouse}"
L2_SERVICE_PREFIX="${L2_SERVICE_PREFIX:-op-el-1-op-geth-op-node}"
AGGLAYER_SERVICE="${AGGLAYER_SERVICE:-agglayer}"
AGGKIT_BRIDGE_SERVICE_PREFIX="${AGGKIT_BRIDGE_SERVICE_PREFIX:-aggkit}"
NETWORK_INDEX=1

# Parse flags and positional args
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

NETWORK_SUFFIX=$(printf '%03d' "$NETWORK_INDEX")
L2_SERVICE="${L2_SERVICE_PREFIX}-${NETWORK_SUFFIX}"

# ---------------------------------------------------------------------------
# Dependency checks
# ---------------------------------------------------------------------------

command -v kurtosis &>/dev/null || { log_error "Missing required tool: kurtosis"; exit 1; }

# ---------------------------------------------------------------------------
# Kurtosis helpers
# ---------------------------------------------------------------------------

# kurtosis port print returns something like "http://127.0.0.1:PORT" — extract
# the port and rebuild as http://localhost:PORT.
port_to_localhost_url() {
    local raw_url="$1"
    local port
    port=$(echo "$raw_url" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

get_l1_rpc_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$L1_SERVICE" rpc 2>/dev/null); then
        log_error "Failed to get L1 RPC port from service '$L1_SERVICE' in enclave '$KURTOSIS_ENCLAVE'"
        log_error "Ensure the enclave is running: kurtosis enclave inspect $KURTOSIS_ENCLAVE"
        exit 1
    fi
    port_to_localhost_url "$raw"
}

get_l2_rpc_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$L2_SERVICE" rpc 2>/dev/null); then
        log_error "Failed to get L2 RPC port from service '$L2_SERVICE' in enclave '$KURTOSIS_ENCLAVE'"
        log_error "To use a different prefix, set L2_SERVICE_PREFIX"
        exit 1
    fi
    port_to_localhost_url "$raw"
}

get_agglayer_admin_url() {
    local raw
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$AGGLAYER_SERVICE" aglr-admin 2>/dev/null); then
        return 1
    fi
    port_to_localhost_url "$raw"
}

get_agglayer_grpc_url() {
    local raw port
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$AGGLAYER_SERVICE" aglr-grpc 2>/dev/null); then
        return 1
    fi
    # kurtosis returns grpc://host:PORT — rebuild as http://localhost:PORT (insecure gRPC)
    port=$(echo "$raw" | sed -E 's|^[a-zA-Z]+://||' | cut -f2 -d':')
    echo "http://localhost:${port}"
}

# Aggkit bridge REST endpoint of this L2 network (service <prefix>-<suffix>-bridge, port "rest").
get_bridge_service_url() {
    local raw
    local service="${AGGKIT_BRIDGE_SERVICE_PREFIX}-${NETWORK_SUFFIX}-bridge"
    if ! raw=$(kurtosis port print "$KURTOSIS_ENCLAVE" "$service" rest 2>/dev/null); then
        return 1
    fi
    port_to_localhost_url "$raw"
}

get_bridge_address() {
    local tmp_dir
    tmp_dir=$(mktemp -d)
    # shellcheck disable=SC2064
    trap "rm -rf '$tmp_dir'" RETURN

    local artifact_name="${KURTOSIS_ARTIFACT_AGGKIT_CONFIG}-${NETWORK_SUFFIX}"
    if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$artifact_name" "$tmp_dir" &>/dev/null; then
        log_warn "Artifact '$artifact_name' not found, trying '$KURTOSIS_ARTIFACT_AGGKIT_CONFIG'..."
        artifact_name="$KURTOSIS_ARTIFACT_AGGKIT_CONFIG"
        if ! kurtosis files download "$KURTOSIS_ENCLAVE" "$artifact_name" "$tmp_dir" &>/dev/null; then
            log_error "Could not download artifact '$artifact_name' from enclave '$KURTOSIS_ENCLAVE'"
            exit 1
        fi
    fi

    local config_file="$tmp_dir/config.toml"
    if [[ ! -f "$config_file" ]]; then
        log_error "config.toml not found in downloaded artifact '$artifact_name'"
        exit 1
    fi

    local addr
    addr=$(grep 'BridgeAddr' "$config_file" | head -1 | tr -d '[:space:]' | cut -f2 -d'=' | tr -d '"')
    if [[ -z "$addr" ]]; then
        log_error "BridgeAddr not found in $config_file"
        exit 1
    fi
    echo "$addr"
}

# ---------------------------------------------------------------------------
# Resolve
# ---------------------------------------------------------------------------

log_info "Enclave:        $KURTOSIS_ENCLAVE"
log_info "Network index:  $NETWORK_INDEX (suffix: $NETWORK_SUFFIX)"
log_info "L1 service:     $L1_SERVICE"
log_info "L2 service:     $L2_SERVICE"

log_info "Getting L1 RPC URL..."
L1_RPC_URL=$(get_l1_rpc_url)
log_info "L1 RPC URL: $L1_RPC_URL"

log_info "Getting L2 RPC URL..."
L2_RPC_URL=$(get_l2_rpc_url)
log_info "L2 RPC URL: $L2_RPC_URL"

log_info "Getting bridge address from aggkit config artifact..."
BRIDGE_ADDR=$(get_bridge_address)
log_info "Bridge address: $BRIDGE_ADDR"

log_info "Getting bridge service URL..."
BRIDGE_SERVICE_URL=""
if BRIDGE_SERVICE_URL=$(get_bridge_service_url); then
    log_info "Bridge service URL: $BRIDGE_SERVICE_URL"
else
    log_warn "Service '${AGGKIT_BRIDGE_SERVICE_PREFIX}-${NETWORK_SUFFIX}-bridge' (rest) not found — BRIDGE_SERVICE_URL will be empty"
fi

log_info "Getting agglayer URLs..."
AGGLAYER_ADMIN_URL=""
if AGGLAYER_ADMIN_URL=$(get_agglayer_admin_url); then
    log_info "Agglayer admin URL: $AGGLAYER_ADMIN_URL"
else
    log_warn "Service '$AGGLAYER_SERVICE' (aglr-admin) not found — AGGLAYER_ADMIN_URL will be empty"
fi
AGGLAYER_GRPC_URL=""
if AGGLAYER_GRPC_URL=$(get_agglayer_grpc_url); then
    log_info "Agglayer gRPC URL:  $AGGLAYER_GRPC_URL"
else
    log_warn "Service '$AGGLAYER_SERVICE' (aglr-grpc) not found — AGGLAYER_GRPC_URL will be empty"
fi

# ---------------------------------------------------------------------------
# Emit export statements on stdout
# ---------------------------------------------------------------------------

cat <<EOF
export KURTOSIS_ENCLAVE="$KURTOSIS_ENCLAVE"
export NETWORK_INDEX="$NETWORK_INDEX"
export L1_RPC_URL="$L1_RPC_URL"
export L2_RPC_URL="$L2_RPC_URL"
export BRIDGE_ADDR="$BRIDGE_ADDR"
export BRIDGE_SERVICE_URL="$BRIDGE_SERVICE_URL"
export AGGLAYER_ADMIN_URL="$AGGLAYER_ADMIN_URL"
export AGGLAYER_GRPC_URL="$AGGLAYER_GRPC_URL"
EOF

log_info "Run: source <($0 $NETWORK_INDEX)   to load these into your shell."
