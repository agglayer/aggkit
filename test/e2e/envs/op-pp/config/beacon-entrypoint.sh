#!/bin/bash
set -e

echo "Beacon entrypoint: Starting Teku with checkpoint sync"

# Clear any existing Teku database to avoid genesis time mismatch issues
# The beacon database was created with a different genesis time on previous runs
if [ -d "/data/teku/beacon" ]; then
    echo "Clearing existing Teku database to avoid genesis time conflicts..."
    rm -rf /data/teku/beacon
fi

# Read checkpoint metadata
SNAPSHOT_TIME=$(jq -r '.snapshot_time' /checkpoint/checkpoint_metadata.json)
FINALIZED_EPOCH=$(jq -r '.finalized_epoch' /checkpoint/checkpoint_metadata.json)
EPOCH_START_SLOT=$(jq -r '.epoch_start_slot // "0"' /checkpoint/checkpoint_metadata.json)
FINALIZED_SLOT=$(jq -r '.finalized_slot // "0"' /checkpoint/checkpoint_metadata.json)
NOW=$(date +%s)
TIME_GAP=$((NOW - SNAPSHOT_TIME))

echo "Snapshot was taken at: $(date -d @$SNAPSHOT_TIME -u)"
echo "Current time: $(date -d @$NOW -u)"
echo "Time gap: $TIME_GAP seconds ($((TIME_GAP / 3600)) hours)"
echo "Checkpoint finalized epoch: $FINALIZED_EPOCH"
echo "Checkpoint epoch start slot: $EPOCH_START_SLOT"
echo "Checkpoint finalized slot (actual): $FINALIZED_SLOT"
echo ""

# EXPERIMENTAL: Skip genesis time patching to preserve checkpoint integrity
# Patching breaks Teku's checkpoint validation - "initial state is too recent" error
# Trade-off: Snapshots must be run within ~1 hour of creation
SKIP_GENESIS_PATCHING=${SKIP_GENESIS_PATCHING:-false}

if [ "$SKIP_GENESIS_PATCHING" = "true" ]; then
    echo "Skipping genesis time patching (SKIP_GENESIS_PATCHING=true)"
    echo "Using original checkpoint state without modifications"
