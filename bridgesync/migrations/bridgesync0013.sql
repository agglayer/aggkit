-- +migrate Down
ALTER TABLE bridge DROP COLUMN to_address;
ALTER TABLE bridge ADD COLUMN to_address VARCHAR;

-- +migrate Up
ALTER TABLE bridge DROP COLUMN to_address;
ALTER TABLE bridge ADD COLUMN to_address VARCHAR DEFAULT '';
