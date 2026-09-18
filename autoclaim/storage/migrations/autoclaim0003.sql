-- +migrate Down
-- Reverses autoclaim0003: collapses the per-(source, destination) LER cursors back to a single cursor
-- per source and folds any still-unseeded legacy rows back in. Per-destination granularity cannot be
-- preserved, so the most conservative pair per source is kept (lowest last_verify_block_num, then lowest
-- destination_network): rolling back then re-fetches from an older LER, which is idempotent
-- (EnqueueRequest is insert-once) rather than skipping candidates.
CREATE TABLE autoclaim_ler_cursor_old (
    source_network               INTEGER PRIMARY KEY,
    last_ler                     TEXT NOT NULL,
    last_verify_block_num        INTEGER NOT NULL,
    updated_at                   TIMESTAMP NOT NULL
);
INSERT INTO autoclaim_ler_cursor_old (source_network, last_ler, last_verify_block_num, updated_at)
SELECT c.source_network, c.last_ler, c.last_verify_block_num, c.updated_at
FROM autoclaim_ler_cursor c
WHERE c.destination_network = (
    SELECT c2.destination_network
    FROM autoclaim_ler_cursor c2
    WHERE c2.source_network = c.source_network
    ORDER BY c2.last_verify_block_num ASC, c2.destination_network ASC
    LIMIT 1
);
INSERT OR IGNORE INTO autoclaim_ler_cursor_old (source_network, last_ler, last_verify_block_num, updated_at)
SELECT source_network, last_ler, last_verify_block_num, updated_at
FROM autoclaim_ler_cursor_legacy;
DROP TABLE autoclaim_ler_cursor;
DROP TABLE autoclaim_ler_cursor_legacy;
ALTER TABLE autoclaim_ler_cursor_old RENAME TO autoclaim_ler_cursor;

-- +migrate Up
-- Re-keys the LER discovery cursor from source_network to (source_network, destination_network).
-- autoclaim0002 shipped in v0.11.0-rc3..rc10, so existing per-source rows carry real processed history
-- and cannot be discarded. They are parked in autoclaim_ler_cursor_legacy; the L2ToLx detector fans each
-- parked row out to the destinations configured at the time of the first post-upgrade poll for that
-- source, and deletes the parked row in the same transaction (see SeedLERCursorsFromLegacy).
-- The destination set is a runtime fact (the enabled claimer registry), which is why the fan-out cannot
-- happen in SQL here.
-- No secondary index is created: the composite primary key is the only access path, and a plain
-- low-cardinality index would hijack the query planner in a database that never runs ANALYZE.
ALTER TABLE autoclaim_ler_cursor RENAME TO autoclaim_ler_cursor_legacy;

CREATE TABLE autoclaim_ler_cursor (
    source_network               INTEGER NOT NULL,
    destination_network          INTEGER NOT NULL,
    last_ler                     TEXT NOT NULL,
    last_verify_block_num        INTEGER NOT NULL,
    updated_at                   TIMESTAMP NOT NULL,
    PRIMARY KEY (source_network, destination_network)
);
