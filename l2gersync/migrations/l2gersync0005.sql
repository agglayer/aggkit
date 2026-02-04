
-- +migrate Down

CREATE TABLE IF NOT EXISTS imported_global_exit_root (
    block_num INTEGER PRIMARY KEY REFERENCES block (num) ON DELETE CASCADE,
    block_pos INTEGER,
    global_exit_root VARCHAR NOT NULL,
    l1_info_tree_index INTEGER NOT NULL
);

INSERT INTO imported_global_exit_root (block_num, block_pos, global_exit_root, l1_info_tree_index)
SELECT
    block_num,
    block_pos,
    global_exit_root,
    l1_info_tree_index
FROM imported_global_exit_root_v2;

DROP TABLE IF EXISTS imported_global_exit_root_v2;

-- +migrate Up

CREATE TABLE
	IF NOT EXISTS imported_global_exit_root_v2 (
		block_num INTEGER NOT NULL,
        block_pos INTEGER NOT NULL,
		global_exit_root VARCHAR NOT NULL,
		l1_info_tree_index INTEGER NOT NULL,
        PRIMARY KEY (block_num, block_pos),
        FOREIGN KEY (block_num) REFERENCES block(num) ON DELETE CASCADE
	);

INSERT INTO imported_global_exit_root_v2 (
    block_num,
    block_pos,
    global_exit_root,
    l1_info_tree_index
)
SELECT
    block_num,
    COALESCE(block_pos, 0) AS block_pos,
    global_exit_root,
    l1_info_tree_index
FROM imported_global_exit_root;

DROP TABLE imported_global_exit_root;
