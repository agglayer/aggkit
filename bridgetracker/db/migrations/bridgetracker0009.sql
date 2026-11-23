-- +migrate Down
-- Back to one row per (network_id, tx_hash): only the tx-only rows (log_index = -1) fit that key,
-- the per-event ones are dropped — this is a cache, they are simply re-resolved on demand.
CREATE TABLE tracked_bridge_old (
    network_id          INTEGER NOT NULL,
    tx_hash             VARCHAR NOT NULL,
    schema_version      INTEGER NOT NULL,
    claim_status        VARCHAR NOT NULL,
    origin_block_number BIGINT  NOT NULL DEFAULT 0,
    origin_block_hash   VARCHAR NOT NULL DEFAULT '',
    updated_at          BIGINT NOT NULL,
    last_access         BIGINT NOT NULL,
    terminal_since      BIGINT NOT NULL DEFAULT 0,
    data                BLOB NOT NULL,
    PRIMARY KEY (network_id, tx_hash)
);
INSERT INTO tracked_bridge_old
    (network_id, tx_hash, schema_version, claim_status, origin_block_number, origin_block_hash,
     updated_at, last_access, terminal_since, data)
SELECT network_id, tx_hash, schema_version, claim_status, origin_block_number, origin_block_hash,
       updated_at, last_access, terminal_since, data
  FROM tracked_bridge WHERE log_index = -1;
DROP TABLE tracked_bridge;
ALTER TABLE tracked_bridge_old RENAME TO tracked_bridge;
CREATE INDEX idx_tracked_bridge_last_access    ON tracked_bridge (last_access);
CREATE INDEX idx_tracked_bridge_terminal_since ON tracked_bridge (terminal_since);

-- +migrate Up
-- log_index: which of the tx's BridgeEvents a row tracks, by the index of its log (see
-- domain.LogSelector), so the bridges of a tx that emitted several each get their own row instead
-- of sharing the first one's. -1 is the tx-only identity (no log_index
-- requested), only resolvable while the tx emitted a single BridgeEvent: every row written before
-- this migration becomes one, and keeps being served as-is to the same tx-only requests.
-- It joins the primary key, which SQLite cannot alter in place: the table is rebuilt and its rows
-- copied over (same columns as bridgetracker0001.sql, see its comments).
CREATE TABLE tracked_bridge_new (
    network_id          INTEGER NOT NULL,
    tx_hash             VARCHAR NOT NULL,
    log_index           INTEGER NOT NULL DEFAULT -1,
    schema_version      INTEGER NOT NULL,
    claim_status        VARCHAR NOT NULL,
    origin_block_number BIGINT  NOT NULL DEFAULT 0,
    origin_block_hash   VARCHAR NOT NULL DEFAULT '',
    updated_at          BIGINT NOT NULL,
    last_access         BIGINT NOT NULL,
    terminal_since      BIGINT NOT NULL DEFAULT 0,
    data                BLOB NOT NULL,
    PRIMARY KEY (network_id, tx_hash, log_index)
);
INSERT INTO tracked_bridge_new
    (network_id, tx_hash, log_index, schema_version, claim_status, origin_block_number,
     origin_block_hash, updated_at, last_access, terminal_since, data)
SELECT network_id, tx_hash, -1, schema_version, claim_status, origin_block_number,
       origin_block_hash, updated_at, last_access, terminal_since, data
  FROM tracked_bridge;
DROP TABLE tracked_bridge;
ALTER TABLE tracked_bridge_new RENAME TO tracked_bridge;
CREATE INDEX idx_tracked_bridge_last_access    ON tracked_bridge (last_access);
CREATE INDEX idx_tracked_bridge_terminal_since ON tracked_bridge (terminal_since);
