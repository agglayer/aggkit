-- +migrate Down
CREATE TABLE IF NOT EXISTS claim (
	block_num               INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
	block_pos               INTEGER NOT NULL,
	global_index            TEXT NOT NULL,
	origin_network          INTEGER NOT NULL,
	origin_address          VARCHAR NOT NULL,
	destination_address     VARCHAR NOT NULL,
	amount                  TEXT NOT NULL,
	proof_local_exit_root   VARCHAR,
	proof_rollup_exit_root  VARCHAR,
	mainnet_exit_root       VARCHAR,
	rollup_exit_root        VARCHAR,
	global_exit_root        VARCHAR,
	destination_network     INTEGER NOT NULL,
	metadata                BLOB,
	is_message              BOOLEAN,
	tx_hash                 VARCHAR,
	block_timestamp         INTEGER,
	type                    TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (block_num, block_pos)
);

CREATE INDEX IF NOT EXISTS idx_claim_block_num_block_pos_desc ON claim (block_num DESC, block_pos DESC);
CREATE INDEX IF NOT EXISTS idx_claim_block_num_block_pos_asc ON claim (block_num ASC, block_pos ASC);
CREATE INDEX IF NOT EXISTS idx_claim_type_block ON claim (type, block_num);

CREATE TABLE IF NOT EXISTS unset_claim (
	block_num                     INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
	block_pos                     INTEGER NOT NULL,
	tx_hash                       VARCHAR NOT NULL,
	global_index                  TEXT NOT NULL,
	unset_global_index_hash_chain VARCHAR NOT NULL,
	created_at                    INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	PRIMARY KEY (block_num, block_pos)
);

CREATE TABLE IF NOT EXISTS set_claim (
	block_num    INTEGER NOT NULL REFERENCES block(num) ON DELETE CASCADE,
	block_pos    INTEGER NOT NULL,
	tx_hash      VARCHAR NOT NULL,
	global_index TEXT NOT NULL,
	created_at   INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	PRIMARY KEY (block_num, block_pos)
);

-- +migrate Up
DROP TABLE IF EXISTS set_claim;
DROP TABLE IF EXISTS unset_claim;
DROP TABLE IF EXISTS claim;
