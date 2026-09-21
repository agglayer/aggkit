-- +migrate Down
ALTER TABLE activity_address DROP COLUMN include_tracking;
ALTER TABLE activity_address DROP COLUMN last_warnings;
ALTER TABLE activity_address DROP COLUMN refreshed;

-- +migrate Up
-- include_tracking: sticky flag set once any GET /activity/from/{from_address} call has asked
-- for includeTracking=true; from then on every background refresh (see
-- domain.ActivitySupervisedStore.RefreshAddress) enriches still-unclaimed bridges with their
-- tracker snapshot, since the background refresh cannot know a future request's own flag.
ALTER TABLE activity_address ADD COLUMN include_tracking INTEGER NOT NULL DEFAULT 0;

-- last_warnings: JSON snapshot of whatever ActivityBridgeScanner.BridgesFrom reported as
-- unreachable networks on the last background refresh (see domain.ActivityWarning);
-- overwritten every refresh rather than accumulated, so a recovered network's warning clears
-- promptly. NULL means nothing cached yet, or the last refresh reported no warnings.
ALTER TABLE activity_address ADD COLUMN last_warnings BLOB;

-- refreshed: set once RefreshAddress has completed at least once for this address (successful
-- or not) -- RegisterAndAwait's ready return value, read back to tell a caller with nothing
-- meaningful to show yet apart from one whose address genuinely has no activity (see
-- domain.ActivitySupervisedStore.RegisterAndAwait, activityCommand.Execute's 503 + Retry-After).
ALTER TABLE activity_address ADD COLUMN refreshed INTEGER NOT NULL DEFAULT 0;
