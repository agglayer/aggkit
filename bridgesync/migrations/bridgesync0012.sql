-- +migrate Down
DROP INDEX IF EXISTS idx_claim_type_block;

ALTER TABLE claim
DROP COLUMN type;

-- +migrate Up
ALTER TABLE claim
ADD COLUMN type TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_claim_type_block ON claim (type, block_num);