-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_block_num_block_pos_asc;

-- +migrate Up
CREATE INDEX idx_bridge_block_num_block_pos_asc ON bridge (block_num ASC, block_pos ASC);
