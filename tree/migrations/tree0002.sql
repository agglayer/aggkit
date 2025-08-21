-- +migrate Down
DROP INDEX IF EXISTS idx_/*dbprefix*/root_hash;
DROP INDEX IF EXISTS idx_/*dbprefix*/root_block_num_position;
DROP INDEX IF EXISTS idx_/*dbprefix*/root_position;
DROP INDEX IF EXISTS idx_/*dbprefix*/rht_hash;

-- +migrate Up
-- Root table indexes (for merkle tree operations)
CREATE INDEX idx_/*dbprefix*/root_hash ON /*dbprefix*/root(hash);
CREATE INDEX idx_/*dbprefix*/root_block_num_position ON /*dbprefix*/root(block_num DESC, block_position DESC);
CREATE INDEX idx_/*dbprefix*/root_position ON /*dbprefix*/root(position);

-- RHT (Right Hash Tree) table indexes
CREATE INDEX idx_/*dbprefix*/rht_hash ON /*dbprefix*/rht(hash);
