-- +migrate Down
ALTER TABLE bridge ADD COLUMN from_address VARCHAR;

-- +migrate Up
ALTER TABLE claim DROP COLUMN from_address;
