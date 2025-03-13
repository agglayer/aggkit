-- +migrate Down
DROP TABLE IF EXISTS token_mapping;
ALTER TABLE bridge DROP COLUMN tx_hash;
ALTER TABLE claim DROP COLUMN tx_hash;
ALTER TABLE bridge DROP COLUMN block_timestamp;
ALTER TABLE claim DROP COLUMN block_timestamp;
ALTER TABLE bridge DROP COLUMN from_address;
ALTER TABLE claim DROP COLUMN from_address;
ALTER TABLE bridge DROP COLUMN calldata;

-- +migrate Up
CREATE TABLE
	token_mapping (
		block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
		block_pos INTEGER NOT NULL,
		block_timestamp INTEGER NOT NULL,
		tx_hash VARCHAR NOT NULL,
		origin_network INTEGER NOT NULL,
		origin_token_address VARCHAR NOT NULL,
		wrapped_token_address VARCHAR NOT NULL,
		metadata BLOB,
		calldata BLOB,
		PRIMARY KEY (block_num, block_pos)
	);
ALTER TABLE bridge ADD COLUMN tx_hash VARCHAR;
ALTER TABLE claim ADD COLUMN tx_hash VARCHAR;
ALTER TABLE bridge ADD COLUMN block_timestamp INTEGER;
ALTER TABLE claim ADD COLUMN block_timestamp INTEGER;
ALTER TABLE bridge ADD COLUMN from_address VARCHAR;
ALTER TABLE claim ADD COLUMN from_address VARCHAR;
ALTER TABLE bridge ADD COLUMN calldata BLOB;
