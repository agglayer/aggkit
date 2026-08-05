#!/usr/bin/env bash
# Shows the status and height of the latest agglayer certificate for an L2 network,
# using the same agglayer gRPC client as the exit_certificate tool (so it works even
# when the agglayer node's gRPC reflection is incomplete). With --wait it polls until
# the latest certificate settles.
#
# Connection info is taken from the environment — this script never talks to Kurtosis
# or docker-compose. Populate the variables first with:
#   source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
#   source <(tools/exit_certificate/scripts/export_e2e_env.sh 1)
# Requires: go (the helper is run via `go run`).
#
# Required environment variables:
#   AGGLAYER_GRPC_URL   agglayer gRPC endpoint (e.g. http://localhost:PORT)
# Optional:
#   NETWORK_INDEX       default L2 network id (overridden by -n/--network)
set -euo pipefail

GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

usage() {
    cat >&2 <<EOF
Usage: $0 [OPTIONS]

Shows the status and height of the latest agglayer certificate for an L2 network.
Uses the agglayer gRPC client (GetLatestCertificateHeader + GetNetworkInfo).

Options:
  -n, --network   NETWORK_ID   L2 network id (default: \$NETWORK_INDEX or 1)
  -w, --wait                   Poll until the latest certificate is Settled
  -i, --interval  DURATION     Poll interval with --wait (default: 5s)
  -t, --timeout   DURATION     Max time to wait with --wait, 0 = no limit (default: 10m)
  -e, --expected-ler HASH      With --wait: poll until the settled local exit root equals HASH
                               (0x-prefixed 32-byte hash); avoids the pre-submission settlement race
      --tls                    Use TLS for the gRPC connection
  -h, --help                   Show this help

DURATION accepts Go duration syntax (e.g. 5s, 1m, 10m, 1h).

Required environment variables:
  AGGLAYER_GRPC_URL            agglayer gRPC endpoint (e.g. http://localhost:PORT)
  Tip: source <(tools/exit_certificate/scripts/export_kurtosis_env.sh NETWORK_ID)
  Tip: source <(tools/exit_certificate/scripts/export_e2e_env.sh NETWORK_ID)

Examples:
  source <(tools/exit_certificate/scripts/export_kurtosis_env.sh 1)
  source <(tools/exit_certificate/scripts/export_e2e_env.sh 1)
  $0                       # Show latest certificate status + height for network 1
  $0 -n 2                  # Network 2
  $0 --wait                # Wait until the latest certificate settles
  $0 --wait --timeout 0    # Wait forever
EOF
    exit 1
}

# ---------------------------------------------------------------------------
# Defaults / args
# ---------------------------------------------------------------------------

NETWORK_ID="${NETWORK_INDEX:-1}"
WAIT_FOR_SETTLED=false
POLL_INTERVAL="5s"
POLL_TIMEOUT="10m"
USE_TLS=false
EXPECTED_LER=""

while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help) usage ;;
        -n|--network)
            [[ $# -lt 2 ]] && { log_error "--network requires a value"; usage; }
            NETWORK_ID="$2"; shift 2 ;;
        -w|--wait)
            WAIT_FOR_SETTLED=true; shift ;;
        -i|--interval)
            [[ $# -lt 2 ]] && { log_error "--interval requires a value"; usage; }
            POLL_INTERVAL="$2"; shift 2 ;;
        -t|--timeout)
            [[ $# -lt 2 ]] && { log_error "--timeout requires a value"; usage; }
            POLL_TIMEOUT="$2"; shift 2 ;;
        -e|--expected-ler)
            [[ $# -lt 2 ]] && { log_error "--expected-ler requires a value"; usage; }
            EXPECTED_LER="$2"; shift 2 ;;
        --tls)
            USE_TLS=true; shift ;;
        *)
            log_error "Unknown argument: $1"; usage ;;
    esac
done

# ---------------------------------------------------------------------------
# Checks
# ---------------------------------------------------------------------------

if ! command -v go &>/dev/null; then
    log_error "Missing required tool: go"
    exit 1
fi

if [[ -z "${AGGLAYER_GRPC_URL:-}" ]]; then
    log_error "Missing required environment variable: AGGLAYER_GRPC_URL"
    log_error "Populate it with: source <(tools/exit_certificate/scripts/export_kurtosis_env.sh $NETWORK_ID)"
    log_error "            or: source <(tools/exit_certificate/scripts/export_e2e_env.sh $NETWORK_ID)"
    exit 1
fi

# ---------------------------------------------------------------------------
# Run the Go helper
# ---------------------------------------------------------------------------

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

ARGS=(-grpc "$AGGLAYER_GRPC_URL" -network "$NETWORK_ID")
[[ "$USE_TLS" == "true" ]] && ARGS+=(-tls)
if [[ "$WAIT_FOR_SETTLED" == "true" ]]; then
    ARGS+=(-wait -interval "$POLL_INTERVAL" -timeout "$POLL_TIMEOUT")
    [[ -n "$EXPECTED_LER" ]] && ARGS+=(-expected-ler "$EXPECTED_LER")
fi

cd "$REPO_ROOT"
exec go run ./tools/exit_certificate/scripts/agglayer_status "${ARGS[@]}"
