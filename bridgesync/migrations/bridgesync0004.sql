-- +migrate Up
CREATE TABLE updated_claimed_global_index_hash_chain_event (
    block_num INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
    block_pos INTEGER NOT NULL,
    block_timestamp INTEGER NOT NULL,
    tx_hash VARCHAR NOT NULL,
    contract_address VARCHAR NOT NULL,
    network_id INTEGER NOT NULL,
    claimed_global_index TEXT NOT NULL,
    new_global_index_hash_chain TEXT NOT NULL,
    PRIMARY KEY (block_num, block_pos)
);

CREATE INDEX idx_ucgihce_block_num ON updated_claimed_global_index_hash_chain_event(block_num);
CREATE INDEX idx_ucgihce_network_id ON updated_claimed_global_index_hash_chain_event(network_id);
CREATE INDEX idx_ucgihce_claimed_global_index ON updated_claimed_global_index_hash_chain_event(claimed_global_index);

-- +migrate Down
DROP TABLE IF EXISTS updated_claimed_global_index_hash_chain_event;
