-- +migrate Down
DROP TABLE IF EXISTS remove_ger_events;

-- +migrate Up
CREATE TABLE
	IF NOT EXISTS remove_ger_events (
		global_exit_root VARCHAR NOT NULL,
		block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
		block_pos INTEGER NOT NULL,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now')),
		PRIMARY KEY (block_num, block_pos)
	);
