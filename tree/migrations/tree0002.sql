-- +migrate Down
DROP INDEX IF EXISTS /*dbprefix*/idx_root_block_num_position;

-- +migrate Up
-- Create composite index for queries filtering on both block_num and block_position
CREATE INDEX /*dbprefix*/idx_root_block_num_position ON /*dbprefix*/root(block_num, block_position);
