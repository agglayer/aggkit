-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_deposit_count_desc;
DROP INDEX IF EXISTS idx_claim_block_num_block_pos_desc;
DROP INDEX IF EXISTS idx_legacy_token_migration_block_num_block_pos_desc;
DROP INDEX IF EXISTS idx_token_mapping_block_num_desc;
DROP INDEX IF EXISTS idx_claim_global_index_block_num_block_pos;
DROP INDEX IF EXISTS idx_block_num_desc;

-- +migrate Up
-- Index for bridge table ORDER BY deposit_count DESC (used in GetBridgesPaged)
CREATE INDEX idx_bridge_deposit_count_desc ON bridge (deposit_count DESC);

-- Index for claim table ORDER BY block_num DESC, block_pos DESC (used in GetClaimsPaged)
CREATE INDEX idx_claim_block_num_block_pos_desc ON claim (block_num DESC, block_pos DESC);

-- Index for legacy_token_migration table ORDER BY block_num DESC, block_pos DESC (used in GetLegacyTokenMigrations)
CREATE INDEX idx_legacy_token_migration_block_num_block_pos_desc ON legacy_token_migration (block_num DESC, block_pos DESC);

-- Index for token_mapping table ORDER BY block_num DESC (used in fetchTokenMappings)
CREATE INDEX idx_token_mapping_block_num_desc ON token_mapping (block_num DESC);

-- Index for block table ORDER BY num DESC (used in getLastProcessedBlockWithTx)
CREATE INDEX idx_block_num_desc ON block (num DESC);
