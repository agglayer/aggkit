-- +migrate Down
DROP INDEX IF EXISTS idx_claim_type_block;

ALTER TABLE claim DROP COLUMN type;

ALTER TABLE bridge DROP COLUMN to_address;

-- +migrate Up
ALTER TABLE claim ADD COLUMN type TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_claim_type_block ON claim (type, block_num);

ALTER TABLE bridge ADD COLUMN to_address VARCHAR;
