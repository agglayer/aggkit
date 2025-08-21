-- +migrate Down
DROP INDEX IF EXISTS idx_key_value_owner;
DROP INDEX IF EXISTS idx_key_value_owner_key;
DROP INDEX IF EXISTS idx_key_value_updated_at;

-- +migrate Up
-- Key value storage table indexes
CREATE INDEX idx_key_value_owner ON key_value(owner);
CREATE INDEX idx_key_value_owner_key ON key_value(owner, key);
CREATE INDEX idx_key_value_updated_at ON key_value(updated_at);
