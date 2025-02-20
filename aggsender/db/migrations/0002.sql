-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN aggchain_proof;
ALTER TABLE certificate_info DROP COLUMN finalized_l1_info_tree_root;
ALTER TABLE certificate_info_history DROP COLUMN aggchain_proof;
ALTER TABLE certificate_info_history DROP COLUMN finalized_l1_info_tree_root;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN aggchain_proof VARCHAR;
ALTER TABLE certificate_info ADD COLUMN finalized_l1_info_tree_root VARCHAR;
ALTER TABLE certificate_info_history ADD COLUMN aggchain_proof VARCHAR;
ALTER TABLE certificate_info_history ADD COLUMN finalized_l1_info_tree_root VARCHAR;
