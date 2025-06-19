--- Fix migration from 0001 to 0002 that field l1_inffo_tree_leaf_count can't be read
-- +migrate Down


-- +migrate Up

-- The field l1_info_tree_leaf_count is unknown for previous data stored in DB
-- the only place where is used is to retry a FEP. So we delete all non-finalized certificates
-- so it can't produce the error keeping as much data as possible.
DELETE FROM certificate_info WHERE  status != 4 AND l1_info_tree_leaf_count IS NULL; -- Remove non-finalized certificates with NULL l1_info_tree_leaf_count
DELETE FROM certificate_info_history WHERE status != 4 AND l1_info_tree_leaf_count IS NULL; -- Remove non-finalized certificates with NULL l1_info_tree_leaf_count
--- The rest of l1info_tree_leaf_count is not used in the code, so we can safely set to 0
UPDATE certificate_info SET l1_info_tree_leaf_count = 0 WHERE l1_info_tree_leaf_count IS NULL;
UPDATE certificate_info_history SET l1_info_tree_leaf_count = 0 WHERE l1_info_tree_leaf_count IS NULL;
