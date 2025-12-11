-- +migrate Down
DROP TABLE IF EXISTS backward_let;

DROP TRIGGER IF EXISTS archive_bridge_before_delete;

DROP TABLE IF EXISTS bridge_archive;

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

------------------------------------------------------------------------------
-- Create archive table
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
		txn_sender VARCHAR
	);

------------------------------------------------------------------------------
-- Create BEFORE DELETE trigger: archive only deleted rows
------------------------------------------------------------------------------
CREATE TRIGGER IF NOT EXISTS archive_bridge_before_delete
BEFORE DELETE ON bridge
FOR EACH ROW
BEGIN
    INSERT INTO bridge_archive (
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
        txn_sender
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
        OLD.txn_sender
    );
END;