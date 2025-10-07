-- +migrate Down
ALTER TABLE bridge DROP COLUMN txn_sender;
DROP INDEX IF EXISTS idx_bridge_block_num_block_pos_desc;

-- +migrate Up
ALTER TABLE bridge ADD COLUMN txn_sender VARCHAR DEFAULT '';
CREATE INDEX idx_bridge_block_num_block_pos_desc ON bridge (block_num DESC, block_pos DESC);
