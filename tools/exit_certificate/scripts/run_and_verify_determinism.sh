#!/usr/bin/env bash
# Runs the exit_certificate full pipeline N times (default 2) from the same JSON config and
# verifies the result: each run must produce a valid final certificate, and every run must
# produce byte-identical output (AET determinism regression check).
#
# To make the comparison fair against a live chain, the script resolves the L2 target block and
# the L1 cutoff block ONCE and pins them (targetBlock / options.l1EndBlock) in every generated
# per-run config, so all runs see the same on-chain snapshot. Each run gets its own fresh
# outputDir under a scratch workdir.
#
# Per run it checks:
#   - the pipeline exits 0 and writes exit-certificate-final.json
#   - new_local_exit_root is set (non-zero) and matches step-g-new-local-exit-root.json
#   - the bridge_exits order in the final certificate equals the pre-G order
#     (step-f-capped-certificate.json when present, otherwise step-e-exit-certificate.json)
# Across runs it checks:
#   - exit-certificate-final.json is identical in every run (hard failure otherwise)
#   - exit-certificate-signed.json is identical when present (warning otherwise)
#
# Requires: go (unless --binary points to a prebuilt one), jq, curl.
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
Usage: $0 [OPTIONS]

Runs the exit_certificate pipeline several times from the same config and verifies
the final certificate is valid and identical across runs.

Options:
  -c, --config  PATH   JSON config file (default: tmp/exit_certificate-kurtosis.json, repo-relative)
  -r, --runs    N      Number of pipeline runs to compare (default: 2, minimum 1)
  -b, --binary  PATH   Prebuilt exit_certificate binary (default: build via make build-exit_certificate)
  -w, --workdir PATH   Scratch dir for per-run configs/outputs (default: mktemp under /tmp)
  -k, --keep           Keep the workdir on success (always kept on failure)
  -h, --help           Show this help
EOF
    exit 1
}

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
CONFIG="$REPO_ROOT/tmp/exit_certificate-kurtosis.json"
RUNS=2
BINARY=""
WORKDIR=""
KEEP=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        -c|--config)  CONFIG="$2"; shift 2 ;;
        -r|--runs)    RUNS="$2"; shift 2 ;;
        -b|--binary)  BINARY="$2"; shift 2 ;;
        -w|--workdir) WORKDIR="$2"; shift 2 ;;
        -k|--keep)    KEEP=true; shift ;;
        -h|--help)    usage ;;
        *) log_error "Unknown option: $1"; usage ;;
    esac
done

for tool in jq curl; do
    command -v "$tool" >/dev/null || { log_error "$tool is required"; exit 1; }
done
[[ -f "$CONFIG" ]] || { log_error "Config not found: $CONFIG"; exit 1; }
[[ "$CONFIG" == *.json ]] || { log_error "Only JSON configs are supported (got: $CONFIG)"; exit 1; }
[[ "$RUNS" =~ ^[0-9]+$ && "$RUNS" -ge 1 ]] || { log_error "--runs must be a positive integer"; exit 1; }
CONFIG="$(cd "$(dirname "$CONFIG")" && pwd)/$(basename "$CONFIG")"
CONFIG_DIR="$(dirname "$CONFIG")"

# --- binary ---------------------------------------------------------------------------------------
if [[ -z "$BINARY" ]]; then
    log_info "Building exit_certificate (make build-exit_certificate)..."
    make -C "$REPO_ROOT" build-exit_certificate >/dev/null
    BINARY="$REPO_ROOT/target/exit_certificate"
fi
[[ -x "$BINARY" ]] || { log_error "Binary not executable: $BINARY"; exit 1; }

# --- workdir --------------------------------------------------------------------------------------
if [[ -z "$WORKDIR" ]]; then
    WORKDIR="$(mktemp -d /tmp/exit-cert-determinism.XXXXXX)"
else
    mkdir -p "$WORKDIR"
fi
log_info "Workdir: $WORKDIR"

# --- pin the on-chain snapshot --------------------------------------------------------------------
rpc_block_number() {
    local url="$1"
    curl -s -m 10 -X POST "$url" -H 'Content-Type: application/json' \
        -d '{"jsonrpc":"2.0","id":1,"method":"eth_blockNumber","params":[]}' \
        | jq -re '.result' | xargs printf '%d\n'
}

L2_RPC_URL="$(jq -re '.l2RpcUrl' "$CONFIG")"
L1_RPC_URL="$(jq -r '.l1RpcUrl // empty' "$CONFIG")"

L2_TARGET_BLOCK="$(rpc_block_number "$L2_RPC_URL")" \
    || { log_error "Cannot resolve L2 latest block from $L2_RPC_URL"; exit 1; }
log_info "Pinned L2 targetBlock: $L2_TARGET_BLOCK"

L1_END_BLOCK=""
if [[ -n "$L1_RPC_URL" ]]; then
    L1_END_BLOCK="$(rpc_block_number "$L1_RPC_URL")" \
        || { log_error "Cannot resolve L1 latest block from $L1_RPC_URL"; exit 1; }
    log_info "Pinned L1 l1EndBlock: $L1_END_BLOCK"
fi

