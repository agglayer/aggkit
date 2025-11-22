-- +migrate Down
DROP TABLE IF EXISTS set_claim;
DROP TABLE IF EXISTS unset_claim;
DROP TABLE IF EXISTS invalid_claim;

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
	global_index            TEXT NOT NULL,
	created_at              INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
	PRIMARY KEY (block_num, block_pos)
);

CREATE TABLE invalid_claim (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    block_num               INTEGER NOT NULL,
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
    block_timestamp         INTEGER,
    tx_hash                 VARCHAR NOT NULL,
    created_at              INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
    reason                  VARCHAR NOT NULL
);

ALTER TABLE claim DROP COLUMN from_address;
