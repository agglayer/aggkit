-- +migrate Down
DROP TABLE IF EXISTS logs_reorged;
-- +migrate Up

CREATE TABLE logs_reorged (
    chain_id INTEGER NOT NULL,
    block_number BIGINT NOT NULL,
    address TEXT NOT NULL,                -- 
    topics TEXT NOT NULL,                 -- list of hashes in JSON
    data BLOB,                            -- 
    tx_hash TEXT NOT NULL,
    tx_index INTEGER NOT NULL,
    log_index INTEGER NOT NULL,      -- “index” is a reserved keyword
    PRIMARY KEY (address, chain_id,block_number, log_index),
    FOREIGN KEY (chain_id, block_number) REFERENCES block_reorged(chain_id, block_number)
);

CREATE INDEX idx_logs_reorged_block_number ON logs_reorged(block_number);

CREATE TABLE block_reorged (
    chain_id INTEGER NOT NULL,
    block_number BIGINT NOT NULL,
     block_hash TEXT NOT NULL, 
     block_timestamp INTEGER NOT NULL,
    block_parent_hash TEXT, 
    PRIMARY KEY (chain_id, block_number)
);

CREATE TABLE reorgs (
    chain_id INTEGER NOT NULL,
    detected_at_block BIGINT NOT NULL,
    reorged_from_block BIGINT NOT NULL,
    reorged_to_block BIGINT NOT NULL,
    detected_timestamp INTEGER NOT NULL,    
    PRIMARY KEY (chain_id, detected_at_block)
);