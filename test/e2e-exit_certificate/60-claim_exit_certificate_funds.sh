#!/usr/bin/env bash
# Claims the exit-certificate funds on L1 after the L2 network has been halted.
#
# Two steps:
#   1. Start the exit_certificate_claimer HTTP service, deriving its config from
#      the exit_certificate config produced earlier
#      (tmp/exit_certificate-kurtosis.json). The service only binds its port once
#      its L1 Info Tree is synced up to the certificate's settlement GER, so we
#      poll /claimer/v1/health until it is ready.
#   2. Run tools/exit_certificate_claimer/scripts/claim-all.sh against it, which
#      claims every pending bridge exit for all tracked addresses (the EOAs plus
#      the config's exitAddress).
#
# The claimer runs in one of two modes (CLAIMER_MODE):
#   - binary (default): build the exit_certificate_claimer binary and run it in
#     the background on the host.
#   - docker: run it through tools/exit_certificate_claimer/docker/docker-compose.yml.
#     A copy of the exit_certificate config is created with 127.0.0.1/localhost
#     rewritten to host.docker.internal so the container reaches the
#     Kurtosis-published L1 RPC on the host; relative paths (outputDir) keep
#     resolving because the copy lives next to the original, mounted at /data.
#
# The service (background process or compose stack) is always stopped on exit.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CLAIMER_SCRIPT_DIR="${AGGKIT_DIR}/tools/exit_certificate_claimer/scripts"
CLAIMER_BIN="${AGGKIT_DIR}/target/exit_certificate_claimer"
EXIT_CERT_CONFIG="${EXIT_CERT_CONFIG:-${AGGKIT_DIR}/tmp/exit_certificate-kurtosis.json}"

# How to run the claimer: "binary" (host background process) or "docker" (compose).
CLAIMER_MODE="${CLAIMER_MODE:-binary}"
CLAIMER_COMPOSE_DIR="${AGGKIT_DIR}/tools/exit_certificate_claimer/docker"
CLAIMER_COMPOSE_PROJECT="${CLAIMER_COMPOSE_PROJECT:-exit-certificate-claimer-e2e}"
# Image tag the compose builds/runs in docker mode. Set CLAIMER_DOCKER_BUILD=0 to
# reuse an existing image instead of rebuilding it.
AGGKIT_IMAGE="${AGGKIT_IMAGE:-aggkit:e2e-exit-certificate-claimer}"
CLAIMER_DOCKER_BUILD="${CLAIMER_DOCKER_BUILD:-1}"

# Bind host/port for the claimer. Defaults match claim-all.sh's CLAIMER_URL.
CLAIMER_HOST="${CLAIMER_HOST:-127.0.0.1}"
CLAIMER_PORT="${CLAIMER_PORT:-7080}"
CLAIMER_URL="http://${CLAIMER_HOST}:${CLAIMER_PORT}"
# How long to wait for the claimer to finish its initial L1 sync and bind.
CLAIMER_READY_TIMEOUT="${CLAIMER_READY_TIMEOUT:-300}"

CLAIMER_PID=""
CLAIMER_LOG="$(mktemp -t exit_certificate_claimer.XXXXXX.log)"
DOCKER_CONFIG_COPY=""

# Runs docker compose against the claimer compose file with the e2e environment.
compose() {
    EXIT_TOOL_DIR="$(dirname "$EXIT_CERT_CONFIG")" \
    EXIT_CERT_CONFIG="$(basename "$DOCKER_CONFIG_COPY")" \
    CLAIMER_ADDRESS="0.0.0.0" \
    CLAIMER_PORT="$CLAIMER_PORT" \
    AGGKIT_IMAGE="$AGGKIT_IMAGE" \
        docker compose --project-directory "$CLAIMER_COMPOSE_DIR" \
        --project-name "$CLAIMER_COMPOSE_PROJECT" "$@"
}

claimer_alive() {
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        [[ -n "$(compose ps --status running --quiet 2>/dev/null)" ]]
    else
        kill -0 "$CLAIMER_PID" 2>/dev/null
    fi
}

claimer_logs() {
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        compose logs --no-color 2>/dev/null >&2 || true
    else
        cat "$CLAIMER_LOG" >&2
    fi
}

cleanup() {
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        if [[ -n "$DOCKER_CONFIG_COPY" ]]; then
            log_info "🛑 Stopping exit_certificate_claimer compose stack"
            compose down --remove-orphans >/dev/null 2>&1 || true
            rm -f "$DOCKER_CONFIG_COPY"
        fi
    elif [[ -n "$CLAIMER_PID" ]] && kill -0 "$CLAIMER_PID" 2>/dev/null; then
        log_info "🛑 Stopping exit_certificate_claimer service (pid $CLAIMER_PID)"
        kill "$CLAIMER_PID" 2>/dev/null || true
        wait "$CLAIMER_PID" 2>/dev/null || true
    fi
    rm -f "$CLAIMER_LOG"
}
trap cleanup EXIT

if [[ "$CLAIMER_MODE" != "binary" && "$CLAIMER_MODE" != "docker" ]]; then
    log_error "Invalid CLAIMER_MODE '$CLAIMER_MODE' (expected 'binary' or 'docker')"
    exit 1
fi

if [[ ! -f "$EXIT_CERT_CONFIG" ]]; then
    log_error "exit_certificate config not found: $EXIT_CERT_CONFIG"
    log_error "Run the exit tool first (40-generate_exit_certificate.sh) to generate it."
    exit 1
fi

if [[ "$CLAIMER_MODE" == "docker" ]]; then
    # The container reaches host-published services through host.docker.internal
    # (wired to host-gateway by the compose file), not 127.0.0.1. Rewrite the RPC
    # URLs in a sibling copy of the config so relative paths still resolve at /data.
    DOCKER_CONFIG_COPY="${EXIT_CERT_CONFIG%.json}.docker.json"
    sed -e 's#//127\.0\.0\.1:#//host.docker.internal:#g' \
        -e 's#//localhost:#//host.docker.internal:#g' \
        "$EXIT_CERT_CONFIG" >"$DOCKER_CONFIG_COPY"

    log_info "🚀 Start exit_certificate_claimer via docker compose on ${CLAIMER_URL}"
    log_info "   compose:               $CLAIMER_COMPOSE_DIR/docker-compose.yml"
    log_info "   config (derived from): $DOCKER_CONFIG_COPY"
    log_info "   image:                 $AGGKIT_IMAGE"
    if [[ "$CLAIMER_DOCKER_BUILD" == "1" ]]; then
        run_quiet compose up --detach --build
    else
        run_quiet compose up --detach
    fi
else
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
fi

log_info "⏳ Wait for the claimer to sync L1 and become ready (timeout ${CLAIMER_READY_TIMEOUT}s)"
health_url="${CLAIMER_URL}/claimer/v1/health"
ready=0
for (( i=0; i<CLAIMER_READY_TIMEOUT; i++ )); do
    if ! claimer_alive; then
        log_error "Claimer exited before becoming ready. Logs:"
        claimer_logs
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
    claimer_logs
    exit 1
fi
log_info "✅ Claimer is ready: $health_url"

log_info "💰 Claim all pending bridge exits"
CLAIMER_URL="$CLAIMER_URL" ASSUME_YES=1 \
    "${CLAIMER_SCRIPT_DIR}/claim-all.sh" "$EXIT_CERT_CONFIG"

log_info "✅ Done, exit-certificate funds claimed."
