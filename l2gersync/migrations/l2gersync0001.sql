-- +migrate Down
DROP TABLE IF EXISTS imported_global_exit_root;
DROP TABLE IF EXISTS block;

-- +migrate Up
CREATE TABLE IF NOT EXISTS block (
    num   BIGINT PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS imported_global_exit_root (
	block_num           INTEGER PRIMARY KEY REFERENCES block(num) ON DELETE CASCADE,
	global_exit_root    VARCHAR NOT NULL,
	l1_info_tree_index  INTEGER NOT NULL
);