# --- per-run configs ------------------------------------------------------------------------------
# Same pinned blocks in every run; fresh outputDir per run. signerConfig.Path is made absolute
# (it resolves relative to the config file, which moves into the workdir).
make_run_config() {
    local run_dir="$1" out="$2"
    jq --arg out "$run_dir" \
       --arg target "$L2_TARGET_BLOCK" \
       --arg l1end "$L1_END_BLOCK" \
       --arg cfgdir "$CONFIG_DIR" \
       '
        .options.outputDir = $out
        | .targetBlock = $target
        | (if $l1end != "" then .options.l1EndBlock = ($l1end | tonumber) else . end)
        | (if (.signerConfig.Path? // "") != "" and (.signerConfig.Path | startswith("/") | not)
           then .signerConfig.Path = $cfgdir + "/" + .signerConfig.Path else . end)
       ' "$CONFIG" > "$out"
}

# --- run + per-run checks -------------------------------------------------------------------------
FAILURES=0
check() { # check <description> <condition-exit-code>
    if [[ "$2" -eq 0 ]]; then
        log_info "  ✅ $1"
    else
        log_error "  ❌ $1"
        FAILURES=$((FAILURES + 1))
    fi
}

for i in $(seq 1 "$RUNS"); do
    RUN_DIR="$WORKDIR/run-$i"
    RUN_CONFIG="$WORKDIR/config-run-$i.json"
    mkdir -p "$RUN_DIR"
    make_run_config "$RUN_DIR" "$RUN_CONFIG"

    log_info "Run $i/$RUNS: $BINARY --config $RUN_CONFIG (log: $WORKDIR/run-$i.log)"
    if ! "$BINARY" --config "$RUN_CONFIG" > "$WORKDIR/run-$i.log" 2>&1; then
        log_error "Run $i failed — last log lines:"
        tail -5 "$WORKDIR/run-$i.log" >&2
        FAILURES=$((FAILURES + 1))
        continue
    fi

    FINAL="$RUN_DIR/exit-certificate-final.json"
    log_info "Run $i checks:"
    [[ -f "$FINAL" ]]; check "exit-certificate-final.json written" $?
    [[ -f "$FINAL" ]] || continue

    LER="$(jq -r '.new_local_exit_root // empty' "$FINAL")"
    [[ -n "$LER" && "$LER" != "0x0000000000000000000000000000000000000000000000000000000000000000" ]]
    check "new_local_exit_root is set ($LER)" $?

    STEP_G_LER="$(jq -r '.newLocalExitRoot // empty' "$RUN_DIR/step-g-new-local-exit-root.json" 2>/dev/null)"
    [[ -n "$STEP_G_LER" && "${LER,,}" == "${STEP_G_LER,,}" ]]
    check "final cert LER matches step G result" $?

    # The bridge_exits order must be the deterministic pre-G order: Step G never reorders.
    PRE_G="$RUN_DIR/step-f-capped-certificate.json"
    [[ -f "$PRE_G" ]] || PRE_G="$RUN_DIR/step-e-exit-certificate.json"
    diff <(jq '[.bridge_exits[] | {t: .token_info.origin_token_address, d: .dest_address, a: .amount}]' "$PRE_G") \
         <(jq '[.bridge_exits[] | {t: .token_info.origin_token_address, d: .dest_address, a: .amount}]' "$FINAL") \
         >/dev/null
    check "bridge_exits keep the pre-G order ($(basename "$PRE_G"), $(jq '.bridge_exits | length' "$FINAL") exits)" $?
done

# --- cross-run determinism checks -----------------------------------------------------------------
if [[ "$RUNS" -ge 2 && -f "$WORKDIR/run-1/exit-certificate-final.json" ]]; then
    log_info "Cross-run checks (vs run 1):"
    for i in $(seq 2 "$RUNS"); do
        [[ -f "$WORKDIR/run-$i/exit-certificate-final.json" ]] || continue
        diff <(jq -S . "$WORKDIR/run-1/exit-certificate-final.json") \
             <(jq -S . "$WORKDIR/run-$i/exit-certificate-final.json") >/dev/null
        check "run $i final certificate identical to run 1" $?

        if [[ -f "$WORKDIR/run-1/exit-certificate-signed.json" && -f "$WORKDIR/run-$i/exit-certificate-signed.json" ]]; then
            if ! diff <(jq -S . "$WORKDIR/run-1/exit-certificate-signed.json") \
                      <(jq -S . "$WORKDIR/run-$i/exit-certificate-signed.json") >/dev/null; then
                log_warn "  ⚠️  run $i signed certificate differs from run 1 (signature-level difference)"
            else
                log_info "  ✅ run $i signed certificate identical to run 1"
            fi
        fi
    done
fi

# --- verdict --------------------------------------------------------------------------------------
if [[ "$FAILURES" -eq 0 ]]; then
    log_info "All checks passed ($RUNS run(s))."
    if [[ "$KEEP" == false ]]; then
        rm -rf "$WORKDIR"
    else
        log_info "Workdir kept: $WORKDIR"
    fi
    exit 0
fi
log_error "$FAILURES check(s) failed. Workdir kept for inspection: $WORKDIR"
exit 1
