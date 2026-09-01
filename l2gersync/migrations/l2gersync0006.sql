-- +migrate Down
ALTER TABLE imported_global_exit_root_v2
DROP COLUMN block_timestamp;

-- +migrate Up
ALTER TABLE imported_global_exit_root_v2
ADD COLUMN block_timestamp INTEGER;
