-- +migrate Down
DROP TABLE IF EXISTS activity_bridge;
DROP TABLE IF EXISTS activity_address;

-- +migrate Up
-- activity_address persists ActivityCache's per-from_address bookkeeping (see
-- bridgetracker.db's sqliteActivityStore): the scan cursor/error state for each network, plus
-- idle-eviction metadata. One row per address queried through GET /activity/from/{from_address}.
CREATE TABLE activity_address (
    from_address   VARCHAR NOT NULL,

    -- schema_version is the version of the Go shapes serialized into scan_state — see
    -- tracked_bridge.schema_version's doc for why a mismatch is treated as a cache miss
    schema_version INTEGER NOT NULL,

    updated_at     BIGINT NOT NULL, -- unix seconds this row was last written
    last_access    BIGINT NOT NULL, -- unix seconds this address was last queried (idle eviction anchor)

    -- scan_state is JSON: map[network_id] -> {last_error, last_error_at, last_success_at} — the
    -- last scan outcome per network, so an error survives across calls without touching bridges
    -- already cached from a previous successful scan of that same network (mirrors upsert's
    -- per-bridge failure handling, just for a whole network's scan). A per-network page-resume
    -- cursor was considered too (to resume a fetch that failed partway through a long run of new
    -- pages) but needs ActivityBridgeScanner.BridgesFrom to accept/report one first — deferred,
    -- not yet part of this shape.
    scan_state     BLOB NOT NULL,

    PRIMARY KEY (from_address)
);

CREATE INDEX idx_activity_address_last_access ON activity_address (last_access);

-- activity_bridge persists one row per bridge cached for some from_address (the entries an
-- ActivityCache keeps per address, normalized out of activity_address so claim status and
-- bridge age are indexed columns instead of requiring every row to be deserialized first).
CREATE TABLE activity_bridge (
    global_index         VARCHAR NOT NULL,
    from_address         VARCHAR NOT NULL REFERENCES activity_address(from_address) ON DELETE CASCADE,

    schema_version       INTEGER NOT NULL,

    origin_network_id    INTEGER NOT NULL, -- ScannedBridge.NetworkID: the bridge-service network that reported it
    destination_network  INTEGER NOT NULL, -- Bridge.DestinationNetwork
    tx_hash              VARCHAR NOT NULL, -- + origin_network_id: the TrackingID to join tracked_bridge by

    claim_status         VARCHAR NOT NULL, -- unclaimed | claimed | error   (on-chain isClaimed())
    tracker_claim_status VARCHAR NOT NULL, -- pending | readyToClaim | claimed | error

    block_timestamp      BIGINT NOT NULL, -- Bridge.BlockTimestamp: when the bridge itself was created on-chain
    created_at           BIGINT NOT NULL, -- unix nanoseconds this entry was first cached (exact round-trip of ActivityEntry.CreatedAt)
    updated_at           BIGINT NOT NULL, -- unix nanoseconds claim/tracking state was last (re)computed

    -- data is JSON: {Bridge, Claim, Errors} — the raw bridge-service payloads plus whatever
    -- check failed last, minus Tracking (always sourced live from tracked_bridge, never
    -- duplicated here — see sqliteActivityStore)
    data                 BLOB NOT NULL,

    PRIMARY KEY (global_index)
);

CREATE INDEX idx_activity_bridge_from_address_status
    ON activity_bridge (from_address, tracker_claim_status);
CREATE INDEX idx_activity_bridge_from_address_timestamp
    ON activity_bridge (from_address, block_timestamp);
