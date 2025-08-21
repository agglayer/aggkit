-- +migrate Down
DROP INDEX IF EXISTS idx_l1info_leaf_position;
DROP INDEX IF EXISTS idx_l1info_leaf_global_exit_root;
DROP INDEX IF EXISTS idx_l1info_leaf_hash;
DROP INDEX IF EXISTS idx_l1info_leaf_timestamp;
DROP INDEX IF EXISTS idx_l1info_leaf_block_num_pos;
DROP INDEX IF EXISTS idx_verify_batches_rollup_id;
DROP INDEX IF EXISTS idx_verify_batches_batch_num;
DROP INDEX IF EXISTS idx_verify_batches_state_root;
DROP INDEX IF EXISTS idx_verify_batches_exit_root;
DROP INDEX IF EXISTS idx_verify_batches_aggregator;

-- +migrate Up
-- L1Info leaf table indexes
CREATE INDEX idx_l1info_leaf_position ON l1info_leaf(position);
CREATE INDEX idx_l1info_leaf_global_exit_root ON l1info_leaf(global_exit_root);
CREATE INDEX idx_l1info_leaf_hash ON l1info_leaf(hash);
CREATE INDEX idx_l1info_leaf_timestamp ON l1info_leaf(timestamp);

-- Composite index for block queries (high priority)
CREATE INDEX idx_l1info_leaf_block_num_pos ON l1info_leaf(block_num DESC, block_pos DESC);

-- Verify batches table indexes
CREATE INDEX idx_verify_batches_rollup_id ON verify_batches(rollup_id);
CREATE INDEX idx_verify_batches_batch_num ON verify_batches(batch_num);
CREATE INDEX idx_verify_batches_state_root ON verify_batches(state_root);
CREATE INDEX idx_verify_batches_exit_root ON verify_batches(exit_root);
CREATE INDEX idx_verify_batches_aggregator ON verify_batches(aggregator);
