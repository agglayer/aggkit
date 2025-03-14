-- +migrate Down
ALTER TABLE token_mapping DROP COLUMN calldata;
ALTER TABLE bridge DROP COLUMN calldata;

-- +migrate Up
ALTER TABLE token_mapping ADD COLUMN calldata BLOB;
ALTER TABLE bridge ADD COLUMN calldata BLOB;