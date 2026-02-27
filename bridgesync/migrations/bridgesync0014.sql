-- +migrate Down
DROP TABLE IF EXISTS bridge_archive;
DROP TABLE IF EXISTS backward_let;
DROP TABLE IF EXISTS forward_let;
ALTER TABLE bridge DROP COLUMN source;

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

-- 'source' column on bridge is handled by the Go idempotent function
-- addSourceField (called via RunMigrations) so that it works on databases
-- that already have the column (e.g. v0.9.0) and those that do not (e.g.
-- v0.8.1). SQLite does not support ALTER TABLE … ADD COLUMN IF NOT EXISTS.

CREATE TABLE IF NOT EXISTS forward_let (
	block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
	block_pos INTEGER NOT NULL,
	block_timestamp INTEGER NOT NULL,
	tx_hash VARCHAR NOT NULL,
	previous_deposit_count TEXT NOT NULL,
	previous_root VARCHAR NOT NULL,
	new_deposit_count TEXT NOT NULL,
	new_root VARCHAR NOT NULL,
	new_leaves BLOB NOT NULL,
	PRIMARY KEY (block_num, block_pos)
);
------------------------------------------------------------------------------
-- Create bridge_archive table.
-- notice that from_address doesn't have default value, it's not possible to set
-- in table creation because v0.9.0 have the table created without default value 
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

