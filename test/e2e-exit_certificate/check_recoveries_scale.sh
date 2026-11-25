#!/usr/bin/env bash
# Scale check of `exit_certificate --step recoveries` (delta mode) with a REAL version-1 recovery.json, outside
# the Kurtosis e2e. The e2e recovers a handful of outputs; the Circle USDC.e recovery of zkEVM has thousands.
# This script feeds that recovery to the tool offline and checks what the tool writes at that size:
#
#   1. normalise_input: takes the package (.tgz or unpacked dir), a case dir, or a bare recovery.json, and
#      prints what it holds (outputs, leaf types, decoded payout total). A recovery.json in the older shape
#      (numeric leaf_type + deposit_count) is converted to the current one, and the script says so.
#   2. build_synthetic_previous_run: the smallest state `--step recoveries` accepts (step-0-lbt.json,
#      step-c-sc-locked-values.json, step-f-checks.json; no certificate, no lite DB, no RPC).
#   3. run_recoveries: runs `--step recoveries` against unreachable RPC URLs (it needs none), timed.
#   4. check_outputs: delta-exits.json equals the outputs field by field and in order, one ledger entry per
#      output, duplicates reported, the run is deterministic, and the previous run is left untouched. A load
#      probe then feeds delta-exits.json to the delta pipeline, which loads and validates every exit
#      (LoadDeltaExits) and stops at step CHECK (it needs the live network).
#   5. summary: time, sizes, peak RSS and an ESTIMATE of the certificate size against the gRPC default limit.
#
# Step F (the agglayer balance check) does NOT run offline: in delta mode it only runs inside the whole delta
# pipeline, after step CHECK, and it always queries admin_getTokenBalance. With --delta-config the script also
# runs that pipeline on a live network (run_delta_pipeline_live).
#
# The input data is not part of this repo: it is the v2 package of the Circle USDC recovery,
# `zkevm-circle-usdc-upgrade-submission.tgz`, obtained separately. Pass it as an argument (or set
# CIRCLE_USDC_RECOVERY). Needs jq and bc; /usr/bin/time (GNU) is used for the peak RSS when present.
set -euo pipefail
export LC_ALL=C

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/helper.sh"

AGGKIT_DIR="$(cd "${SCRIPT_DIR}/../.." && pwd)"
BINARY="${EXIT_CERT_BINARY:-${AGGKIT_DIR}/target/exit_certificate}"
# The exiting network (zkEVM mainnet is network 1): a Message output must come from it.
L2_NETWORK_ID="${L2_NETWORK_ID:-1}"
# Every URL of the derived config points here: nothing listens on port 1, so any RPC attempt fails loudly.
UNREACHABLE_URL="http://127.0.0.1:1"
# Any non-empty address satisfies the config loader; the recoveries step never calls it. zkEVM's bridge.
L2_BRIDGE_ADDRESS="0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe"
# grpc-go's default maximum received message size. It is the library default, NOT a verified agglayer limit.
GRPC_DEFAULT_MAX_MSG=4194304
ZERO_HASH="0x0000000000000000000000000000000000000000000000000000000000000000"

usage() {
    cat >&2 <<EOF
Usage: $(basename "$0") <package.tgz | package dir | case dir | recovery.json> [--work-dir <dir>] [--keep]

Feeds a real version-1 recovery.json to 'exit_certificate --step recoveries' (delta mode) offline and checks
the delta exits file it writes at that size (exact match with the outputs, ledger, duplicates, determinism),
then prints timing, sizes and an estimate of the certificate size.

Input: the v2 package of the Circle USDC recovery, 'zkevm-circle-usdc-upgrade-submission.tgz' (obtained
separately, not part of this repo), either as the .tgz or unpacked; its case/ directory; or a bare
recovery.json. The input can also be given with CIRCLE_USDC_RECOVERY. The input must hold exactly one
recovery.json.

Options:
  --work-dir <dir>   where to write the synthetic previous run, configs, outputs and logs (must not exist or
                     be empty; it is kept). Default: a temporary dir, removed at the end unless --keep.
  --keep             keep the temporary work dir.
  --delta-config <f> also run the whole delta pipeline (CHECK, F, G2, H, I, SIGN) with these exits on a live
                     network: <f> is the exit_certificate config of its last SETTLED run (the exits are
                     chained on it). The network must be the one the outputs come from (L2_NETWORK_ID).
  --submit           with --delta-config: also submit the certificate and wait for settlement. This changes
                     the network (use a test network).
  -h, --help         this help.

Environment: EXIT_CERT_BINARY (default target/exit_certificate, built if missing), L2_NETWORK_ID (default 1),
VERBOSE=1 (show the build output).
EOF
}

# --- arguments ---
INPUT="${CIRCLE_USDC_RECOVERY:-}"
WORK_DIR=""
KEEP=0
DELTA_CONFIG=""
LIVE_SUBMIT=0
LIVE_SUMMARY=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --work-dir) [[ $# -ge 2 ]] || { usage; exit 1; }; WORK_DIR="$2"; shift 2 ;;
        --keep) KEEP=1; shift ;;
        --delta-config) [[ $# -ge 2 ]] || { usage; exit 1; }; DELTA_CONFIG="$2"; shift 2 ;;
        --submit) LIVE_SUBMIT=1; shift ;;
        -h|--help) usage; exit 0 ;;
        -*) log_error "unknown option: $1"; usage; exit 1 ;;
        *) INPUT="$1"; shift ;;
    esac
