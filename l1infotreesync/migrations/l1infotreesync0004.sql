-- +migrate Down
DROP INDEX IF EXISTS idx_l1info_leaf_position;
DROP INDEX IF EXISTS idx_l1info_leaf_block_num_composite;
DROP INDEX IF EXISTS idx_verify_batches_rollup_id;
DROP INDEX IF EXISTS idx_verify_batches_block_num_block_pos;

-- +migrate Up
CREATE INDEX idx_l1info_leaf_position ON l1info_leaf(position);
CREATE INDEX idx_l1info_leaf_block_num_composite ON l1info_leaf(block_num, block_pos);
CREATE INDEX idx_verify_batches_rollup_id ON verify_batches(rollup_id);
CREATE INDEX idx_verify_batches_block_num_block_pos ON verify_batches(block_num, block_pos);
