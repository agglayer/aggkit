-- +migrate Down
DROP TRIGGER IF EXISTS archive_bridge_before_delete;
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

ALTER TABLE bridge ADD COLUMN source TEXT;
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
        source TEXT,
        to_address VARCHAR
	);

------------------------------------------------------------------------------
-- Create BEFORE DELETE trigger: archive only deleted rows
------------------------------------------------------------------------------
CREATE TRIGGER IF NOT EXISTS archive_bridge_before_delete
BEFORE DELETE ON bridge
FOR EACH ROW
BEGIN
    INSERT OR IGNORE INTO bridge_archive (
        deposit_count,
        block_num,
        block_pos,
        leaf_type,
        origin_network,
        origin_address,
        destination_network,
        destination_address,
        amount,
        metadata,
        tx_hash,
        block_timestamp,
        txn_sender,
        from_address,
        source,
        to_address
    )
    VALUES (
        OLD.deposit_count,
        OLD.block_num,
        OLD.block_pos,
        OLD.leaf_type,
        OLD.origin_network,
        OLD.origin_address,
        OLD.destination_network,
        OLD.destination_address,
        OLD.amount,
        OLD.metadata,
        OLD.tx_hash,
        OLD.block_timestamp,
        OLD.txn_sender,
        OLD.from_address,
        OLD.source,
        OLD.to_address
    );
END;