elif [ "$EPOCH_START_SLOT" != "0" ] && [ "$EPOCH_START_SLOT" != "null" ] && [ -n "$EPOCH_START_SLOT" ]; then
    echo "Patching checkpoint genesis time (epoch-aligned)..."

    # Extract SECONDS_PER_SLOT from config files (try spec.yaml first, fall back to config.yaml)
    # Use exact match to avoid picking up SECONDS_PER_ETH1_BLOCK
    if [ -f /network-configs/spec.yaml ]; then
        SECONDS_PER_SLOT=$(grep "^SECONDS_PER_SLOT:" /network-configs/spec.yaml | awk '{print $2}')
    fi

    if [ -z "$SECONDS_PER_SLOT" ] && [ -f /network-configs/config.yaml ]; then
        SECONDS_PER_SLOT=$(grep "^SECONDS_PER_SLOT:" /network-configs/config.yaml | awk '{print $2}')
    fi

    if [ -n "$SECONDS_PER_SLOT" ]; then
        # Genesis time calculation with increased slack to bypass "too recent" errors
        # target_slot = finalized_slot + 3*SLOTS_PER_EPOCH
        # new_genesis_time = now - target_slot * SECONDS_PER_SLOT - 30
        # This puts current_slot about 4 epochs ahead of finalized_slot

        # Extract SLOTS_PER_EPOCH from config
        SLOTS_PER_EPOCH=32  # Default
        if [ -f /network-configs/spec.yaml ]; then
            SLOTS_PER_EPOCH_FROM_CONFIG=$(grep "^SLOTS_PER_EPOCH:" /network-configs/spec.yaml | awk '{print $2}')
            if [ -n "$SLOTS_PER_EPOCH_FROM_CONFIG" ]; then
                SLOTS_PER_EPOCH="$SLOTS_PER_EPOCH_FROM_CONFIG"
            fi
        fi

        TARGET_SLOT=$((FINALIZED_SLOT + 3 * SLOTS_PER_EPOCH))
        NEW_GENESIS_TIME=$((NOW - (TARGET_SLOT * SECONDS_PER_SLOT) - 30))

        CALCULATED_CURRENT_SLOT=$(( (NOW - NEW_GENESIS_TIME) / SECONDS_PER_SLOT ))

        echo "  Snapshot time: $SNAPSHOT_TIME"
        echo "  Current time: $NOW"
        echo "  Elapsed time since snapshot: $((NOW - SNAPSHOT_TIME)) seconds"
        echo "  Finalized slot: $FINALIZED_SLOT"
        echo "  SLOTS_PER_EPOCH: $SLOTS_PER_EPOCH"
        echo "  Target slot (finalized + 3*SLOTS_PER_EPOCH): $TARGET_SLOT"
        echo "  Seconds per slot: $SECONDS_PER_SLOT"
        echo "  Calculated new genesis_time: $NEW_GENESIS_TIME"
        echo "  Current slot after patching: $CALCULATED_CURRENT_SLOT"
        echo "  Slots ahead of finalized: $((CALCULATED_CURRENT_SLOT - FINALIZED_SLOT))"

        # Patcher is pre-compiled at build time for faster and more consistent runtime

        # Run patcher on checkpoint state
        echo "  Patching checkpoint_state.ssz..."
        java -cp '/opt/teku/lib/*:/patcher' GenesisTimePatcher \
            /network-configs/spec.yaml \
            /checkpoint/checkpoint_state.ssz \
            $NEW_GENESIS_TIME

        # Also patch genesis.ssz if it exists
        if [ -f /network-configs/genesis.ssz ]; then
            echo "  Patching genesis.ssz..."
            java -cp '/opt/teku/lib/*:/patcher' GenesisTimePatcher \
                /network-configs/spec.yaml \
                /network-configs/genesis.ssz \
                $NEW_GENESIS_TIME
        fi

        echo "  Genesis time patching complete"
    else
        echo "  WARNING: Could not determine SECONDS_PER_SLOT, skipping patching"
    fi
else
    echo "  WARNING: Could not determine checkpoint slot, skipping genesis time patching"
fi

echo ""
echo "Starting Teku with checkpoint state..."

# ---------------------------------------------------------------------------
# Watchdog: Teku v24.12.0 occasionally wedges silently (slot processing stops,
# e.g. after a block-production 500 at an epoch boundary) while /eth/v1/node/health
# keeps returning 200, freezing L1 block production for the rest of the run.
# Restarting the Teku process resumes the chain; restarting the whole container
# would re-clear the DB and re-patch genesis time (see above), desyncing the
# slot clock, so the restart must happen in-process here.
# ---------------------------------------------------------------------------

# The DB clear / genesis patching above rely on `set -e` to abort loudly on
# unexpected failures. The supervisor below runs its own long-lived loop and
# must not let a single failed command (e.g. a curl timeout) kill the script,
# so disable errexit from here on and handle errors explicitly.
set +e

# Start Teku beacon node with checkpoint state
# --ignore-weak-subjectivity-period-enabled allows loading checkpoints with time gaps
# --rest-api-host-allowlist=* allows validator to connect from other docker containers
# --validators-proposer-default-fee-recipient is new vs. the original image entrypoint:
# beacon warns that block production can fail when no non-zero default fee recipient is
# configured and the validator client's proposer-config "prepare" call is missed/late.
TEKU_ARGS=(
    --data-path=/data/teku
    --network=/network-configs/spec.yaml
    --initial-state=/checkpoint/checkpoint_state.ssz
    --ee-endpoint=http://geth:8551
    --ee-jwt-secret-file=/jwt/jwtsecret
    --rest-api-enabled=true
    --rest-api-interface=0.0.0.0
    --rest-api-port=4000
    --rest-api-host-allowlist=*
    --p2p-enabled=false
    --p2p-discovery-enabled=false
    --p2p-peer-lower-bound=0
    --ignore-weak-subjectivity-period-enabled=true
    --logging=INFO
    --validators-proposer-default-fee-recipient=0x0000000000000000000000000000000000000001
)

