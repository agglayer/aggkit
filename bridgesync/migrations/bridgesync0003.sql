-- +migrate Down
ALTER TABLE bridge DROP COLUMN tx_hash;
ALTER TABLE claim DROP COLUMN tx_hash;
ALTER TABLE bridge DROP COLUMN block_timestamp;
ALTER TABLE claim DROP COLUMN block_timestamp;

-- +migrate Up
ALTER TABLE bridge ADD COLUMN tx_hash VARCHAR;
ALTER TABLE claim ADD COLUMN tx_hash VARCHAR;
ALTER TABLE bridge ADD COLUMN block_timestamp INTEGER;
ALTER TABLE claim ADD COLUMN block_timestamp INTEGER;