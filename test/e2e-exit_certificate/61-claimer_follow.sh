#!/usr/bin/env bash
# Runs the exit_certificate_claimer in FOLLOW MODE on the run archive (the GCS emulator of 51-archive_run.sh):
# it starts serving the run of one certificate height and must move by itself to the run of each next
# height once it is archived, without a restart.
#
#   61-claimer_follow.sh start <exit_certificate config of the run to start from>
#       Derives the claimer config from that config, but serves the run from the archive:
#       --run-source <ARCHIVE_URI_BASE> --run-source-height <height of that run> --follow-interval-seconds N,
#       with the message route of the Circle USDC scenario (--message-routes). Waits until it is ready.
#   61-claimer_follow.sh wait-height <height>
#       Waits until /health reports the run of <height>, and checks it is still the same process.
#   61-claimer_follow.sh stop
#
# CLAIMER_MODE: "binary" (default, host process) or "docker" (the claimer's docker-compose.yml plus
# claimer-follow.compose.yml, which passes the follow-mode settings and puts the container on the host
# network so it reaches the emulator on 127.0.0.1).
#
# Requires ARCHIVE_URI_BASE (start) and the emulator of 51-archive_run.sh.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
TMP_DIR="${AGGKIT_DIR}/tmp"
CLAIMER_BIN="${AGGKIT_DIR}/target/exit_certificate_claimer"
CIRCLE_STATE_FILE="${CIRCLE_STATE_FILE:-${TMP_DIR}/exit_certificate-e2e-circle-usdc.env}"

CLAIMER_MODE="${CLAIMER_MODE:-binary}"
CLAIMER_COMPOSE_DIR="${AGGKIT_DIR}/tools/exit_certificate_claimer/docker"
CLAIMER_COMPOSE_PROJECT="${CLAIMER_COMPOSE_PROJECT:-exit-certificate-claimer-e2e-follow}"
AGGKIT_IMAGE="${AGGKIT_IMAGE:-aggkit:e2e-exit-certificate-claimer}"
CLAIMER_DOCKER_BUILD="${CLAIMER_DOCKER_BUILD:-1}"
CLAIMER_HOST="${CLAIMER_HOST:-127.0.0.1}"
CLAIMER_PORT="${CLAIMER_PORT:-7080}"
CLAIMER_URL="http://${CLAIMER_HOST}:${CLAIMER_PORT}"
CLAIMER_READY_TIMEOUT="${CLAIMER_READY_TIMEOUT:-300}"
CLAIMER_FOLLOW_INTERVAL="${CLAIMER_FOLLOW_INTERVAL:-5}"
CLAIMER_FOLLOW_TIMEOUT="${CLAIMER_FOLLOW_TIMEOUT:-300}"
export STORAGE_EMULATOR_HOST="${STORAGE_EMULATOR_HOST:-127.0.0.1:${ARCHIVE_EMULATOR_PORT:-14443}}"

# Files of the follow-mode claimer, all in tmp/ (mounted at /data in docker mode).
CLAIMER_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-claimer.json"
CLAIMER_DOCKER_CONFIG="${TMP_DIR}/exit_certificate-kurtosis-claimer.docker.json"
CLAIMER_ROUTES="${TMP_DIR}/exit_certificate-claimer-message-routes.json"
CLAIMER_LOG="${TMP_DIR}/exit_certificate-claimer-follow.log"
CLAIMER_ID_FILE="${TMP_DIR}/exit_certificate-claimer-follow.id"

if [[ "$CLAIMER_MODE" != "binary" && "$CLAIMER_MODE" != "docker" ]]; then
    log_error "Invalid CLAIMER_MODE '$CLAIMER_MODE' (expected 'binary' or 'docker')"
    exit 1
fi

compose() {
    EXIT_TOOL_DIR="$TMP_DIR" \
    EXIT_CERT_CONFIG="$(basename "$CLAIMER_DOCKER_CONFIG")" \
    CLAIMER_ADDRESS="0.0.0.0" \
    CLAIMER_PORT="$CLAIMER_PORT" \
    AGGKIT_IMAGE="$AGGKIT_IMAGE" \
    CLAIMER_USER="$(id -u):$(id -g)" \
    CLAIMER_RUN_SOURCE="${ARCHIVE_URI_BASE:-}" \
    CLAIMER_RUN_SOURCE_HEIGHT="${START_HEIGHT:-0}" \
    CLAIMER_FOLLOW_INTERVAL_SECONDS="$CLAIMER_FOLLOW_INTERVAL" \
    CLAIMER_MESSAGE_ROUTES="/data/$(basename "$CLAIMER_ROUTES")" \
    STORAGE_EMULATOR_HOST="$STORAGE_EMULATOR_HOST" \
        docker compose --project-directory "$CLAIMER_COMPOSE_DIR" \
        -f "${CLAIMER_COMPOSE_DIR}/docker-compose.yml" -f "${SCRIPT_DIR}/claimer-follow.compose.yml" \
        --project-name "$CLAIMER_COMPOSE_PROJECT" "$@"
}

