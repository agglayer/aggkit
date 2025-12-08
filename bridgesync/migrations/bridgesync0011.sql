-- +migrate Down
DROP TABLE IF EXISTS backward_let;

-- +migrate Up
CREATE TABLE
	backward_let (
		block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
		block_pos INTEGER NOT NULL,
		previous_deposit_count TEXT NOT NULL,
		previous_root VARCHAR NOT NULL,
		new_deposit_count TEXT NOT NULL,
		new_root VARCHAR NOT NULL,
		PRIMARY KEY (block_num, block_pos)
	);