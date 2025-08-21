-- +migrate Down
DROP INDEX IF EXISTS idx_imported_ger_global_exit_root;
DROP INDEX IF EXISTS idx_imported_ger_l1_info_tree_index;

-- +migrate Up
-- Imported global exit root table indexes
CREATE INDEX idx_imported_ger_global_exit_root ON imported_global_exit_root(global_exit_root);
CREATE INDEX idx_imported_ger_l1_info_tree_index ON imported_global_exit_root(l1_info_tree_index DESC);
