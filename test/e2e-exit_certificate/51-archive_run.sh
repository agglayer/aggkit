#!/usr/bin/env bash
# Archives a settled run with `--step upload` in a GCS emulator (fake-gcs-server), the way a real run is
# archived in the write-once LET archive bucket, and checks the upload is idempotent.
# Usage: 51-archive_run.sh <config> <archive URI>   (e.g. tmp/exit_certificate-kurtosis.json gs://let-archive/runs/e2e-full)
#
# The config gets options.archiveURI set to <archive URI>. The emulator is started on first use (container
# ARCHIVE_EMULATOR_CONTAINER, port ARCHIVE_EMULATOR_PORT) with the bucket of the URI; the tool reaches it
# through STORAGE_EMULATOR_HOST. Stop it with: 51-archive_run.sh stop
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BINARY="${AGGKIT_DIR}/target/exit_certificate"
ARCHIVE_EMULATOR_IMAGE="${ARCHIVE_EMULATOR_IMAGE:-fsouza/fake-gcs-server:1.52.2}"
ARCHIVE_EMULATOR_CONTAINER="${ARCHIVE_EMULATOR_CONTAINER:-exit-certificate-e2e-gcs}"
ARCHIVE_EMULATOR_PORT="${ARCHIVE_EMULATOR_PORT:-14443}"
export STORAGE_EMULATOR_HOST="127.0.0.1:${ARCHIVE_EMULATOR_PORT}"

if [[ "${1:-}" == "stop" ]]; then
    docker rm -f "$ARCHIVE_EMULATOR_CONTAINER" >/dev/null 2>&1 || true
    exit 0
fi
if [[ $# -ne 2 ]]; then
    log_error "usage: ${0##*/} <config> <gs://bucket/prefix> | stop"
    exit 1
fi
CONFIG="$1"
ARCHIVE_URI="$2"
BUCKET="${ARCHIVE_URI#gs://}"
BUCKET="${BUCKET%%/*}"

for bin in docker jq curl; do
    command -v "$bin" >/dev/null || { log_error "$bin is required"; exit 1; }
done

# --- emulator (started once, reused by the next archives) ---
if [[ -z "$(docker ps -q -f "name=^${ARCHIVE_EMULATOR_CONTAINER}$")" ]]; then
    log_info "🪣 Start the GCS emulator (${ARCHIVE_EMULATOR_IMAGE}) on ${STORAGE_EMULATOR_HOST}"
    docker rm -f "$ARCHIVE_EMULATOR_CONTAINER" >/dev/null 2>&1 || true
    docker run -d --rm --name "$ARCHIVE_EMULATOR_CONTAINER" -p "${STORAGE_EMULATOR_HOST}:4443" \
        "$ARCHIVE_EMULATOR_IMAGE" -scheme http -port 4443 -public-host "$STORAGE_EMULATOR_HOST" -backend memory \
        >/dev/null
    for _ in $(seq 1 30); do
        curl -sf "http://${STORAGE_EMULATOR_HOST}/storage/v1/b" >/dev/null && break
        sleep 1
    done
fi
if ! curl -sf "http://${STORAGE_EMULATOR_HOST}/storage/v1/b/${BUCKET}" >/dev/null; then
    curl -sf -X POST -H 'Content-Type: application/json' -d "{\"name\":\"${BUCKET}\"}" \
        "http://${STORAGE_EMULATOR_HOST}/storage/v1/b" >/dev/null
fi

# --- upload ---
tmp="$(mktemp)"
jq --arg uri "$ARCHIVE_URI" '.options.archiveURI = $uri' "$CONFIG" > "$tmp" && mv "$tmp" "$CONFIG"
OUTPUT_REL="$(jq -r '.options.outputDir' "$CONFIG")"
[[ "$OUTPUT_REL" == /* ]] && OUTPUT_DIR="$OUTPUT_REL" || OUTPUT_DIR="$(cd "$(dirname "$CONFIG")" && pwd)/${OUTPUT_REL}"

log_info "📦 Archive ${OUTPUT_DIR} → ${ARCHIVE_URI}"
run_quiet "$BINARY" --config "$CONFIG" --step upload

PREFIX="${ARCHIVE_URI#gs://${BUCKET}/}"
MANIFEST_OBJECT="$(jq -rn --arg p "${PREFIX}/archive-manifest.json" '$p | @uri')"
REMOTE_MANIFEST="$(curl -sf "http://${STORAGE_EMULATOR_HOST}/storage/v1/b/${BUCKET}/o/${MANIFEST_OBJECT}?alt=media")"
LOCAL_MANIFEST="${OUTPUT_DIR}/archive-manifest.json"
[[ -f "$LOCAL_MANIFEST" ]] || { log_error "local manifest copy not written: $LOCAL_MANIFEST"; exit 1; }
if ! diff <(jq -S . <<<"$REMOTE_MANIFEST") <(jq -S . "$LOCAL_MANIFEST") >/dev/null; then
    log_error "the archived manifest differs from the local copy"
    exit 1
fi
for f in step-wait-result.json step-g-l2bridgesyncerlite.sqlite; do
    jq -e --arg f "$f" '.files | any(.path == $f)' "$LOCAL_MANIFEST" >/dev/null ||
        { log_error "$f is not in the archive"; exit 1; }
done
if jq -e '.files | any(.path | test("L1InfoTreeSync|multidownloader|-wal$|-shm$"))' "$LOCAL_MANIFEST" >/dev/null; then
    log_error "the archive holds files it must leave out (claimer L1 info tree DBs or SQLite side files)"
    exit 1
fi
log_info "  ✅ $(jq '.files | length' "$LOCAL_MANIFEST") files archived, manifest uploaded last"

# Re-running is a no-op (same files) and never overwrites anything.
run_quiet "$BINARY" --config "$CONFIG" --step upload
log_info "  ✅ a second upload of the same run is a no-op"
