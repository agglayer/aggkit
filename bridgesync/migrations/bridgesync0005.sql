-- +migrate Down
ALTER TABLE bridge DROP COLUMN tx_sender;
ALTER TABLE claim DROP COLUMN tx_sender;

-- +migrate Up
ALTER TABLE bridge ADD COLUMN tx_sender VARCHAR NOT NULL;
ALTER TABLE claim ADD COLUMN tx_sender VARCHAR NOT NULL;

