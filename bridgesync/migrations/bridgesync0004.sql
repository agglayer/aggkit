-- +migrate Down
ALTER TABLE bridge ADD COLUMN is_native_token BOOLEAN;

-- +migrate Up
ALTER TABLE bridge DROP COLUMN is_native_token;
