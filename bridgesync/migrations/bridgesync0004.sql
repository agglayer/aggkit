-- +migrate Down
DROP TABLE IF EXISTS updated_claimed_global_index_hash_chain;

-- +migrate Up
CREATE TABLE updated_claimed_global_index_hash_chain (
    block_num INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
    block_pos INTEGER NOT NULL,
    block_timestamp INTEGER NOT NULL,
    tx_hash VARCHAR NOT NULL,
    claimed_global_index TEXT NOT NULL,
    new_global_index_hash_chain TEXT NOT NULL,
    PRIMARY KEY (block_num, block_pos)
);

CREATE INDEX idx_ucgihce_block_num ON updated_claimed_global_index_hash_chain(block_num);
CREATE INDEX idx_ucgihce_claimed_global_index ON updated_claimed_global_index_hash_chain(claimed_global_index);
