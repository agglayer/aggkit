-- +migrate Up
ALTER TABLE bridge ADD COLUMN tx_sender VARCHAR DEFAULT '';

-- +migrate Down
ALTER TABLE bridge DROP COLUMN tx_sender;
