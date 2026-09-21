-- +migrate Down
ALTER TABLE activity_address DROP COLUMN generation;

-- +migrate Up
-- generation: bumped every time an address's row is (re)created from scratch -- a brand-new
-- registerAddress INSERT, a stale-schema reset, or a flush_cache delete followed by
-- re-registration. RefreshAddress captures it at the start of a run and only marks refreshed/
-- notifies waiters if it is still the same value when the run finishes, so a refresh started
-- against one registration can never mark a later, unrelated registration of the same
-- from_address ready (see sqliteActivityStore.RefreshAddress).
ALTER TABLE activity_address ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;