# Tunables (seconds), overridable via env for local debugging.
POLL="${POLL:-5}"                    # time between watchdog polls
STALL_LIMIT="${STALL_LIMIT:-40}"     # frozen/unreachable head time before restart
STARTUP_GRACE="${STARTUP_GRACE:-120}" # grace period after each (re)start before watchdog can fire

TEKU_PID=""
RESTART_COUNT=0

start_teku() {
    echo "[beacon-watchdog] starting teku (restart_count=${RESTART_COUNT})"
    teku "${TEKU_ARGS[@]}" &
    TEKU_PID=$!
    echo "[beacon-watchdog] teku started with pid ${TEKU_PID}"
}

terminate() {
    echo "[beacon-watchdog] received termination signal, stopping teku (pid ${TEKU_PID})"
    if [ -n "$TEKU_PID" ] && kill -0 "$TEKU_PID" 2>/dev/null; then
        kill -TERM "$TEKU_PID" 2>/dev/null
        wait "$TEKU_PID" 2>/dev/null
    fi
    exit 0
}
trap terminate TERM INT

start_teku

last_slot=""
stall_seconds=0
grace_remaining=$STARTUP_GRACE

while true; do
    sleep "$POLL"

    # If teku died on its own, restart immediately - no re-patch, no DB clear.
    if [ -n "$TEKU_PID" ] && ! kill -0 "$TEKU_PID" 2>/dev/null; then
        echo "[beacon-watchdog] teku process (pid ${TEKU_PID}) exited unexpectedly - restarting"
        RESTART_COUNT=$((RESTART_COUNT + 1))
        start_teku
        last_slot=""
        stall_seconds=0
        grace_remaining=$STARTUP_GRACE
        continue
    fi

    slot=$(curl -sf --max-time 4 http://localhost:4000/eth/v1/beacon/headers/head 2>/dev/null | jq -r '.data.header.message.slot' 2>/dev/null)

    if [ -n "$slot" ] && [ "$slot" != "null" ] && [ "$slot" -eq "$slot" ] 2>/dev/null; then
        if [ "$slot" != "$last_slot" ]; then
            # Progress: reset the stall timer.
            last_slot="$slot"
            stall_seconds=0
        else
            stall_seconds=$((stall_seconds + POLL))
        fi
    else
        # curl/jq failed to produce a numeric slot.
        stall_seconds=$((stall_seconds + POLL))
    fi

    if [ "$grace_remaining" -gt 0 ]; then
        grace_remaining=$((grace_remaining - POLL))
        if [ "$grace_remaining" -lt 0 ]; then
            grace_remaining=0
        fi
        # Never trigger the watchdog during the startup grace period.
        continue
    fi

    if [ "$stall_seconds" -ge "$STALL_LIMIT" ]; then
        echo "[beacon-watchdog] head slot frozen at ${last_slot:-unknown} for ${stall_seconds}s - restarting teku (restart_count=$((RESTART_COUNT + 1)))"

        if [ -n "$TEKU_PID" ] && kill -0 "$TEKU_PID" 2>/dev/null; then
            kill -TERM "$TEKU_PID" 2>/dev/null
            for _ in $(seq 1 10); do
                kill -0 "$TEKU_PID" 2>/dev/null || break
                sleep 1
            done
            if kill -0 "$TEKU_PID" 2>/dev/null; then
                echo "[beacon-watchdog] teku (pid ${TEKU_PID}) did not exit after TERM - sending KILL"
                kill -KILL "$TEKU_PID" 2>/dev/null
            fi
            wait "$TEKU_PID" 2>/dev/null
        fi

        RESTART_COUNT=$((RESTART_COUNT + 1))
        echo "[beacon-watchdog] teku restarted (restart_count=${RESTART_COUNT})"
        start_teku
        last_slot=""
        stall_seconds=0
        grace_remaining=$STARTUP_GRACE
    fi
done
