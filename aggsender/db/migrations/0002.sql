-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN aggchain_proof;
ALTER TABLE certificate_info DROP COLUMN finalized_l1_info_tree_root;
ALTER TABLE certificate_info DROP COLUMN l1_info_tree_leaf_count;
ALTER TABLE certificate_info_history DROP COLUMN aggchain_proof;
ALTER TABLE certificate_info_history DROP COLUMN finalized_l1_info_tree_root;
ALTER TABLE certificate_info_history DROP COLUMN l1_info_tree_leaf_count;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN aggchain_proof BLOB;
ALTER TABLE certificate_info ADD COLUMN finalized_l1_info_tree_root VARCHAR;
ALTER TABLE certificate_info ADD COLUMN l1_info_tree_leaf_count INTEGER DEFAULT 0;
ALTER TABLE certificate_info_history ADD COLUMN aggchain_proof BLOB;
ALTER TABLE certificate_info_history ADD COLUMN finalized_l1_info_tree_root VARCHAR;
ALTER TABLE certificate_info_history ADD COLUMN l1_info_tree_leaf_count INTEGER DEFAULT 0;
-- The field l1_info_tree_leaf_count is unknown for previous data stored in DB
-- the only place where is used is to retry a FEP. So we delete all non-finalized certificates
-- so it can't produce the error keeping as much data as possible.
DELETE FROM certificate_info WHERE  status != 4; -- Remove non-finalized certificates
DELETE FROM certificate_info_history WHERE status != 4; -- Remove non-finalized certificates
