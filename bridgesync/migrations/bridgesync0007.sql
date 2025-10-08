-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_txn_sender;

-- +migrate Up
CREATE INDEX IF NOT EXISTS idx_bridge_txn_sender ON bridge(txn_sender);
