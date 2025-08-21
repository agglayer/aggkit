-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_block_num;
DROP INDEX IF EXISTS idx_bridge_origin_network;
DROP INDEX IF EXISTS idx_bridge_destination_network;
DROP INDEX IF EXISTS idx_bridge_tx_hash;
DROP INDEX IF EXISTS idx_bridge_block_timestamp;
DROP INDEX IF EXISTS idx_claim_block_num;
DROP INDEX IF EXISTS idx_claim_origin_network;
DROP INDEX IF EXISTS idx_claim_destination_network;
DROP INDEX IF EXISTS idx_claim_global_index;
DROP INDEX IF EXISTS idx_claim_tx_hash;
DROP INDEX IF EXISTS idx_claim_block_timestamp;
DROP INDEX IF EXISTS idx_token_mapping_origin_network;
DROP INDEX IF EXISTS idx_token_mapping_origin_token_address;
DROP INDEX IF EXISTS idx_token_mapping_wrapped_token_address;
DROP INDEX IF EXISTS idx_token_mapping_tx_hash;
DROP INDEX IF EXISTS idx_token_mapping_block_timestamp;
DROP INDEX IF EXISTS idx_legacy_token_migration_legacy_token_address;
DROP INDEX IF EXISTS idx_legacy_token_migration_updated_token_address;
DROP INDEX IF EXISTS idx_legacy_token_migration_sender;
DROP INDEX IF EXISTS idx_legacy_token_migration_tx_hash;

-- +migrate Up
-- Bridge table indexes
CREATE INDEX idx_bridge_block_num ON bridge(block_num);
CREATE INDEX idx_bridge_origin_network ON bridge(origin_network);
CREATE INDEX idx_bridge_destination_network ON bridge(destination_network);
CREATE INDEX idx_bridge_tx_hash ON bridge(tx_hash);
CREATE INDEX idx_bridge_block_timestamp ON bridge(block_timestamp);

-- Claim table indexes
CREATE INDEX idx_claim_block_num ON claim(block_num);
CREATE INDEX idx_claim_origin_network ON claim(origin_network);
CREATE INDEX idx_claim_destination_network ON claim(destination_network);
CREATE INDEX idx_claim_global_index ON claim(global_index);
CREATE INDEX idx_claim_tx_hash ON claim(tx_hash);
CREATE INDEX idx_claim_block_timestamp ON claim(block_timestamp);

-- Token mapping table indexes
CREATE INDEX idx_token_mapping_origin_network ON token_mapping(origin_network);
CREATE INDEX idx_token_mapping_origin_token_address ON token_mapping(origin_token_address);
CREATE INDEX idx_token_mapping_wrapped_token_address ON token_mapping(wrapped_token_address);
CREATE INDEX idx_token_mapping_tx_hash ON token_mapping(tx_hash);
CREATE INDEX idx_token_mapping_block_timestamp ON token_mapping(block_timestamp);

-- Legacy token migration table indexes
CREATE INDEX idx_legacy_token_migration_legacy_token_address ON legacy_token_migration(legacy_token_address);
CREATE INDEX idx_legacy_token_migration_updated_token_address ON legacy_token_migration(updated_token_address);
CREATE INDEX idx_legacy_token_migration_sender ON legacy_token_migration(sender);
CREATE INDEX idx_legacy_token_migration_tx_hash ON legacy_token_migration(tx_hash);
