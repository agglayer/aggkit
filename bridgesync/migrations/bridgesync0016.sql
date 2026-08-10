-- +migrate Down
DROP INDEX IF EXISTS idx_bridge_from_address_upper;

-- +migrate Up
-- Backs the "UPPER(from_address) = UPPER($n)" predicate in buildBridgesFilterClause
-- (bridgesync/processor.go). A plain index on from_address cannot be used through the
-- UPPER() wrapper, so this is an expression index matching it exactly.
-- Deliberately NO index on destination_network: aggkit never runs ANALYZE, so SQLite's
-- default heuristics prefer such an index over idx_bridge_deposit_count_desc (or the
-- expression index above), replacing early-exit ORDER BY deposit_count plans with full
-- scans + temp B-tree sorts.
CREATE INDEX IF NOT EXISTS idx_bridge_from_address_upper ON bridge (UPPER(from_address));
