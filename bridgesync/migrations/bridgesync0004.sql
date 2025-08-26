-- +migrate Down
DROP TABLE IF EXISTS unset_global_index;

-- +migrate Up
CREATE TABLE unset_global_index (
    block_num           INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
    block_pos           INTEGER NOT NULL,
    block_timestamp     INTEGER NOT NULL,
    tx_hash             VARCHAR NOT NULL,
    global_index        TEXT NOT NULL,
    claim_block_num     INTEGER NOT NULL,
    claim_block_pos     INTEGER NOT NULL,
    PRIMARY KEY (block_num, block_pos),
    FOREIGN KEY (claim_block_num, claim_block_pos) REFERENCES claim(block_num, block_pos) ON DELETE CASCADE
);

-- Create index on global_index for efficient lookups
CREATE INDEX idx_unset_global_index_global_index ON unset_global_index(global_index);

-- Create index on claim reference for efficient joins
CREATE INDEX idx_unset_global_index_claim_ref ON unset_global_index(claim_block_num, claim_block_pos);