# claimer_identity: what must not change while the claimer follows new runs. Binary: the pid (and it is
# alive). Docker: the container id and its start time (a restart would change the latter).
claimer_identity() {
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        local id
        id="$(compose ps --status running --quiet 2>/dev/null | head -n 1)"
        [[ -n "$id" ]] || return 1
        docker inspect --format '{{.Id}} {{.State.StartedAt}} restarts={{.RestartCount}}' "$id"
    else
        local pid
        pid="$(cat "${CLAIMER_ID_FILE}.pid" 2>/dev/null || true)"
        [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null || return 1
        echo "pid=${pid}"
    fi
}
claimer_logs() {
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        compose logs --no-color 2>/dev/null | tail -n 200 >&2 || true
    else
        tail -n 200 "$CLAIMER_LOG" >&2 || true
    fi
}
health() { curl -sf "${CLAIMER_URL}/claimer/v2/health"; }

stop_claimer() {
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        if [[ -f "$CLAIMER_DOCKER_CONFIG" ]]; then
            compose down --remove-orphans >/dev/null 2>&1 || true
            rm -f "$CLAIMER_DOCKER_CONFIG"
        fi
    else
        local pid
        pid="$(cat "${CLAIMER_ID_FILE}.pid" 2>/dev/null || true)"
        if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            # Not a child of this shell: wait for it to go away.
            for _ in $(seq 1 30); do kill -0 "$pid" 2>/dev/null || break; sleep 1; done
        fi
    fi
    rm -f "${CLAIMER_ID_FILE}.pid" "$CLAIMER_ID_FILE"
}

case "${1:-}" in
start)
    START_CONFIG="${2:-}"
    [[ -f "$START_CONFIG" ]] || { log_error "usage: ${0##*/} start <exit_certificate config>"; exit 1; }
    [[ -n "${ARCHIVE_URI_BASE:-}" ]] || { log_error "ARCHIVE_URI_BASE is not set"; exit 1; }
    [[ -f "$CIRCLE_STATE_FILE" ]] || { log_error "state file not found: $CIRCLE_STATE_FILE"; exit 1; }
    # shellcheck source=/dev/null
    source "$CIRCLE_STATE_FILE"
    stop_claimer

    START_OUTPUT_REL="$(jq -r '.options.outputDir' "$START_CONFIG")"
    [[ "$START_OUTPUT_REL" == /* ]] && START_OUTPUT="$START_OUTPUT_REL" || START_OUTPUT="$(cd "$(dirname "$START_CONFIG")" && pwd)/${START_OUTPUT_REL}"
    START_CERT="${START_OUTPUT}/exit-certificate-full.json"
    [[ -f "$START_CERT" ]] || START_CERT="${START_OUTPUT}/exit-certificate-signed.json"
    START_HEIGHT="$(jq -r '.height' "$START_CERT")"
    NETWORK_ID="$(jq -r '.network_id' "$START_CERT")"

    # The claimer gets its own output dir (its L1 Info Tree DBs) and its own archive cache: the delta that
    # runs while it serves downloads the same archived run, and a cache dir belongs to one reader.
    rm -rf "${TMP_DIR}/output-kurtosis-claimer" "${TMP_DIR}/claimer-archive-cache"
    mkdir -p "${TMP_DIR}/output-kurtosis-claimer"
    jq '.options.outputDir = "./output-kurtosis-claimer" | .options.archiveCacheDir = "./claimer-archive-cache"' \
        "$START_CONFIG" > "$CLAIMER_CONFIG"
    # How API v2 finds a holder's Message exits: ZkMinterBurner (L2) → L1Escrow (L1), metadata
    # abi.encode(address beneficiary, uint256 amount), paid in L1 USDC.
    jq -n --argjson n "$CIRCLE_L2_NETWORK_ID" --arg o "$CIRCLE_ZK_MINTER_BURNER" --arg d "$CIRCLE_L1_ESCROW" \
        --arg t "$CIRCLE_L1_USDC" \
        '{messageRoutes: [{originNetwork: $n, originAddress: $o, destinationNetwork: 0, destinationAddress: $d,
                           metadataLayout: "address,uint256",
                           displayToken: {address: $t, symbol: "USDC", decimals: 6}}]}' > "$CLAIMER_ROUTES"

    log_info "🚀 Start exit_certificate_claimer in follow mode (${CLAIMER_MODE}) on ${CLAIMER_URL}"
    log_info "   archive base: ${ARCHIVE_URI_BASE} — starts at network ${NETWORK_ID}, height ${START_HEIGHT}; checks the next height every ${CLAIMER_FOLLOW_INTERVAL}s"
    if [[ "$CLAIMER_MODE" == "docker" ]]; then
        sed -e 's#//127\.0\.0\.1:#//host.docker.internal:#g' -e 's#//localhost:#//host.docker.internal:#g' \
            "$CLAIMER_CONFIG" > "$CLAIMER_DOCKER_CONFIG"
        if [[ "$CLAIMER_DOCKER_BUILD" == "1" ]]; then
            run_quiet compose up --detach --build
        else
            run_quiet compose up --detach
        fi
    else
        if [[ ! -x "$CLAIMER_BIN" ]]; then
            pushd "$AGGKIT_DIR" >/dev/null
            run_quiet make build-exit_certificate_claimer
            popd >/dev/null
        fi
        nohup "$CLAIMER_BIN" \
            --exit-certificate-config "$CLAIMER_CONFIG" \
            --run-source "$ARCHIVE_URI_BASE" --run-source-height "$START_HEIGHT" \
            --follow-interval-seconds "$CLAIMER_FOLLOW_INTERVAL" \
            --message-routes "$CLAIMER_ROUTES" \
            --address "$CLAIMER_HOST" --port "$CLAIMER_PORT" \
            >"$CLAIMER_LOG" 2>&1 &
        echo "$!" > "${CLAIMER_ID_FILE}.pid"
    fi

    log_info "⏳ Wait for the claimer to download the run, sync L1 and become ready (timeout ${CLAIMER_READY_TIMEOUT}s)"
    ready=0
    for (( i=0; i<CLAIMER_READY_TIMEOUT; i++ )); do
        if ! claimer_identity >/dev/null; then
            log_error "Claimer exited before becoming ready. Logs:"
            claimer_logs
            exit 1
        fi
        if health >/dev/null 2>&1; then ready=1; break; fi
        sleep 1
    done
    if [[ "$ready" -ne 1 ]]; then
        log_error "Claimer did not become ready within ${CLAIMER_READY_TIMEOUT}s. Logs:"
        claimer_logs
        exit 1
    fi
    claimer_identity > "$CLAIMER_ID_FILE"
    HEALTH="$(health)"
    EXPECTED_SOURCE="${ARCHIVE_URI_BASE%/}/${NETWORK_ID}/$(printf '%010d' "$START_HEIGHT")"
    if ! jq -e --argjson h "$START_HEIGHT" --arg s "$EXPECTED_SOURCE" --argjson i "$CLAIMER_FOLLOW_INTERVAL" \
        '.run.height == $h and .run.source == $s and .follow.interval_seconds == $i and .follow.next_height == $h + 1
         and (.api_versions | index(2) != null)' <<<"$HEALTH" >/dev/null; then
        log_error "unexpected /health of the follow-mode claimer (expected run ${EXPECTED_SOURCE}): $HEALTH"
        exit 1
    fi
    log_info "✅ Claimer is serving ${EXPECTED_SOURCE} from the archive and follows height $((START_HEIGHT + 1))"
    ;;
wait-height)
    WANT="${2:-}"
    [[ "$WANT" =~ ^[0-9]+$ ]] || { log_error "usage: ${0##*/} wait-height <height>"; exit 1; }
    [[ -f "$CLAIMER_ID_FILE" ]] || { log_error "the follow-mode claimer was not started (${CLAIMER_ID_FILE} missing)"; exit 1; }
    log_info "⏳ Wait for the claimer to switch by itself to the run of height ${WANT} (timeout ${CLAIMER_FOLLOW_TIMEOUT}s)"
    switched=0
    for (( i=0; i<CLAIMER_FOLLOW_TIMEOUT; i++ )); do
        if ! claimer_identity >/dev/null; then
            log_error "the claimer died while following. Logs:"
            claimer_logs
            exit 1
        fi
        if [[ "$(health | jq -r '.run.height' 2>/dev/null || true)" == "$WANT" ]]; then switched=1; break; fi
        sleep 1
    done
    HEALTH="$(health || true)"
    if [[ "$switched" -ne 1 ]]; then
        log_error "the claimer did not switch to height ${WANT} within ${CLAIMER_FOLLOW_TIMEOUT}s. /health: ${HEALTH}. Logs:"
        claimer_logs
        exit 1
    fi
    if [[ "$(claimer_identity)" != "$(cat "$CLAIMER_ID_FILE")" ]]; then
        log_error "the claimer was restarted while following: $(cat "$CLAIMER_ID_FILE") → $(claimer_identity)"
        exit 1
    fi
    if ! jq -e --argjson h "$WANT" '(.follow.last_switch_time | type == "string") and .follow.next_height == $h + 1 and .status == "ok"' \
        <<<"$HEALTH" >/dev/null; then
        log_error "unexpected /health after the switch: $HEALTH"
        exit 1
    fi
    log_info "✅ Same claimer process ($(cat "$CLAIMER_ID_FILE")), now serving $(jq -r '.run.source' <<<"$HEALTH") (switched at $(jq -r '.follow.last_switch_time' <<<"$HEALTH"))"
    ;;
stop)
    stop_claimer
    ;;
*)
    log_error "usage: ${0##*/} start <exit_certificate config> | wait-height <height> | stop"
    exit 1
    ;;
esac
