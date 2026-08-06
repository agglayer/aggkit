-- +migrate Down
DROP INDEX IF EXISTS idx_claim_global_index;
DROP INDEX IF EXISTS idx_unset_claim_global_index;
-- +migrate Up
CREATE INDEX IF NOT EXISTS idx_claim_global_index ON claim (global_index);
CREATE INDEX IF NOT EXISTS idx_unset_claim_global_index ON unset_claim (global_index);
