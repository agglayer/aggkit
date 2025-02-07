-- +migrate Down
DROP TABLE IF EXISTS token_mapping;

-- +migrate Up
CREATE TABLE
	token_mapping (
		block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
		block_pos INTEGER NOT NULL,
		origin_network INTEGER NOT NULL,
		origin_token_address VARCHAR NOT NULL,
		wrapped_token_address VARCHAR NOT NULL,
		metadata BLOB,
		PRIMARY KEY (block_num, block_pos)
	);