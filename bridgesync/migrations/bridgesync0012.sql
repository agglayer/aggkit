-- +migrate Down
ALTER TABLE claim DROP COLUMN source;

-- +migrate Up
ALTER TABLE claim ADD COLUMN source VARCHAR DEFAULT '';
