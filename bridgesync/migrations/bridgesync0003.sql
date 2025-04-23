-- +migrate Down
ALTER TABLE token_mapping
DROP COLUMN is_not_mintable;

ALTER TABLE token_mapping
DROP COLUMN token_type;

DROP TABLE IF EXISTS legacy_token_migration;

ALTER TABLE block
DROP COLUMN hash;

-- +migrate Up
ALTER TABLE token_mapping
ADD COLUMN is_not_mintable BOOLEAN NOT NULL;

ALTER TABLE token_mapping
ADD COLUMN token_type SMALLINT NOT NULL;

CREATE TABLE
    legacy_token_migration (
        block_num INTEGER NOT NULL REFERENCES block (num) ON DELETE CASCADE,
        block_pos INTEGER NOT NULL,
        block_timestamp INTEGER NOT NULL,
        tx_hash VARCHAR NOT NULL,
        sender VARCHAR NOT NULL,
        legacy_token_address VARCHAR NOT NULL,
        updated_token_address VARCHAR NOT NULL,
        amount VARCHAR NOT NULL,
        calldata BLOB,
        PRIMARY KEY (block_num, block_pos)
    );

ALTER TABLE block
ADD COLUMN hash VARCHAR;