done
if [[ -z "$INPUT" ]]; then
    log_error "no input: pass the package / case dir / recovery.json, or set CIRCLE_USDC_RECOVERY"
    usage
    exit 1
fi
[[ -e "$INPUT" ]] || { log_error "input not found: $INPUT"; exit 1; }
command -v jq >/dev/null || { log_error "jq is required"; exit 1; }
command -v bc >/dev/null || { log_error "bc is required (amounts may exceed jq's 2^53 precision)"; exit 1; }
export BC_LINE_LENGTH=0

abs_path() { echo "$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"; }
INPUT="$(abs_path "$INPUT")"

CREATED_WORK_DIR=0
if [[ -n "$WORK_DIR" ]]; then
    if [[ -e "$WORK_DIR" && -n "$(ls -A "$WORK_DIR")" ]]; then
        log_error "--work-dir $WORK_DIR is not empty"
        exit 1
    fi
    mkdir -p "$WORK_DIR"
    WORK_DIR="$(cd "$WORK_DIR" && pwd)"
else
    WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/check-recoveries-scale.XXXXXX")"
    CREATED_WORK_DIR=1
fi
cleanup() {
    if [[ "$CREATED_WORK_DIR" -eq 1 && "$KEEP" -eq 0 ]]; then
        rm -rf "$WORK_DIR"
    else
        log_info "Work dir kept: $WORK_DIR"
    fi
}
trap cleanup EXIT

FAILURES=0
# check <description> <command...>: runs the command (a failing one must not abort under set -e) and
# reports ✅/❌. Use `test`, not `[[ ]]`, for the conditions.
check() {
    local desc="$1"
    shift
    if "$@"; then
        log_info "  ✅ $desc"
    else
        log_error "  ❌ $desc"
        FAILURES=$((FAILURES + 1))
    fi
}

