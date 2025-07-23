-- +migrate Down
ALTER TABLE imported_global_exit_root DROP COLUMN block_pos;

-- +migrate Up
ALTER TABLE imported_global_exit_root ADD COLUMN block_pos INTEGER NOT NULL DEFAULT 0;