-- +migrate Down
DROP TABLE IF EXISTS tracked_bridge;

-- +migrate Up
-- tracked_bridge persists SupervisedStore's snapshots (see bridgetracker.sqliteRegistry), one
-- row per supervised bridge, so the tracker survives restarts instead of re-resolving every
-- bridge (and re-issuing every bridge-service/agglayer call behind it) from scratch each time.
CREATE TABLE tracked_bridge (
    network_id          INTEGER NOT NULL,
    tx_hash             VARCHAR NOT NULL,

    -- schema_version is the version of the Go shapes (TrackingBridgeTx/BridgeStepPath)
    -- serialized into data. A row whose schema_version does not match what the running binary
    -- knows how to decode is treated as a miss (discarded, re-resolved from scratch) rather
    -- than risking misinterpreting stale/incompatible JSON: this is a cache, not a source of
    -- truth, so "discard and recompute" is always a safe fallback.
    schema_version      INTEGER NOT NULL,

    -- claim_status is a denormalized copy of TrackingData.ClaimStatus() (pending /
    -- readyToClaim / claimed / error), so it can be filtered/indexed directly without
    -- deserializing data first.
    claim_status        VARCHAR NOT NULL,

    -- origin_block_number/origin_block_hash are BridgeInfo.BlockNumber/BlockHash: the block,
    -- on the origin network, where the bridge's creating tx was found. Both stay zero/empty
    -- until Info resolves. Comparing origin_block_hash against the origin chain's current hash
    -- at that height is how a reload/refresh sweep can tell a row was captured on a block since
    -- reorged out, and must be discarded and re-resolved (mirrors reorgdetector's own
    -- tracked_block(num, hash) comparison, scoped to this one cached fact).
    origin_block_number BIGINT  NOT NULL DEFAULT 0,
    origin_block_hash   VARCHAR NOT NULL DEFAULT '',

    updated_at          BIGINT NOT NULL, -- unix seconds this row was last written
    last_access         BIGINT NOT NULL, -- unix seconds this row was last read (idle eviction anchor)
    terminal_since       BIGINT NOT NULL DEFAULT 0, -- unix seconds the snapshot first became terminal; 0 while it is not (retention anchor)

    -- data is the rest of the snapshot: TrackingBridgeTx (Error/Info/StartDate/Timeout) plus
    -- allSteps. Everything else (TrackingStatus, StepIndex, ...) stays derived in memory on
    -- read, exactly as it is today (see domain.TrackingData) -- nothing else needs storing.
    data                BLOB NOT NULL,

    PRIMARY KEY (network_id, tx_hash)
);

CREATE INDEX idx_tracked_bridge_last_access    ON tracked_bridge (last_access);
CREATE INDEX idx_tracked_bridge_terminal_since ON tracked_bridge (terminal_since);
