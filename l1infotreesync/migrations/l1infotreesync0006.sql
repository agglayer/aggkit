-- +migrate Down
ALTER TABLE verify_batches DROP COLUMN tx_hash;
ALTER TABLE verify_batches DROP COLUMN block_timestamp;

-- +migrate Up
-- tx_hash and block_timestamp are nullable and never backfilled for rows synced before this
-- migration (issue #1817): existing rows keep them NULL, new rows always populate both.
ALTER TABLE verify_batches ADD COLUMN tx_hash VARCHAR;
ALTER TABLE verify_batches ADD COLUMN block_timestamp INTEGER;
