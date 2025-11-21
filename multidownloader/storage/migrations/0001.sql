-- +migrate Down
DROP TABLE IF EXISTS logs;
DROP TABLE IF EXISTS blocks;
DROP TABLE IF EXISTS sync_status;
-- +migrate Up
CREATE TABLE logs (
    address TEXT NOT NULL,                -- 
    topics TEXT NOT NULL,                 -- list of hashes in JSON
    data BLOB,                            -- 
    block_number BIGINT NOT NULL REFERENCES blocks(block_number),
    tx_hash TEXT NOT NULL,
    tx_index INTEGER NOT NULL,
    log_index INTEGER NOT NULL,      -- “index” is a reserved keyword
    PRIMARY KEY (address, block_number, log_index)
);

CREATE INDEX idx_logs_block_number ON logs(block_number);

CREATE TABLE blocks (
    block_number BIGINT NOT NULL,
    block_hash TEXT NOT NULL,             
    block_timestamp INTEGER NOT NULL,
    block_parent_hash TEXT, 
    is_final INTEGER NOT NULL,
    PRIMARY KEY (block_number)
);

CREATE TABLE sync_status (
    contract_address TEXT NOT NULL,              -- Contract address       
    target_from_block BIGINT NOT NULL,  -- Desired from block
    target_to_block TEXT NOT NULL,    -- Desired to block
    synced_from_block BIGINT NOT NULL,  -- Current synced from block
    synced_to_block BIGINT NOT NULL,    -- Current synced to block
    syncers_id TEXT NOT NULL,          -- Syncer identifier
    PRIMARY KEY (contract_address)
);