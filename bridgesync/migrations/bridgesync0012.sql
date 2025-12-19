-- +migrate Down
DROP TABLE IF EXISTS bridge_archive;
DROP TABLE IF EXISTS backward_let;
ALTER TABLE bridge DROP COLUMN source;
ALTER TABLE bridge DROP COLUMN to_address;

-- +migrate Up
CREATE TABLE IF NOT EXISTS backward_let (
		block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
		block_pos INTEGER NOT NULL,
		previous_deposit_count TEXT NOT NULL,
		previous_root VARCHAR NOT NULL,
		new_deposit_count TEXT NOT NULL,
		new_root VARCHAR NOT NULL,
		PRIMARY KEY (block_num, block_pos)
	);

ALTER TABLE bridge ADD COLUMN source TEXT DEFAULT '';
ALTER TABLE bridge ADD COLUMN to_address VARCHAR;

------------------------------------------------------------------------------
-- Create bridge_archive table
------------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS bridge_archive (
		deposit_count INTEGER PRIMARY KEY,
		block_num INTEGER NOT NULL,
		block_pos INTEGER NOT NULL,
		leaf_type INTEGER NOT NULL,
		origin_network INTEGER NOT NULL,
		origin_address VARCHAR NOT NULL,
		destination_network INTEGER NOT NULL,
		destination_address VARCHAR NOT NULL,
		amount TEXT NOT NULL,
		metadata BLOB,
		tx_hash VARCHAR,
		block_timestamp INTEGER,
		txn_sender VARCHAR,
        from_address VARCHAR,
        source TEXT DEFAULT '',
        to_address VARCHAR
	);
