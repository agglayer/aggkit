-- +migrate Down
CREATE TABLE _bridge_to_address_backup AS SELECT block_num, block_pos, to_address FROM bridge;
ALTER TABLE bridge DROP COLUMN to_address;
ALTER TABLE bridge ADD COLUMN to_address VARCHAR;
UPDATE bridge SET to_address = (SELECT to_address FROM _bridge_to_address_backup b WHERE b.block_num = bridge.block_num AND b.block_pos = bridge.block_pos);
DROP TABLE _bridge_to_address_backup;

-- +migrate Up
CREATE TABLE _bridge_to_address_backup AS SELECT block_num, block_pos, to_address FROM bridge;
CREATE INDEX _bridge_to_address_backup_idx ON _bridge_to_address_backup(block_num, block_pos);
ALTER TABLE bridge DROP COLUMN to_address;
ALTER TABLE bridge ADD COLUMN to_address VARCHAR DEFAULT '';
-- Only set to_address for rows != null
UPDATE bridge
SET to_address = (SELECT b.to_address FROM _bridge_to_address_backup b WHERE b.block_num = bridge.block_num AND b.block_pos = bridge.block_pos)
WHERE EXISTS (SELECT 1 FROM _bridge_to_address_backup b WHERE b.block_num = bridge.block_num AND b.block_pos = bridge.block_pos AND b.to_address IS NOT NULL);
DROP TABLE _bridge_to_address_backup;
