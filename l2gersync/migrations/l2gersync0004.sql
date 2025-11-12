-- +migrate Down
DROP TABLE IF EXISTS remove_ger_events;

-- +migrate Up
CREATE TABLE
	IF NOT EXISTS remove_ger_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		global_exit_root VARCHAR NOT NULL,
		block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
		created_at INTEGER NOT NULL DEFAULT (strftime('%s', 'now'))
	);
