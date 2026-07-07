#!/usr/bin/env bash
# Claims the exit-certificate funds on L1 after the L2 network has been halted.
#
# Two steps:
#   1. Build and start the exit_certificate_claimer HTTP service, deriving its
#      config from the exit_certificate config produced earlier
#      (tmp/exit_certificate-kurtosis.json). The service only binds its port once
#      its L1 Info Tree is synced up to the certificate's settlement GER, so we
#      poll /claimer/v1/health until it is ready.
#   2. Run tools/exit_certificate_claimer/scripts/claim-all.sh against it, which
#      claims every pending bridge exit for all tracked addresses (the EOAs plus
#      the config's exitAddress).
#
# The background service is always stopped on exit.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CLAIMER_SCRIPT_DIR="${AGGKIT_DIR}/tools/exit_certificate_claimer/scripts"
CLAIMER_BIN="${AGGKIT_DIR}/target/exit_certificate_claimer"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"

# Bind host/port for the claimer. Defaults match claim-all.sh's CLAIMER_URL.
CLAIMER_HOST="${CLAIMER_HOST:-127.0.0.1}"
CLAIMER_PORT="${CLAIMER_PORT:-7080}"
CLAIMER_URL="http://${CLAIMER_HOST}:${CLAIMER_PORT}"
# How long to wait for the claimer to finish its initial L1 sync and bind.
CLAIMER_READY_TIMEOUT="${CLAIMER_READY_TIMEOUT:-300}"

CLAIMER_PID=""
CLAIMER_LOG="$(mktemp -t exit_certificate_claimer.XXXXXX.log)"

cleanup() {
    if [[ -n "$CLAIMER_PID" ]] && kill -0 "$CLAIMER_PID" 2>/dev/null; then
        log_info "🛑 Stopping exit_certificate_claimer service (pid $CLAIMER_PID)"
        kill "$CLAIMER_PID" 2>/dev/null || true
        wait "$CLAIMER_PID" 2>/dev/null || true
    fi
    rm -f "$CLAIMER_LOG"
}
trap cleanup EXIT

if [[ ! -f "$EXIT_CERT_CONFIG" ]]; then
    log_error "exit_certificate config not found: $EXIT_CERT_CONFIG"
    log_error "Run the exit tool first (40-generate_exit_certificate.sh) to generate it."
    exit 1
fi

log_info "🛠️ Build exit_certificate_claimer"
pushd "${AGGKIT_DIR}" >/dev/null
run_quiet make build-exit_certificate_claimer
popd >/dev/null

log_info "🚀 Start exit_certificate_claimer service on ${CLAIMER_URL}"
log_info "   config (derived from): $EXIT_CERT_CONFIG"
log_info "   logs:                  $CLAIMER_LOG"
"$CLAIMER_BIN" \
    --exit-certificate-config "$EXIT_CERT_CONFIG" \
    --address "$CLAIMER_HOST" \
    --port "$CLAIMER_PORT" \
    >"$CLAIMER_LOG" 2>&1 &
CLAIMER_PID=$!

log_info "⏳ Wait for the claimer to sync L1 and become ready (timeout ${CLAIMER_READY_TIMEOUT}s)"
health_url="${CLAIMER_URL}/claimer/v1/health"
ready=0
for (( i=0; i<CLAIMER_READY_TIMEOUT; i++ )); do
    if ! kill -0 "$CLAIMER_PID" 2>/dev/null; then
        log_error "Claimer process exited before becoming ready. Logs:"
        cat "$CLAIMER_LOG" >&2
        exit 1
    fi
    if curl -sf "$health_url" >/dev/null 2>&1; then
        ready=1
        break
    fi
    sleep 1
done

if [[ "$ready" -ne 1 ]]; then
    log_error "Claimer did not become ready within ${CLAIMER_READY_TIMEOUT}s. Logs:"
    cat "$CLAIMER_LOG" >&2
    exit 1
fi
log_info "✅ Claimer is ready: $health_url"

log_info "💰 Claim all pending bridge exits"
CLAIMER_URL="$CLAIMER_URL" ASSUME_YES=1 \
    "${CLAIMER_SCRIPT_DIR}/claim-all.sh" "$EXIT_CERT_CONFIG"

log_info "✅ Done, exit-certificate funds claimed."