# Field-by-field projection of an exit, used to compare recovery.json outputs with delta-exits.json exits:
# leaf type, origin network, origin address, destination network, destination address, amount, metadata
# (addresses and metadata lower-case, metadata without 0x; a missing/null metadata is "").
JQ_OUTPUT_FIELDS='[.leaf_type, .token_info.origin_network, (.token_info.origin_token_address | ascii_downcase),
    .destination_network, (.destination_address | ascii_downcase), .amount,
    ((.metadata // "") | ascii_downcase | ltrimstr("0x"))] | @json'
JQ_EXIT_FIELDS='[.leaf_type, .token_info.origin_network, (.token_info.origin_token_address | ascii_downcase),
    .dest_network, (.dest_address | ascii_downcase), .amount,
    ((.metadata // "") | ascii_downcase | ltrimstr("0x"))] | @json'

# --- 1. input ---
INPUT_DIR="${WORK_DIR}/input"        # normalised copy: case/recovery.json [+ move.sh, case-hash.txt], extras
RECOVERY="${INPUT_DIR}/case/recovery.json"
RECOVERIES_PATH=""                   # what options.recoveriesPath gets
CONVERTED=0
PACKAGE_DELTA_EXITS=""               # the delta exits file a package may ship next to case/
PACKAGE_SUMS=""                      # its SHA256SUMS

# normalise_input copies the one recovery.json of the input (and what sits next to it: move.sh, the kit's
# case-hash.txt; at the package root: SHA256SUMS and a delta-exits*.json) into INPUT_DIR. A .tgz is never
# extracted as a whole: it is untrusted, so only those members are read, by name, to stdout.
normalise_input() {
    mkdir -p "${INPUT_DIR}/case"
    local case_rel root_rel f
    if [[ -f "$INPUT" && ( "$INPUT" == *.tgz || "$INPUT" == *.tar.gz ) ]]; then
        local -a members
        mapfile -t members < <(tar -tzf "$INPUT")
        local -a recs=()
        for f in "${members[@]}"; do
            [[ "$f" == recovery.json || "$f" == */recovery.json ]] && recs+=("$f")
        done
        if [[ ${#recs[@]} -ne 1 ]]; then
            log_error "$INPUT must hold exactly one recovery.json, found ${#recs[@]}: ${recs[*]:-none}"
            exit 1
        fi
        case_rel="$(dirname "${recs[0]}")"
        root_rel="$case_rel"
        [[ "$(basename "$case_rel")" == case ]] && root_rel="$(dirname "$case_rel")"
        for f in "${members[@]}"; do
            case "$f" in
                "${case_rel}/recovery.json"|recovery.json) tar -xzOf "$INPUT" "$f" > "$RECOVERY" ;;
                "${case_rel}/move.sh"|move.sh) tar -xzOf "$INPUT" "$f" > "${INPUT_DIR}/case/move.sh" ;;
                "${case_rel}/case-hash.txt"|case-hash.txt) tar -xzOf "$INPUT" "$f" > "${INPUT_DIR}/case/case-hash.txt" ;;
                "${root_rel}/SHA256SUMS"|SHA256SUMS) tar -xzOf "$INPUT" "$f" > "${INPUT_DIR}/SHA256SUMS" ;;
                "${root_rel}"/delta-exits*.json|delta-exits*.json)
                    [[ "$(dirname "$f")" == "$root_rel" ]] && tar -xzOf "$INPUT" "$f" > "${INPUT_DIR}/delta-exits.json" ;;
            esac
        done
        log_info "📦 Input: package archive $(basename "$INPUT") (recovery.json at ${recs[0]})"
    elif [[ -d "$INPUT" ]]; then
        local -a recs
        mapfile -t recs < <(find "$INPUT" -type f -name recovery.json | sort)
        if [[ ${#recs[@]} -ne 1 ]]; then
            log_error "$INPUT must hold exactly one recovery.json, found ${#recs[@]}: ${recs[*]:-none}"
            exit 1
        fi
        case_rel="$(dirname "${recs[0]}")"
        root_rel="$case_rel"
        # The package root is read only when it is inside the input (not when the input is the case dir).
        [[ "$(basename "$case_rel")" == case && "$case_rel" != "$INPUT" ]] && root_rel="$(dirname "$case_rel")"
        cp "${recs[0]}" "$RECOVERY"
        [[ -f "${case_rel}/move.sh" ]] && cp "${case_rel}/move.sh" "${INPUT_DIR}/case/move.sh"
        [[ -f "${case_rel}/case-hash.txt" ]] && cp "${case_rel}/case-hash.txt" "${INPUT_DIR}/case/case-hash.txt"
        [[ -f "${root_rel}/SHA256SUMS" ]] && cp "${root_rel}/SHA256SUMS" "${INPUT_DIR}/SHA256SUMS"
        local -a deltas
        mapfile -t deltas < <(find "$root_rel" -maxdepth 1 -type f -name 'delta-exits*.json')
        [[ ${#deltas[@]} -eq 1 ]] && cp "${deltas[0]}" "${INPUT_DIR}/delta-exits.json"
        log_info "📁 Input: directory $INPUT (recovery.json at ${recs[0]#"$INPUT"/})"
    elif [[ -f "$INPUT" ]]; then
        cp "$INPUT" "$RECOVERY"
        [[ -f "$(dirname "$INPUT")/move.sh" ]] && cp "$(dirname "$INPUT")/move.sh" "${INPUT_DIR}/case/move.sh"
        log_info "📄 Input: recovery file $INPUT"
    else
        log_error "unsupported input: $INPUT"
        exit 1
    fi
    [[ -f "${INPUT_DIR}/delta-exits.json" ]] && PACKAGE_DELTA_EXITS="${INPUT_DIR}/delta-exits.json"
    [[ -f "${INPUT_DIR}/SHA256SUMS" ]] && PACKAGE_SUMS="${INPUT_DIR}/SHA256SUMS"

    jq -e 'type == "object"' "$RECOVERY" >/dev/null 2>&1 || { log_error "recovery.json is not a JSON object"; exit 1; }
    if ! jq -e '.version == 1 and (.outputs | type == "array")' "$RECOVERY" >/dev/null; then
        log_error "recovery.json is not version 1 ({\"version\":1,\"outputs\":[...]}): this check only covers the outputs format"
        exit 1
    fi

    # Older shape of the package: numeric leaf_type (0 = Transfer, 1 = Message) and an extra deposit_count.
    if jq -e '[.outputs[] | ((.leaf_type | type) == "number") or has("deposit_count")] | any' "$RECOVERY" >/dev/null; then
        CONVERTED=1
        log_warn "════════════════════════════════════════════════════════════════════════"
        log_warn "⚠️  OLD-SHAPE recovery.json: numeric leaf_type and/or deposit_count."
        log_warn "⚠️  CONVERTED for this check: leaf_type 0/1 -> \"Transfer\"/\"Message\", deposit_count dropped."
        log_warn "⚠️  The converted file is NOT the one that was signed: its case hash differs from the kit's."
        log_warn "════════════════════════════════════════════════════════════════════════"
        mv "$RECOVERY" "${INPUT_DIR}/recovery.original.json"
        jq '.outputs |= map(
                (if (.leaf_type | type) == "number" then
                    .leaf_type |= (if . == 0 then "Transfer" elif . == 1 then "Message"
                                   else error("unknown numeric leaf_type \(.)") end)
                 else . end)
                | del(.deposit_count))' "${INPUT_DIR}/recovery.original.json" > "$RECOVERY"
    fi

    # options.recoveriesPath: the package .tgz itself when it needs no conversion (the tool reads recovery.json
    # and move.sh from it in memory, which also exercises that reader), else the normalised case dir.
    if [[ "$CONVERTED" -eq 0 && -f "$INPUT" && ( "$INPUT" == *.tgz || "$INPUT" == *.tar.gz ) ]]; then
        RECOVERIES_PATH="$INPUT"
    else
        RECOVERIES_PATH="${INPUT_DIR}/case"
    fi

    OUTPUTS="$(jq '.outputs | length' "$RECOVERY")"
    LEAF_TYPES="$(jq -r '[.outputs[].leaf_type] | group_by(.) | map("\(.[0]): \(length)") | join(", ")' "$RECOVERY")"
    TRANSFERS="$(jq '[.outputs[] | select(.leaf_type == "Transfer")] | length' "$RECOVERY")"
    MESSAGES="$(jq '[.outputs[] | select(.leaf_type == "Message")] | length' "$RECOVERY")"
    # A Message's metadata here is abi.encode(address holder, uint256 amount): 2 words, the first one a
    # left-padded address. The amount (2nd word) is summed with bc: jq numbers lose precision above 2^53.
    local abi_ok='(ltrimstr("0x") | length == 128 and (.[0:24] | test("^0+$")))'
    MSG_NON_ABI="$(jq "[.outputs[] | select(.leaf_type == \"Message\") | .metadata | select(${abi_ok} | not)] | length" "$RECOVERY")"
    MSG_TOTAL="$( { echo "ibase=16; s=0"
        jq -r ".outputs[] | select(.leaf_type == \"Message\") | .metadata | select(${abi_ok}) | ltrimstr(\"0x\")
            | .[64:128] | ascii_upcase | \"s+=\" + ." "$RECOVERY"
        echo "s"; } | bc)"
    MSG_HOLDERS="$(jq -r ".outputs[] | select(.leaf_type == \"Message\") | .metadata | select(${abi_ok})
            | ltrimstr(\"0x\") | .[24:64] | ascii_downcase" "$RECOVERY" | sort -u | wc -l)"
    TRANSFER_TOTAL="$( { echo "s=0"
        jq -r '.outputs[] | select(.leaf_type == "Transfer") | "s+=" + .amount' "$RECOVERY"
        echo "s"; } | bc)"
    log_info "🔢 recovery.json: ${OUTPUTS} outputs (${LEAF_TYPES:-none}); $(wc -c < "$RECOVERY") bytes"
    log_info "   senders/destinations: $(jq -r '[.outputs[] | "\(.token_info.origin_network)/\(.token_info.origin_token_address | ascii_downcase) -> \(.destination_network)/\(.destination_address | ascii_downcase)"] | unique | join("; ")' "$RECOVERY")"
    if [[ "$MESSAGES" -gt 0 ]]; then
        log_info "   Message payouts decoded from metadata (abi.encode(address,uint256)): total ${MSG_TOTAL} base units, ${MSG_HOLDERS} distinct holders"
        [[ "$MSG_NON_ABI" -eq 0 ]] || log_warn "   ${MSG_NON_ABI} Message metadata are not abi.encode(address,uint256): left out of that total"
    fi
    [[ "$TRANSFERS" -eq 0 ]] || log_info "   Transfer amounts: total ${TRANSFER_TOTAL} base units (all tokens)"
    log_info "   options.recoveriesPath = ${RECOVERIES_PATH}"
}

# --- 2. synthetic previous run ---
PREV_DIR="${WORK_DIR}/previous-run"

# build_synthetic_previous_run writes the only files `--step recoveries` reads from options.previousRun
# (loadRecoveriesState): step-0-lbt.json, step-c-sc-locked-values.json and step-f-checks.json (plus an optional
# ledger, absent here: nothing applied yet). Without delta-manifest.json it is taken as a first, non-delta run
# (recoveriesStateDir), so no certificate, wait result or lite DB is needed. A Message consumes no balance, so
# a messages-only recovery needs empty lists. Each Transfer token gets an entry whose pending balance is
# exactly what the recovery takes (synthetic: the wrapped address is the origin address, except ETH).
build_synthetic_previous_run() {
    mkdir -p "$PREV_DIR"
    local sums="${WORK_DIR}/transfer-sums.txt" key net addr total
    : > "$sums"
    while read -r net addr; do
        key="${net} ${addr}"
        total="$( { echo "s=0"
            jq -r --argjson n "$net" --arg a "$addr" '.outputs[] | select(.leaf_type == "Transfer"
                and .token_info.origin_network == $n and (.token_info.origin_token_address | ascii_downcase) == $a)
                | "s+=" + .amount' "$RECOVERY"
            echo "s"; } | bc)"
        echo "${key} ${total}" >> "$sums"
    done < <(jq -r '.outputs[] | select(.leaf_type == "Transfer")
        | "\(.token_info.origin_network) \(.token_info.origin_token_address | ascii_downcase)"' "$RECOVERY" | sort -u)

    local zero_addr="0x0000000000000000000000000000000000000000"
    jq -R -s --arg z "$zero_addr" 'split("\n") | map(select(length > 0) | split(" "))
        | map({wrappedTokenAddress: (if .[1] == $z then $z else .[1] end), originNetwork: (.[0] | tonumber),
               originTokenAddress: .[1], balance: .[2]})' "$sums" > "${PREV_DIR}/step-0-lbt.json"
    jq -R -s --arg z "$zero_addr" 'split("\n") | map(select(length > 0) | split(" "))
        | map({wrappedTokenAddress: (if .[1] == $z then $z else .[1] end), originNetwork: (.[0] | tonumber),
               originTokenAddress: .[1], lbtBalance: .[2], eoaAccumulated: "0", totalSCLockedBalance: .[2],
               pendingSCLockedBalance: .[2]})' "$sums" > "${PREV_DIR}/step-c-sc-locked-values.json"
    jq -R -s 'split("\n") | map(select(length > 0) | split(" "))
        | map({originNetwork: (.[0] | tonumber), originTokenAddress: .[1], lbtAmount: "0", certificateAmount: "0",
               agglayerAmount: "0", match: true, skippedSCLockedAmount: .[2]})' "$sums" > "${PREV_DIR}/step-f-checks.json"
    log_info "🧱 Synthetic previous run: ${PREV_DIR} ($(wc -l < "$sums") token(s) with Transfer outputs; no certificate, no lite DB)"
    PREV_STATE_HASH="$(cd "$PREV_DIR" && sha256sum ./*)"
}

# --- 3. the recoveries step ---
TIME_V=0
/usr/bin/time -v -o /dev/null true >/dev/null 2>&1 && TIME_V=1

# write_config <file> <outputDir> <deltaExitsFile> <previousRun> [recoveriesPath]: a delta config whose RPC
# URLs are all unreachable.
write_config() {
    jq -n --arg u "$UNREACHABLE_URL" --arg bridge "$L2_BRIDGE_ADDRESS" --argjson net "$L2_NETWORK_ID" \
        --arg out "$2" --arg exits "$3" --arg prev "$4" --arg rec "${5:-}" '
        {l1RpcUrl: $u, l2RpcUrl: $u, l2BridgeAddress: $bridge, l2NetworkId: $net,
         options: ({deltaMode: true, previousRun: $prev, deltaExitsFile: $exits, outputDir: $out,
                    agglayerAdminURL: $u} + (if $rec == "" then {} else {recoveriesPath: $rec} end))}' > "$1"
}

ensure_binary() {
    if [[ ! -x "$BINARY" ]]; then
        log_info "🛠️ Build exit_certificate"
        pushd "$AGGKIT_DIR" >/dev/null
        run_quiet make build-exit_certificate
        popd >/dev/null
    fi
}

# run_recoveries <label>: runs `--step recoveries` into ${WORK_DIR}/output-<label>. Sets ELAPSED, PEAK_RSS_KB
# (empty without GNU time) and RUN_OUT (the recoveries/ dir). Fails the script if the step fails.
run_recoveries() {
    local label="$1"
    local out="${WORK_DIR}/output-${label}"
    local cfg="${WORK_DIR}/config-${label}.json"
    local logf="${WORK_DIR}/recoveries-${label}.log"
    local timef="${WORK_DIR}/time-${label}.txt"
    RUN_OUT="${out}/recoveries"
    write_config "$cfg" "$out" "${RUN_OUT}/delta-exits.json" "$PREV_DIR" "$RECOVERIES_PATH"
    local -a cmd=("$BINARY" --config "$cfg" --step recoveries)
    [[ "$TIME_V" -eq 1 ]] && cmd=(/usr/bin/time -v -o "$timef" "${cmd[@]}")
    log_info "🧾 --step recoveries (${label}) → ${out}"
    local start end rc
    start="$EPOCHREALTIME"
    if "${cmd[@]}" >"$logf" 2>&1; then rc=0; else rc=$?; fi
    end="$EPOCHREALTIME"
    if [[ "$rc" -ne 0 ]]; then
        log_error "--step recoveries failed (exit $rc); last lines of $logf:"
        tail -n 20 "$logf" >&2
        exit 1
    fi
    ELAPSED="$(printf '%.3f' "$(echo "scale=3; ($end - $start) / 1" | bc)")"
    PEAK_RSS_KB=""
    [[ -f "$timef" ]] && PEAK_RSS_KB="$(sed -n 's/.*Maximum resident set size (kbytes): *//p' "$timef")"
    # No RPC is needed: every URL is unreachable, so an attempt would show up as a dial error in the log.
    check "${label}: no RPC was attempted (no dial error in the log)" \
        bash -c '! grep -qiE "dial tcp|connection refused" "$1"' _ "$logf"
}

# --- 4. checks ---
check_outputs() {
    local exits="${OUT1}/delta-exits.json" ledger="${OUT1}/recoveries-applied.json"
    log_info "🔎 Checking the recoveries output"
    check "delta-exits.json has exactly ${OUTPUTS} exits (one per output)" \
        jq -e --argjson n "$OUTPUTS" '.bridge_exits | length == $n' "$exits" >/dev/null
    jq -r ".outputs[] | ${JQ_OUTPUT_FIELDS}" "$RECOVERY" > "${WORK_DIR}/outputs.fields"
    jq -r ".bridge_exits[] | ${JQ_EXIT_FIELDS}" "$exits" > "${WORK_DIR}/exits.fields"
    check "every exit equals its output field by field and in order (leaf_type, token_info, destination, amount, metadata)" \
        cmp -s "${WORK_DIR}/outputs.fields" "${WORK_DIR}/exits.fields"
    if ! cmp -s "${WORK_DIR}/outputs.fields" "${WORK_DIR}/exits.fields"; then
        diff "${WORK_DIR}/outputs.fields" "${WORK_DIR}/exits.fields" | head -n 6 >&2 || true
    fi
    check "the ledger has one entry per output (${OUTPUTS}), all of the same case" \
        jq -e --argjson n "$OUTPUTS" 'length == $n and ([.[].caseHash] | unique | length == 1)' "$ledger" >/dev/null
    check "the ledger entries follow the outputs (leaf type, destination, metadata, in order)" \
        cmp -s <(jq -r '.outputs[] | [.leaf_type, (.destination_address | ascii_downcase), (.metadata | ascii_downcase)] | @json' "$RECOVERY") \
               <(jq -r '.[] | [(.leafType // "Transfer"), (.destination | ascii_downcase), ((.metadata // "0x") | ascii_downcase)] | @json' "$ledger")
    LEDGER_CASE_HASH="$(jq -r '.[0].caseHash' "$ledger")"
    if [[ -f "${INPUT_DIR}/case/case-hash.txt" && "$CONVERTED" -eq 0 ]]; then
        check "the tool's case hash is the kit's case-hash.txt ($(tr -d '[:space:]' < "${INPUT_DIR}/case/case-hash.txt"))" \
            test "$LEDGER_CASE_HASH" = "$(tr -d '[:space:]' < "${INPUT_DIR}/case/case-hash.txt")"
    fi
    if [[ -n "$PACKAGE_SUMS" && "$CONVERTED" -eq 0 ]]; then
        check "case/recovery.json matches the package SHA256SUMS" \
            test "$(sha256sum < "$RECOVERY" | cut -d' ' -f1)" = "$(awk '$2 == "./case/recovery.json" || $2 == "case/recovery.json" {print $1}' "$PACKAGE_SUMS")"
        if [[ -f "${INPUT_DIR}/case/move.sh" ]]; then
            check "case/move.sh matches the package SHA256SUMS" \
                test "$(sha256sum < "${INPUT_DIR}/case/move.sh" | cut -d' ' -f1)" = "$(awk '$2 == "./case/move.sh" || $2 == "case/move.sh" {print $1}' "$PACKAGE_SUMS")"
        fi
    fi
    if [[ -n "$PACKAGE_DELTA_EXITS" ]]; then
        check "delta-exits.json equals the delta exits file shipped in the package (addresses case-insensitive)" \
            cmp -s "${WORK_DIR}/exits.fields" <(jq -r ".bridge_exits[] | ${JQ_EXIT_FIELDS}" "$PACKAGE_DELTA_EXITS")
    fi

    # Two identical outputs are two identical leaves (same hash). The recoveries step does not de-duplicate
    # version-1 outputs (one exit per output, never merged); the delta step G2 refuses them later
    # (rejectDuplicateLeaves), together with a leaf identical to one already in the previous exit tree,
    # which needs the real lite DB and cannot be checked here.
    DUP_OUTPUTS=$(( OUTPUTS - $(sort -u "${WORK_DIR}/outputs.fields" | wc -l) ))
    DUP_EXITS=$(( $(wc -l < "${WORK_DIR}/exits.fields") - $(sort -u "${WORK_DIR}/exits.fields" | wc -l) ))
    if [[ "$DUP_OUTPUTS" -eq 0 && "$DUP_EXITS" -eq 0 ]]; then
        log_info "  ✅ no two outputs / exits are identical"
    else
        log_warn "  ⚠️ identical outputs: ${DUP_OUTPUTS} extra copies in recovery.json, ${DUP_EXITS} in delta-exits.json."
        log_warn "     --step recoveries accepted them (it never merges version-1 outputs); the delta step G2"
        log_warn "     (rejectDuplicateLeaves) would refuse the certificate. First duplicate:"
        sort "${WORK_DIR}/outputs.fields" | uniq -d | head -n 1 >&2
    fi

    check "the previous run was not modified" test "$PREV_STATE_HASH" = "$(cd "$PREV_DIR" && sha256sum ./*)"

    # Determinism: the same state and cases into ANOTHER output dir give the same files (re-running into the
    # same dir is refused by the tool).
    local first_out="$OUT1" first_elapsed="$ELAPSED" first_rss="$PEAK_RSS_KB"
    run_recoveries rerun
    local f
    for f in delta-exits.json recoveries-applied.json step-c-sc-locked-values.json step-f-checks.json; do
        check "deterministic: a second run gives a byte-identical ${f}" cmp -s "${first_out}/${f}" "${RUN_OUT}/${f}"
    done
    ELAPSED="$first_elapsed"
    PEAK_RSS_KB="$first_rss"

    load_probe
}

# load_probe feeds delta-exits.json to the delta pipeline (`--step all`) with a stub previous run (a certificate
# with a dummy root, a Settled wait result and an empty lite DB file: loadPreviousRun only checks the DB exists).
# The pipeline loads and validates every exit (LoadDeltaExits) and then stops at step CHECK, which needs the
# L1/L2 RPCs and the agglayer: this probes the parsing of the delta file at this size, nothing after it.
load_probe() {
    local prev="${WORK_DIR}/probe-previous-run" out="${WORK_DIR}/probe-output"
    local cfg="${WORK_DIR}/config-probe.json" logf="${WORK_DIR}/probe.log" rc
    mkdir -p "$prev"
    jq -n --argjson net "$L2_NETWORK_ID" --arg z "$ZERO_HASH" \
        '{network_id: $net, height: 0, prev_local_exit_root: $z,
          new_local_exit_root: "0x1111111111111111111111111111111111111111111111111111111111111111",
          bridge_exits: [], imported_bridge_exits: []}' > "${prev}/exit-certificate-signed.json"
    # agglayertypes.CertificateStatus Settled = 4.
    jq -n '{certificateHash: "0x2222222222222222222222222222222222222222222222222222222222222222",
            finalStatus: 4, elapsedSeconds: 0}' > "${prev}/step-wait-result.json"
    : > "${prev}/step-g-l2bridgesyncerlite.sqlite"
    write_config "$cfg" "$out" "${OUT1}/delta-exits.json" "$prev"
    log_info "🧪 Load probe: the delta pipeline reads delta-exits.json (it stops at step CHECK, offline)"
    if "$BINARY" --config "$cfg" >"$logf" 2>&1; then rc=0; else rc=$?; fi
    check "load probe: the delta pipeline loaded and validated all ${OUTPUTS} exits (LoadDeltaExits)" \
        grep -q "Delta exits: ${OUTPUTS} (" "$logf"
    check "load probe: it then stopped at step CHECK (exit ${rc}), before step F" \
        bash -c '[[ "$1" -ne 0 ]] && grep -q "STEP CHECK" "$2" && ! grep -q "STEP F (delta)" "$2"' _ "$rc" "$logf"
}

# --- step F / live mode ---
# run_delta_pipeline_live is the --delta-config mode: chain these exits on a REAL settled run and run the
# whole delta pipeline (CHECK → F → G2 → H → I → SIGN), and SUBMIT/WAIT with --submit, on a live network. Step F
# cannot run offline here:
#   - delta mode has no single `--step f` (runDeltaSteps allows all, recoveries, sign, submit, wait, upload);
#   - inside `--step all` it runs after loadPreviousRun (certificate, Settled wait result, lite DB), LoadDeltaExits
#     and step CHECK, which needs the L1 RPC, the L2 RPC/bridge and the agglayer gRPC (settled LER/height);
#   - RunStepFDelta needs useAgglayerAdminToStepFCheck=true and agglayerAdminURL, and always calls
#     admin_getTokenBalance once, even when every exit is a value-less Message (groupBridgeExitsByToken leaves
#     them out, so for this recovery the comparison itself is empty and passes).
# Then G2 needs the previous run's real lite DB (root == previous new_local_exit_root; rejectDuplicateLeaves
# against every historical leaf) and the L2 RPC (gas token), H/I the agglayer and L1, SIGN a key.
run_delta_pipeline_live() {
    local base_dir prev_rel prev_abs live_cfg live_out start signed size log
    [[ -f "$DELTA_CONFIG" ]] || { log_error "--delta-config: file not found: $DELTA_CONFIG"; exit 1; }
    DELTA_CONFIG="$(abs_path "$DELTA_CONFIG")"
    base_dir="$(dirname "$DELTA_CONFIG")"
    if [[ "$(jq -r '.l2NetworkId' "$DELTA_CONFIG")" != "$L2_NETWORK_ID" ]]; then
        log_error "--delta-config: the network is $(jq -r '.l2NetworkId' "$DELTA_CONFIG") but the Message outputs come from network ${L2_NETWORK_ID}"
        exit 1
    fi
    prev_rel="$(jq -r '.options.outputDir' "$DELTA_CONFIG")"
    [[ "$prev_rel" == /* ]] && prev_abs="$prev_rel" || prev_abs="${base_dir}/${prev_rel}"
    [[ -f "${prev_abs}/step-wait-result.json" ]] || { log_error "--delta-config: its run is not settled (${prev_abs}/step-wait-result.json missing)"; exit 1; }
    # The derived config sits next to the given one so its relative paths (keystore, ...) stay valid.
    live_cfg="${base_dir}/$(basename "${DELTA_CONFIG%.json}")-scale-check.json"
    live_out="${WORK_DIR}/live-out"
    jq --arg prev "$prev_abs" --arg exits "${OUT1}/delta-exits.json" --arg out "$live_out" '
        .options.deltaMode = true | .options.previousRun = $prev | .options.deltaExitsFile = $exits
        | .options.outputDir = $out | del(.options.recoveriesPath) | del(.options.archiveURI)' "$DELTA_CONFIG" > "$live_cfg"

    log_info "🌐 Live mode: the delta pipeline (CHECK → F → G2 → H → I → SIGN) with the ${OUTPUTS} exits, chained on ${prev_abs}"
    log="${WORK_DIR}/live-pipeline.log"
    start="$(date +%s.%N)"
    if ! "$BINARY" --config "$live_cfg" >"$log" 2>&1; then
        tail -n 30 "$log" >&2
        log_error "the delta pipeline failed (log: $log)"
        rm -f "$live_cfg"
        exit 1
    fi
    LIVE_PIPELINE_SECONDS="$(echo "$(date +%s.%N) - $start" | bc)"
    signed="${live_out}/exit-certificate-signed.json"
    size="$(wc -c < "$signed")"
    check "live: the delta certificate carries the ${OUTPUTS} exits" \
        jq -e --argjson n "$OUTPUTS" '.bridge_exits | length == $n' "$signed" >/dev/null
    LIVE_SUMMARY="pipeline ${LIVE_PIPELINE_SECONDS} s; exit-certificate-signed.json ${size} bytes"
    if [[ "$LIVE_SUBMIT" -eq 1 ]]; then
        log_info "📤 Live mode: submit the certificate and wait for settlement (this changes the network)"
        log="${WORK_DIR}/live-submit.log"
        start="$(date +%s.%N)"
        if "$BINARY" --config "$live_cfg" --step submit,wait >"$log" 2>&1; then
            LIVE_SUMMARY="${LIVE_SUMMARY}; submit + wait $(echo "$(date +%s.%N) - $start" | bc) s"
        else
            tail -n 30 "$log" >&2
            LIVE_SUMMARY="${LIVE_SUMMARY}; SUBMIT/WAIT FAILED after $(echo "$(date +%s.%N) - $start" | bc) s (log: $log)"
        fi
        check "live: the agglayer accepted the certificate and it settled" \
            bash -c '[[ -f "$1" ]] && jq -e ".finalStatus == 4 or .finalStatus == \"Settled\"" "$1" >/dev/null' \
                _ "${live_out}/step-wait-result.json"
        grep -a -m1 "entered InError" "$log" | sed -e 's/\t{"pid".*$//' >&2 || true
    fi
    rm -f "$live_cfg"
}

# --- 5. summary ---
summary() {
    local exits="${OUT1}/delta-exits.json" n="$OUTPUTS"
    local hash64 header est_raw est_hash per_exit fit pct
    hash64="$(printf '%064d' 0)"
    # The certificate's JSON with these exits. The submitted certificate carries each exit's metadata as its
    # keccak256 hash (32 bytes, set by step G2), so the hash variant is the estimate of its size; the raw
    # variant is the same JSON with the metadata as in delta-exits.json. Compact JSON, without aggchain_data.
    local jq_cert='{network_id: $net, height: 1, prev_local_exit_root: $z, new_local_exit_root: $z,
        bridge_exits: $exits, imported_bridge_exits: [], l1_info_tree_leaf_count: 1}'
    header="$(jq -c -n --argjson net "$L2_NETWORK_ID" --arg z "$ZERO_HASH" --argjson exits '[]' "$jq_cert" | wc -c)"
    est_raw="$(jq -c --argjson net "$L2_NETWORK_ID" --arg z "$ZERO_HASH" \
        '.bridge_exits as $exits | '"$jq_cert" "$exits" | wc -c)"
    est_hash="$(jq -c --argjson net "$L2_NETWORK_ID" --arg z "$ZERO_HASH" --arg h "$hash64" \
        '(.bridge_exits | map(.metadata = (if (.metadata // "") == "" then null else $h end))) as $exits | '"$jq_cert" \
        "$exits" | wc -c)"
    per_exit="$(echo "($est_hash - $header) / $n" | bc)"
    fit="$(echo "($GRPC_DEFAULT_MAX_MSG - $header) / $per_exit" | bc)"
    pct="$(printf '%.1f' "$(echo "scale=3; 100 * $est_hash / $GRPC_DEFAULT_MAX_MSG" | bc)")"

    local rss="n/a (no GNU /usr/bin/time)"
    [[ -n "$PEAK_RSS_KB" ]] && rss="${PEAK_RSS_KB} KiB ($(echo "$PEAK_RSS_KB / 1024" | bc) MiB)"
    row() { printf '%-64s %s\n' "$1" "$2"; }
    echo
    echo "=== check_recoveries_scale summary ==="
    row "outputs (recovery.json)" "${n} (${LEAF_TYPES})"
    [[ "$CONVERTED" -eq 1 ]] && row "input shape" "OLD (numeric leaf_type / deposit_count), CONVERTED"
    [[ "$MESSAGES" -gt 0 ]] && row "Message payouts decoded (metadata word 2)" "${MSG_TOTAL} base units, ${MSG_HOLDERS} holders"
    [[ "$TRANSFERS" -gt 0 ]] && row "Transfer amounts" "${TRANSFER_TOTAL} base units"
    row "identical outputs (extra copies)" "${DUP_OUTPUTS}"
    row "--step recoveries elapsed (wall)" "${ELAPSED} s"
    row "--step recoveries peak RSS" "$rss"
    row "recovery.json" "$(wc -c < "$RECOVERY") bytes"
    row "delta-exits.json" "$(wc -c < "$exits") bytes"
    row "recoveries-applied.json (ledger)" "$(wc -c < "${OUT1}/recoveries-applied.json") bytes"
    row "recoveries-report.md" "$(wc -c < "${OUT1}/recoveries-report.md") bytes"
    row "ESTIMATE certificate JSON, metadata as 32-byte hash" "${est_hash} bytes (${per_exit} bytes/exit)"
    row "ESTIMATE certificate JSON, raw metadata" "${est_raw} bytes"
    row "  vs 4 MiB (grpc-go default max message, not an agglayer limit)" \
        "${pct}% — about ${fit} such exits would fit, in JSON terms"
    echo "The estimates are the compact JSON of a certificate carrying these exits; the gRPC request is protobuf"
    echo "(binary addresses/hashes), so its size differs. The agglayer's real request and proof limits are not"
    echo "verified here."
}

# --- main ---
normalise_input
build_synthetic_previous_run
ensure_binary
run_recoveries first
OUT1="$RUN_OUT"
for f in delta-exits.json recoveries-applied.json recoveries-report.md step-c-sc-locked-values.json \
         step-f-checks.json step-0-lbt.json; do
    [[ -f "${OUT1}/${f}" ]] || { log_error "recoveries output not found: ${OUT1}/${f}"; exit 1; }
done
check_outputs
if [[ -n "$DELTA_CONFIG" ]]; then
    run_delta_pipeline_live
fi
summary
[[ -z "$LIVE_SUMMARY" ]] || echo "live network (--delta-config): ${LIVE_SUMMARY}"

if [[ "$FAILURES" -ne 0 ]]; then
    log_error "$FAILURES check(s) failed (rerun with --keep or --work-dir to inspect the outputs)"
    exit 1
fi
log_info "✅ --step recoveries handled the ${OUTPUTS} outputs: exact, deterministic, offline"
