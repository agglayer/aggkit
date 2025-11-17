-- +migrate Down
ALTER TABLE claim ADD COLUMN from_address VARCHAR;

-- +migrate Up
ALTER TABLE claim DROP COLUMN from_address;
