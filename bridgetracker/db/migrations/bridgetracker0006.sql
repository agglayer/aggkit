-- +migrate Down
-- Back to PRIMARY KEY (global_index). That schema cannot hold two rows sharing a global_index
-- (two addresses with the same bridge, or the legacy pre-Etrog collision within one address), so
-- only rows whose global_index is unique across the table are copied back; the rest are dropped
-- explicitly. activity_bridge is a cache: the older binary rescans whatever is missing on the next
-- refresh of each address.
CREATE TABLE activity_bridge_old (
    global_index         VARCHAR NOT NULL,
    from_address         VARCHAR NOT NULL REFERENCES activity_address(from_address) ON DELETE CASCADE,
    schema_version       INTEGER NOT NULL,
    origin_network_id    INTEGER NOT NULL,
    destination_network  INTEGER NOT NULL,
    tx_hash              VARCHAR NOT NULL,
    claim_status         VARCHAR NOT NULL,
    tracker_claim_status VARCHAR NOT NULL,
    block_timestamp      BIGINT NOT NULL,
    created_at           BIGINT NOT NULL,
    updated_at           BIGINT NOT NULL,
    data                 BLOB NOT NULL,
    revision_hash        BLOB,
    PRIMARY KEY (global_index)
);
INSERT INTO activity_bridge_old
    (global_index, from_address, schema_version, origin_network_id, destination_network, tx_hash,
     claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data, revision_hash)
SELECT global_index, from_address, schema_version, origin_network_id, destination_network, tx_hash,
       claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data, revision_hash
  FROM activity_bridge
 WHERE global_index IN (SELECT global_index FROM activity_bridge GROUP BY global_index HAVING COUNT(*) = 1);
DROP TABLE activity_bridge;
ALTER TABLE activity_bridge_old RENAME TO activity_bridge;
CREATE INDEX idx_activity_bridge_from_address_status
    ON activity_bridge (from_address, tracker_claim_status);
CREATE INDEX idx_activity_bridge_from_address_timestamp
    ON activity_bridge (from_address, block_timestamp);

-- +migrate Up
-- activity_bridge is keyed per owner and per bridge: (from_address, origin_network_id,
-- deposit_count) instead of PRIMARY KEY (global_index).
--   - from_address: every row belongs to one watched address. With the global index alone as the
--     key, refreshing address B overwrote (and took over) address A's row for the same global
--     index, and A's settled row hid B's bridge.
--   - origin_network_id + deposit_count: the bridge's leaf in the exit tree of the network that
--     emitted it (domain.BridgeKey). global_index is not unique: under the legacy pre-Etrog
--     encoding an L1 bridge to network 1 encodes to the bare deposit count, the same value as a
--     bridge of that deposit count emitted on network 1. It stays a plain column.
-- SQLite cannot alter a primary key, so the table is rebuilt with every column it has today,
-- revision_hash (bridgetracker0005) included, plus deposit_count, backfilled from the row's own
-- JSON (activityBridgeData.Bridge.deposit_count). The old key allowed one row per global_index,
-- so the copy cannot conflict. A row whose data is not valid JSON or has no integer deposit_count
-- cannot be keyed and is dropped: the table is a cache, and the bridge is found again by the next
-- refresh of its address.
CREATE TABLE activity_bridge_new (
    global_index         VARCHAR NOT NULL, -- Bridge.GlobalIndex: served as is, not an identity
    from_address         VARCHAR NOT NULL REFERENCES activity_address(from_address) ON DELETE CASCADE,
    schema_version       INTEGER NOT NULL,
    origin_network_id    INTEGER NOT NULL, -- ScannedBridge.NetworkID: the network that emitted (and reported) it
    deposit_count        INTEGER NOT NULL, -- Bridge.DepositCount on origin_network_id
    destination_network  INTEGER NOT NULL,
    tx_hash              VARCHAR NOT NULL,
    claim_status         VARCHAR NOT NULL,
    tracker_claim_status VARCHAR NOT NULL,
    block_timestamp      BIGINT NOT NULL,
    created_at           BIGINT NOT NULL,
    updated_at           BIGINT NOT NULL,
    data                 BLOB NOT NULL,
    revision_hash        BLOB,
    PRIMARY KEY (from_address, origin_network_id, deposit_count)
);
INSERT INTO activity_bridge_new
    (global_index, from_address, schema_version, origin_network_id, deposit_count, destination_network,
     tx_hash, claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data, revision_hash)
SELECT global_index, from_address, schema_version, origin_network_id,
       json_extract(CAST(data AS TEXT), '$.Bridge.deposit_count'), destination_network,
       tx_hash, claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data, revision_hash
  FROM activity_bridge
 WHERE CASE WHEN json_valid(CAST(data AS TEXT))
            THEN json_type(CAST(data AS TEXT), '$.Bridge.deposit_count') = 'integer'
            ELSE 0 END;
DROP TABLE activity_bridge;
ALTER TABLE activity_bridge_new RENAME TO activity_bridge;
CREATE INDEX idx_activity_bridge_from_address_status
    ON activity_bridge (from_address, tracker_claim_status);
CREATE INDEX idx_activity_bridge_from_address_timestamp
    ON activity_bridge (from_address, block_timestamp);
