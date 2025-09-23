-- +migrate Down
ALTER TABLE bridge ADD COLUMN global_index TEXT NOT NULL,;

-- +migrate Up
ALTER TABLE bridge DROP COLUMN global_index;
