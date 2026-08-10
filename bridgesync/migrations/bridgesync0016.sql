-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_from_address_upper;
DROP INDEX IF EXISTS idx_bridge_destination_network;

-- +migrate Up
-- Backs the "UPPER(from_address) = UPPER($n)" predicate in buildBridgesFilterClause
-- (bridgesync/processor.go). A plain index on from_address cannot be used through the
-- UPPER() wrapper, so this is an expression index matching it exactly.
CREATE INDEX IF NOT EXISTS idx_bridge_from_address_upper ON bridge (UPPER(from_address));
CREATE INDEX IF NOT EXISTS idx_bridge_destination_network ON bridge (destination_network);
