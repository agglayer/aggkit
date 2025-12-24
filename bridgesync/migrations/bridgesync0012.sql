-- +migrate Down
ALTER TABLE claim DROP COLUMN type;

-- +migrate Up
ALTER TABLE claim ADD COLUMN type VARCHAR DEFAULT '';
