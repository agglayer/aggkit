#!/usr/bin/env bash
# Shared helpers for the e2e-exit_certificate scripts: colors and logging.
# Source it from a script with:
#   source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/helper.sh"

GREEN='\033[0;32m'
RED='\033[0;31m'
ORANGE='\033[0;33m'
NC='\033[0m'

log_info() { echo -e "${GREEN}[INFO]${NC} $*" >&2; }
log_warn() { echo -e "${ORANGE}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# Number of trailing output lines shown live by run_quiet.
RUN_QUIET_TAIL="${RUN_QUIET_TAIL:-3}"

# run_quiet runs a command capturing all its output (stdout + stderr) to a temp
# file. While it runs, the last RUN_QUIET_TAIL emitted lines are shown in a
# fixed block that is redrawn in place (ANSI) on each new line. On success it
# clears the block; on failure it dumps the whole output and propagates the
# exit code.
# Set VERBOSE=1 to disable all of this: the command's output is shown in full
# as it runs, with no temp file and no fixed block.
run_quiet() {
    # VERBOSE=1: show every line as it comes, no hidden tail block and no temp
    # file. Output streams straight through; we just propagate the exit code.
    if [[ "${VERBOSE:-}" == "1" ]]; then
        local rc=0
        "$@" || rc=$?
        (( rc != 0 )) && log_error "Command failed: $*"
        return "$rc"
    fi
    local out rc cols n
    out="$(mktemp)"
    cols=$(tput cols 2>/dev/null || echo 80)
    n=$RUN_QUIET_TAIL
    # Pipe through a reader that saves every line and live-prints the last n.
    # Inside `if` so `set -e` doesn't abort on a failing command; pipefail makes
    # the pipeline status reflect the command (the reader always exits 0).
    if "$@" 2>&1 | {
        local -a last=()
        local k pad
        # Reserve n lines so the block always exists (even with no output yet),
        # which keeps the cursor math identical on every redraw and on cleanup.
        [[ -t 2 ]] && printf '\n%.0s' $(seq "$n") >&2
        while IFS= read -r line; do
            printf '%s\n' "$line" >>"$out"
            if [[ -t 2 ]]; then
                last+=("$line")
                (( ${#last[@]} > n )) && last=("${last[@]:1}")
                # Move to the top of the block, then redraw exactly n lines.
                printf '\033[%dA' "$n" >&2
                pad=$(( n - ${#last[@]} ))
                for (( k=0; k<n; k++ )); do
                    if (( k < pad )); then
                        # \r col 0, \033[K clears the line; blank padding line.
                        printf '\r\033[K\n' >&2
                    else
                        printf '\r\033[K\t --- %.*s\n' "$cols" "${last[k-pad]}" >&2
                    fi
                done
            fi
        done
    }; then
        rc=0
    else
        rc=$?
    fi
    if [[ $rc -eq 0 ]]; then
        # Move up over the block and erase it to the end of the screen.
        [[ -t 2 ]] && printf '\033[%dA\033[J' "$n" >&2
        tail -n 1 "$out"
        rm -f "$out"
    else
        [[ -t 2 ]] && printf '\033[%dA\033[J' "$n" >&2
        log_error "Command failed: $*"
        cat "$out" >&2
        rm -f "$out"
        return "$rc"
    fi
}
