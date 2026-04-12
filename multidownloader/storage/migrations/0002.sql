-- +migrate Down
DROP TABLE IF EXISTS logs_reorged;
DROP TABLE IF EXISTS blocks_reorged;
DROP TABLE IF EXISTS reorgs;
-- +migrate Up

CREATE TABLE reorgs (
    reorg_id BIGINT PRIMARY KEY,
    detected_at_block BIGINT NOT NULL,
    reorged_from_block BIGINT NOT NULL,
    reorged_to_block BIGINT NOT NULL,
    detected_timestamp INTEGER NOT NULL,
    network_latest_block INTEGER NOT NULL,  -- which was the latest block in the detection moment
    network_finalized_block INTEGER NOT NULL, -- which was the finalized block in the detection moment
    network_finalized_block_name TEXT NOT NULL, -- name of the finalized block (e.g., "finalized", "safe", etc.)
    description TEXT -- extra information, can be null
);

CREATE TABLE blocks_reorged (
    reorg_id BIGINT NOT NULL REFERENCES reorgs(reorg_id),
    block_number BIGINT NOT NULL,
    block_hash TEXT NOT NULL,
    block_timestamp INTEGER NOT NULL,
    block_parent_hash TEXT NOT NULL,
    PRIMARY KEY (reorg_id, block_number)
);

CREATE TABLE logs_reorged (
    reorg_id BIGINT NOT NULL,
    block_number BIGINT NOT NULL,
    address TEXT NOT NULL,                --
    topics TEXT NOT NULL,                 -- list of hashes in JSON
    data BLOB,                            --
    tx_hash TEXT NOT NULL,
    tx_index INTEGER NOT NULL,
    log_index INTEGER NOT NULL,      -- "index" is a reserved keyword
    PRIMARY KEY (address, reorg_id, block_number, log_index),
    FOREIGN KEY (reorg_id, block_number) REFERENCES blocks_reorged(reorg_id, block_number)
);

CREATE INDEX idx_logs_reorged_block_number ON logs_reorged(block_number);