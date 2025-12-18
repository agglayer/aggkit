-- +migrate Down
ALTER TABLE bridge DROP COLUMN to_address;

-- +migrate Up
ALTER TABLE bridge ADD COLUMN to_address VARCHAR;

