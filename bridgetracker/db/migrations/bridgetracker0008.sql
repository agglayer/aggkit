-- +migrate Down
-- The older binary hashes with the v1 scheme: forget the v2 hashes, it recomputes NULL ones from
-- the row's data.
UPDATE activity_bridge SET revision_hash = NULL;

-- +migrate Up
-- The revision scheme is now v2 (domain.ActivityRevisionPrefix): it also hashes the bridge's key,
-- block_pos and contents, so a correction that keeps tx_hash and block_num changes the revision
-- too. Every stored hash is v1: forget them. SQLite cannot compute them, so the
-- store recomputes every NULL one from the row's data when it opens the database
-- (sqliteActivityStore.backfillRevisionHashes), before serving any revision.
UPDATE activity_bridge SET revision_hash = NULL;
