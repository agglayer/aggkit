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
        # Genesis time calculation with a small lead time so the beacon's head is only a few
        # epochs ahead of the finalized checkpoint when the validator connects.
        #
        # IMPORTANT: Use a small lead time (2 epochs) so the beacon catches up quickly and the
        # validator connects while the head is only ~2 epochs ahead of the finalized checkpoint.
        # This ensures finalization advances in small steps (1 epoch at a time) once the
        # validator starts producing attestations.
        #
        # Using a large lead time (e.g., 20 epochs) causes the beacon to run many unfinalized
        # epochs before the validator connects. When finalization finally occurs, it jumps across
        # many epochs at once (e.g., epoch 39 -> epoch 62), which triggers a Teku 26.2.0 bug
        # in StoreTransaction.retrieveLatestFinalized: the newly-finalized checkpoint block has
        # not yet been written to the finalized-block store, so orElseThrow() fires with
        # "Missing latest finalized block and state", permanently breaking the slot timer.
        #
        # With a 2-epoch lead time the validator connects quickly (< 30s) while the store still
        # has the original finalized block (epoch 39). The first finalization is a 1-2 epoch
        # step which the store handles correctly.
        #
        # target_slot = finalized_slot + 2*SLOTS_PER_EPOCH puts current_slot only ~3 epochs
        # ahead of the finalized checkpoint. The beacon catches up in < 1 second.

        # Extract SLOTS_PER_EPOCH from config
        SLOTS_PER_EPOCH=32  # Default
        if [ -f /network-configs/spec.yaml ]; then
            SLOTS_PER_EPOCH_FROM_CONFIG=$(grep "^SLOTS_PER_EPOCH:" /network-configs/spec.yaml | awk '{print $2}')
            if [ -n "$SLOTS_PER_EPOCH_FROM_CONFIG" ]; then
                SLOTS_PER_EPOCH="$SLOTS_PER_EPOCH_FROM_CONFIG"
            fi
        fi

        TARGET_SLOT=$((FINALIZED_SLOT + 2 * SLOTS_PER_EPOCH))
        NEW_GENESIS_TIME=$((NOW - (TARGET_SLOT * SECONDS_PER_SLOT) - 30))

        CALCULATED_CURRENT_SLOT=$(( (NOW - NEW_GENESIS_TIME) / SECONDS_PER_SLOT ))

        echo "  Snapshot time: $SNAPSHOT_TIME"
        echo "  Current time: $NOW"
        echo "  Elapsed time since snapshot: $((NOW - SNAPSHOT_TIME)) seconds"
        echo "  Finalized slot: $FINALIZED_SLOT"
        echo "  SLOTS_PER_EPOCH: $SLOTS_PER_EPOCH"
        echo "  Target slot (finalized + 20*SLOTS_PER_EPOCH): $TARGET_SLOT"
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

        # Also patch genesis.ssz if it exists.
        # NOTE: genesis.ssz may be a pre-fork-era state that cannot be deserialized with the
        # current fork spec (e.g. a pre-Fulu genesis.ssz when FULU_FORK_EPOCH is set).
        # Patching genesis.ssz is non-critical: Teku uses --initial-state (checkpoint_state.ssz),
        # not genesis.ssz. Failure here is logged but does not abort the startup.
        if [ -f /network-configs/genesis.ssz ]; then
            echo "  Patching genesis.ssz..."
            java -cp '/opt/teku/lib/*:/patcher' GenesisTimePatcher \
                /network-configs/spec.yaml \
                /network-configs/genesis.ssz \
                $NEW_GENESIS_TIME || echo "  WARNING: genesis.ssz patching failed (non-critical, continuing)"
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

# Start Teku beacon node with checkpoint state
# --ignore-weak-subjectivity-period-enabled allows loading checkpoints with time gaps
# --rest-api-host-allowlist=* allows validator to connect from other docker containers
exec teku \
    --data-path=/data/teku \
    --network=/network-configs/spec.yaml \
    --initial-state=/checkpoint/checkpoint_state.ssz \
    --ee-endpoint=http://geth:8551 \
    --ee-jwt-secret-file=/jwt/jwtsecret \
    --rest-api-enabled=true \
    --rest-api-interface=0.0.0.0 \
    --rest-api-port=4000 \
    --rest-api-host-allowlist=* \
    --p2p-enabled=false \
    --p2p-discovery-enabled=false \
    --p2p-peer-lower-bound=0 \
    --p2p-subscribe-all-custody-subnets-enabled=false \
    --ignore-weak-subjectivity-period-enabled=true \
    --data-storage-mode=ARCHIVE \
    --data-storage-archive-frequency=1 \
    --logging=INFO
