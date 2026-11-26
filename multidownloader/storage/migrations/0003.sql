-- +migrate Down
DROP INDEX IF EXISTS idx_transactions_block_number;
DROP TABLE IF EXISTS transactions;
-- +migrate Up
-- Canonical transaction inclusion table.
--
-- A transaction row is keyed by (tx_hash, block_hash) so the same transaction
-- hash included in two different blocks (a stale inclusion plus the canonical
-- one, or two distinct chain inclusions) is stored as two distinct rows and
-- never conflated.
--
-- There is intentionally NO foreign key to blocks(block_number): deleting a
-- block by number must never blindly cascade-delete inclusions that belong to a
-- different block that happens to share the same number. Canonical integrity is
-- enforced instead by validating (block_number, block_hash) against blocks on
-- insert and by joining blocks(block_number, block_hash) on read.
CREATE TABLE transactions (
    tx_hash TEXT NOT NULL,
    block_hash TEXT NOT NULL,
    block_number BIGINT NOT NULL,
    tx_index INTEGER NOT NULL,
    from_address TEXT NOT NULL,
    to_address TEXT,                 -- NULL for contract creation
    input BLOB,
    raw TEXT NOT NULL,               -- original JSON-RPC payload
    PRIMARY KEY (tx_hash, block_hash)
);

CREATE INDEX idx_transactions_block_number ON transactions(block_number);
