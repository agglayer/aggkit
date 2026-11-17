-- +migrate Down
ALTER TABLE activity_bridge DROP COLUMN revision_hash;

-- +migrate Up
-- revision_hash: the entry's contribution to its address's revision (domain.HashActivityEntry,
-- 32 bytes), a pure function of the row's own data, written together with it. The address's
-- revision is the sum of these hashes over its current-schema rows, so it needs no address-level
-- counter: it is the same after a restart, after the address's cache is rebuilt and on every
-- instance holding the same rows. NULL for a row written before this migration: it is computed
-- from the row's data instead until the row is written again (see
-- sqliteActivityStore.GetActivityRevision).
ALTER TABLE activity_bridge ADD COLUMN revision_hash BLOB;
