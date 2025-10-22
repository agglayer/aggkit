-- +migrate Down
ALTER TABLE bridge ADD COLUMN calldata BLOB;
ALTER TABLE token_mapping ADD COLUMN calldata BLOB;
ALTER TABLE legacy_token_migration ADD COLUMN calldata BLOB;

-- +migrate Up
ALTER TABLE bridge DROP COLUMN calldata;
ALTER TABLE token_mapping DROP COLUMN calldata;
ALTER TABLE legacy_token_migration DROP COLUMN calldata;
