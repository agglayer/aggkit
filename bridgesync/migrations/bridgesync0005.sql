-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_deposit_count_desc;
DROP INDEX IF EXISTS idx_claim_block_num_block_pos_desc;
DROP INDEX IF EXISTS idx_legacy_token_migration_block_num_block_pos_desc;
ALTER TABLE bridge DROP COLUMN txn_sender;
DROP INDEX IF EXISTS idx_bridge_block_num_block_pos_desc;

-- +migrate Up
CREATE INDEX idx_bridge_deposit_count_desc ON bridge (deposit_count DESC);
CREATE INDEX idx_claim_block_num_block_pos_desc ON claim (block_num DESC, block_pos DESC);
CREATE INDEX idx_legacy_token_migration_block_num_block_pos_desc ON legacy_token_migration (block_num DESC, block_pos DESC);
ALTER TABLE bridge ADD COLUMN txn_sender VARCHAR DEFAULT '';
CREATE INDEX idx_bridge_block_num_block_pos_desc ON bridge (block_num DESC, block_pos DESC);
