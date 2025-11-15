-- +migrate Down
DROP TABLE IF EXISTS set_claim;
DROP TABLE IF EXISTS unset_claim;

ALTER TABLE claim ADD COLUMN from_address VARCHAR;

-- +migrate Up
CREATE TABLE unset_claim (
	block_num           INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
	block_pos           INTEGER NOT NULL,
	tx_hash             VARCHAR NOT NULL,
	global_index        TEXT NOT NULL,
	unset_global_index_hash_chain VARCHAR NOT NULL,
	created_at          INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	PRIMARY KEY (block_num, block_pos)
);

CREATE TABLE set_claim (
	block_num               INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
	block_pos               INTEGER NOT NULL,
	tx_hash                 VARCHAR NOT NULL,
	leaf_index              INTEGER NOT NULL,
	source_bridge_network   INTEGER NOT NULL,
	created_at              INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	PRIMARY KEY (block_num, block_pos)
);

ALTER TABLE claim DROP COLUMN from_address;
