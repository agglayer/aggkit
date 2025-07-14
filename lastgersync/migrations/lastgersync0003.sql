-- +migrate Down
ALTER TABLE imported_global_exit_root DROP block_pos INTEGER;

-- +migrate Up
ALTER TABLE imported_global_exit_root ADD block_pos INTEGER NULL;