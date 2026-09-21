package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/db/migrations"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

// defaultActivityIdleTimeout mirrors bridgetracker.DefaultIdleTimeout (itself
// bridgetracker.DefaultEngineIdleTimeout), which ActivityCache also falls back to; kept in sync
// by hand since this package cannot import bridgetracker — see defaultMaxTrackedBridges
const defaultActivityIdleTimeout = 30 * time.Minute

// defaultMaxActivityAddresses mirrors bridgetracker.DefaultMaxActivityAddresses; kept in sync by
// hand, same reasoning as defaultMaxTrackedBridges
const defaultMaxActivityAddresses = 100_000

// activitySchemaVersion is the version of the Go shapes serialized into activity_address.
// scan_state and activity_bridge.data. Bumped whenever either shape changes in a way that
// would break decoding an older row — see trackedBridgeSchemaVersion's doc for the same idea
// applied to tracked_bridge; a stale row here is reset/discarded rather than decoded, exactly
// the same reasoning
const activitySchemaVersion = 1

// networkScanState is one network's entry in activity_address.scan_state: the last scan error
// for it, if any, so it survives across refreshes without touching bridges already cached from
// a previous successful scan of that same network (mirrors upsert's per-bridge failure
// handling, just for a whole network's scan). It is only ever updated on a failure — there is
// no reliable "this network just succeeded" signal from ActivityBridgeScanner.BridgesFrom today
// (it reports failed networks via ActivityWarning, but not which of the rest were actually
// attempted vs skipped), so LastErrorAt can go stale once a network recovers; treat it as "last
// known failure", not "current health"
type networkScanState struct {
	LastError   string `json:"last_error"`
	LastErrorAt int64  `json:"last_error_at"`
}

// activityAddressRow is the activity_address row shape (see migrations/bridgetracker0002.sql,
// bridgetracker0003.sql)
type activityAddressRow struct {
	FromAddress   string `meddler:"from_address"`
	SchemaVersion int    `meddler:"schema_version"`
	UpdatedAt     int64  `meddler:"updated_at"`
	LastAccess    int64  `meddler:"last_access"`
	ScanState     []byte `meddler:"scan_state"`
	// IncludeTracking is the sticky includeTracking flag (see domain.ActivityQuerier.
	// GetActivity's doc): once set, every background refresh enriches still-unclaimed bridges
	// with their tracker snapshot
	IncludeTracking bool `meddler:"include_tracking"`
	// LastWarnings is the JSON-encoded []domain.ActivityWarning the last background refresh
	// reported, or NULL if nothing has refreshed yet or the last refresh reported none
	LastWarnings []byte `meddler:"last_warnings"`
	// Refreshed is set once RefreshAddress has completed at least once for this address
	// (successful or not) — RegisterAndAwait's ready return value
	Refreshed bool `meddler:"refreshed"`
}

func (row *activityAddressRow) staleSchema() bool {
	return row.SchemaVersion != activitySchemaVersion
}

// decodeScanState unmarshals an activity_address row's scan_state column
func decodeScanState(raw []byte) (map[uint32]networkScanState, error) {
	scanState := make(map[uint32]networkScanState)
	if len(raw) == 0 {
		return scanState, nil
	}
	if err := json.Unmarshal(raw, &scanState); err != nil {
		return nil, fmt.Errorf("decoding activity scan_state: %w", err)
	}
	return scanState, nil
}

// decodeWarnings unmarshals an activity_address row's last_warnings column
func decodeWarnings(raw []byte) ([]domain.ActivityWarning, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var warnings []domain.ActivityWarning
	if err := json.Unmarshal(raw, &warnings); err != nil {
		return nil, fmt.Errorf("decoding activity last_warnings: %w", err)
	}
	return warnings, nil
}

// encodeWarnings marshals warnings for storage in last_warnings; an empty/nil slice encodes to
// nil (NULL column) rather than the literal "[]", so an address that has never had a warning
// and one whose warnings just cleared look identical
func encodeWarnings(warnings []domain.ActivityWarning) ([]byte, error) {
	if len(warnings) == 0 {
		return nil, nil
	}
	return json.Marshal(warnings)
}

// activityBridgeRow is the activity_bridge row shape (see migrations/bridgetracker0002.sql)
type activityBridgeRow struct {
	GlobalIndex        string `meddler:"global_index"`
	FromAddress        string `meddler:"from_address"`
	SchemaVersion      int    `meddler:"schema_version"`
	OriginNetworkID    uint32 `meddler:"origin_network_id"`
	DestinationNetwork uint32 `meddler:"destination_network"`
	TxHash             string `meddler:"tx_hash"`
	ClaimStatus        string `meddler:"claim_status"`
	TrackerClaimStatus string `meddler:"tracker_claim_status"`
	BlockTimestamp     int64  `meddler:"block_timestamp"`
	CreatedAt          int64  `meddler:"created_at"` // unix nanoseconds, to round-trip time.Time exactly
	UpdatedAt          int64  `meddler:"updated_at"` // unix nanoseconds, to round-trip time.Time exactly
	Data               []byte `meddler:"data"`
}

func (row *activityBridgeRow) staleSchema() bool {
	return row.SchemaVersion != activitySchemaVersion
}

// activityTrackingSnapshot is the JSON shape of ActivityEntry.Tracking persisted inside
// activityBridgeData.Tracking: TrackingData's own fields are private, so it is decomposed into
// its tx/steps parts exactly like trackedBridgeData does for tracked_bridge, and rebuilt via
// domain.NewTrackingData on read — TrackingData.ID() is not stored, since it is always the
// bridge's own origin network/tx hash, already available as this row's OriginNetworkID/TxHash
type activityTrackingSnapshot struct {
	Tx    domain.TrackingBridgeTx
	Steps []domain.BridgeStepPath
}

// activityBridgeData is what activityBridgeRow.Data holds, JSON-encoded: everything
// domain.ActivityEntry needs besides its indexed columns. Unlike before RefreshAddress and
// GetActivity were split into separate calls, Tracking is now persisted here too (see
// activityTrackingSnapshot) — it used to be sourced live from the in-memory result of the very
// same call that computed it, which no longer exists by the time GetActivity runs
type activityBridgeData struct {
	Bridge   *bridgeservicetypes.BridgeResponse
	Claim    *bridgeservicetypes.ClaimResponse
	Errors   map[string]string
	Source   domain.ActivitySourceKind
	Tracking *activityTrackingSnapshot
}

// entry decodes row into the domain.ActivityEntry it represents, for use as upsert's existing
// argument (the settled check, and refresh's own "already confirmed claimed" shortcut) and as
// GetActivity's result
func (row *activityBridgeRow) entry() (*domain.ActivityEntry, error) {
	var data activityBridgeData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return nil, fmt.Errorf("decoding activity_bridge row %s: %w", row.GlobalIndex, err)
	}
	entry := &domain.ActivityEntry{
		Bridge:             data.Bridge,
		BridgeNetworkID:    row.OriginNetworkID,
		Source:             data.Source,
		ClaimStatus:        parseClaimStatus(row.ClaimStatus),
		Claim:              data.Claim,
		TrackerClaimStatus: parseTrackerClaimStatus(row.TrackerClaimStatus),
		Errors:             data.Errors,
		CreatedAt:          time.Unix(0, row.CreatedAt).UTC(),
		UpdatedAt:          time.Unix(0, row.UpdatedAt).UTC(),
	}
	if data.Tracking != nil {
		id := domain.TrackingID{NetworkID: row.OriginNetworkID, TxHash: common.HexToHash(row.TxHash)}
		entry.Tracking = domain.NewTrackingData(id, data.Tracking.Tx, data.Tracking.Steps)
	}
	return entry, nil
}

// parseClaimStatus reverses types.ClaimStatus.String(); an unrecognized value (a row from a
// build that added a status this one doesn't know) falls back to Error rather than silently
// misreporting a bridge as unclaimed
func parseClaimStatus(s string) types.ClaimStatus {
	for _, status := range []types.ClaimStatus{
		types.ClaimStatusUnclaimed, types.ClaimStatusClaimed, types.ClaimStatusError,
	} {
		if status.String() == s {
			return status
		}
	}
	return types.ClaimStatusError
}

// parseTrackerClaimStatus reverses types.TrackerClaimStatus.String(), with the same
// unrecognized-value fallback as parseClaimStatus
func parseTrackerClaimStatus(s string) types.TrackerClaimStatus {
	for _, status := range []types.TrackerClaimStatus{
		types.TrackerClaimStatusPending, types.TrackerClaimStatusReadyToClaim,
		types.TrackerClaimStatusClaimed, types.TrackerClaimStatusError,
	} {
		if status.String() == s {
			return status
		}
	}
	return types.TrackerClaimStatusError
}

// sqliteActivityStore is the SQLite-backed implementation of domain.ActivityRegistry: it
// mirrors bridgetracker's in-memory ActivityCache (same scan/refresh logic against
// scanner/claims/supervised) but persists the per-address scan state and per-bridge cache to
// SQLite instead of in-process maps, so both survive a restart instead of re-scanning/
// re-checking every bridge's claim state from scratch.
//
// The wait/trigger plumbing behind RegisterAndAwait (waiters, trigger) is, like sqliteRegistry's
// own subscribers/trigger, inherently local to whichever instance holds the blocked
// goroutine/channel: only the cached data itself is persisted.
//
// Safe for concurrent use (every mutation is a self-contained SQL statement; SQLite itself
// serializes writers).
type sqliteActivityStore struct {
	db         *sql.DB
	scanner    domain.ActivityBridgeScanner
	claims     domain.ActivityClaimChecker
	supervised domain.SupervisedStore
	logger     aggkitcommon.Logger

	// idleTimeout is currently unused: PruneIdle's cutoff is now computed and passed in by
	// ActivityEngine (see ActivityEngineConfig.IdleTimeout) instead of being read from here —
	// kept as a constructor argument for backward compatibility with existing callers
	idleTimeout time.Duration
	// now is the clock updated_at/last_access is stamped with, injectable for tests
	now func() time.Time

	// countMu guards numAddresses, the in-memory mirror of activity_address's row count —
	// mirrors sqliteRegistry.countMu/numEntries
	countMu      sync.Mutex
	numAddresses int

	// trigger carries the from_addresses of freshly registered entries out to ActivityEngine
	// (see Triggers), mirroring sqliteRegistry.trigger
	trigger chan common.Address

	// subMu guards waiters, and also serializes registerAddress's "insert row, then register
	// the caller as a waiter, then signal the trigger" ordering in RegisterAndAwait — so a
	// caller can never miss the notification its own registration provokes
	subMu   sync.Mutex
	waiters map[common.Address]map[chan struct{}]struct{}
}

// compile-time check: the SQLite adapter fulfils the full port
var _ domain.ActivityRegistry = (*sqliteActivityStore)(nil)

// NewSQLiteActivityStore returns a domain.ActivityRegistry backed by a SQLite database at
// dbPath, creating the file and running its migrations if it does not exist yet. dbPath may
// (and typically does) point at the same file bridgetracker/db.NewSQLiteRegistry uses — every
// migration in this package is always applied together, regardless of which store is
// constructed first (see migrations.RunMigrations). idleTimeout <= 0 falls back to
// defaultActivityIdleTimeout, exactly like bridgetracker.NewActivityCache
func NewSQLiteActivityStore(
	dbPath string, scanner domain.ActivityBridgeScanner, claims domain.ActivityClaimChecker,
	supervised domain.SupervisedStore, logger aggkitcommon.Logger, idleTimeout time.Duration,
) (domain.ActivityRegistry, error) {
	if idleTimeout <= 0 {
		idleTimeout = defaultActivityIdleTimeout
	}
	if err := migrations.RunMigrations(dbPath); err != nil {
		return nil, fmt.Errorf("running bridgetracker migrations on %s: %w", dbPath, err)
	}
	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening bridgetracker db %s: %w", dbPath, err)
	}

	var numAddresses int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM activity_address").Scan(&numAddresses); err != nil {
		return nil, fmt.Errorf("counting activity_address rows: %w", err)
	}

	return &sqliteActivityStore{
		db:           sqlDB,
		scanner:      scanner,
		claims:       claims,
		supervised:   supervised,
		logger:       logger,
		idleTimeout:  idleTimeout,
		now:          time.Now,
		numAddresses: numAddresses,
		trigger:      make(chan common.Address, triggerBufferSize),
		waiters:      make(map[common.Address]map[chan struct{}]struct{}),
	}, nil
}

// Close releases the underlying DB connection. Not part of domain.ActivityRegistry: callers
// that construct a sqliteActivityStore via NewSQLiteActivityStore own the returned value's
// lifetime
func (s *sqliteActivityStore) Close() error {
	return s.db.Close()
}

// selectAddressRow returns addr's row, or db.ErrNotFound if there is none
func (s *sqliteActivityStore) selectAddressRow(addr string) (*activityAddressRow, error) {
	var row activityAddressRow
	err := meddler.QueryRow(s.db, &row, "SELECT * FROM activity_address WHERE from_address = ?", addr)
	if err != nil {
		return nil, aggkitdb.ReturnErrNotFound(err)
	}
	return &row, nil
}

// registerAddress ensures addr has a current-schema activity_address row, stamping last_access
// with now: creates one if missing or stale-schema (subject to defaultMaxActivityAddresses,
// only for a genuinely new address — a stale-schema row already counted), or otherwise just
// touches last_access. created reports whether this call is the one that (re)registered it, so
// RegisterAndAwait knows whether to wake ActivityEngine and wait for a refresh
func (s *sqliteActivityStore) registerAddress(addr string, now time.Time) (created bool, err error) {
	row, err := s.selectAddressRow(addr)
	if err != nil && !errors.Is(err, aggkitdb.ErrNotFound) {
		return false, err
	}
	isNew := errors.Is(err, aggkitdb.ErrNotFound)
	if !isNew && !row.staleSchema() {
		_, err = s.db.Exec("UPDATE activity_address SET last_access = ? WHERE from_address = ?", now.Unix(), addr)
		return false, err
	}

	emptyScanState, merr := json.Marshal(map[uint32]networkScanState{})
	if merr != nil {
		return false, merr
	}

	if !isNew {
		// A stale-schema row already exists and already counts toward numAddresses: overwrite
		// it in place, no capacity check or count bookkeeping involved.
		_, err = s.db.Exec(
			`UPDATE activity_address SET
				schema_version = ?, updated_at = ?, last_access = ?, scan_state = ?,
				include_tracking = 0, last_warnings = NULL, refreshed = 0
			 WHERE from_address = ?`,
			activitySchemaVersion, now.Unix(), now.Unix(), emptyScanState, addr,
		)
		return false, err
	}

	// isNew: reserve a capacity slot and insert under countMu, so two concurrent
	// registerAddress calls racing on the very same brand-new address can't both observe
	// aggkitdb.ErrNotFound above and both increment numAddresses — a plain INSERT (from_address
	// is the primary key) means only the call that actually creates the row succeeds; the loser
	// falls through to the race check below instead of double-counting (mirrors
	// sqliteRegistry.create's same race)
	s.countMu.Lock()
	if s.numAddresses >= defaultMaxActivityAddresses {
		s.countMu.Unlock()
		return false, domain.ErrActivityRegistryFull
	}
	_, insertErr := s.db.Exec(
		`INSERT INTO activity_address
			(from_address, schema_version, updated_at, last_access, scan_state, include_tracking,
			 last_warnings, refreshed)
		 VALUES (?, ?, ?, ?, ?, 0, NULL, 0)`,
		addr, activitySchemaVersion, now.Unix(), now.Unix(), emptyScanState,
	)
	if insertErr == nil {
		s.numAddresses++
		s.countMu.Unlock()
		return true, nil
	}
	s.countMu.Unlock()

	// Lost the race against a concurrent registerAddress for the same address, or hit a genuine
	// DB error: either way, check whether the row exists now before deciding which
	if _, selErr := s.selectAddressRow(addr); selErr != nil {
		return false, fmt.Errorf("registering activity address %s: %w", addr, insertErr)
	}
	return false, nil
}

// RegisterAndAwait implements domain.ActivitySupervisedStore. On an already-registered address
// it behaves like a plain touch: no trigger, no wait, ready reports its current refreshed
// column. On a newly registered address it wakes ActivityEngine (see signalTrigger/Triggers)
// and waits up to timeout for that first refresh to complete, reporting whether it actually did
// (ready) or timeout elapsed first with nothing to show yet
func (s *sqliteActivityStore) RegisterAndAwait(fromAddress common.Address, timeout time.Duration) (bool, error) {
	addr := fromAddress.Hex()
	created, err := s.registerAddress(addr, s.now())
	if err != nil {
		if errors.Is(err, domain.ErrActivityRegistryFull) {
			return false, err
		}
		return false, fmt.Errorf("registering activity address %s: %w", fromAddress, err)
	}
	if !created {
		return s.isRefreshed(addr), nil
	}
	if timeout <= 0 {
		s.signalTrigger(fromAddress)
		return false, nil
	}

	s.subMu.Lock()
	ch := make(chan struct{})
	s.addWaiterLocked(fromAddress, ch)
	s.subMu.Unlock()
	s.signalTrigger(fromAddress)

	defer s.removeWaiter(fromAddress, ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
	return s.isRefreshed(addr), nil
}

// isRefreshed reports whether addr's row currently has its refreshed column set; false for a
// missing row (defensive — should not happen given the caller always just registered it)
func (s *sqliteActivityStore) isRefreshed(addr string) bool {
	row, err := s.selectAddressRow(addr)
	if err != nil {
		return false
	}
	return row.Refreshed
}

// addWaiterLocked registers ch as a waiter for fromAddress's next refresh. Callers must hold subMu
func (s *sqliteActivityStore) addWaiterLocked(fromAddress common.Address, ch chan struct{}) {
	waiters, ok := s.waiters[fromAddress]
	if !ok {
		waiters = make(map[chan struct{}]struct{})
		s.waiters[fromAddress] = waiters
	}
	waiters[ch] = struct{}{}
}

// removeWaiter unregisters ch, dropping fromAddress's waiter set entirely once empty
func (s *sqliteActivityStore) removeWaiter(fromAddress common.Address, ch chan struct{}) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	waiters, ok := s.waiters[fromAddress]
	if !ok {
		return
	}
	delete(waiters, ch)
	if len(waiters) == 0 {
		delete(s.waiters, fromAddress)
	}
}

// notifyWaiters wakes every RegisterAndAwait call currently blocked on fromAddress
func (s *sqliteActivityStore) notifyWaiters(fromAddress common.Address) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	for ch := range s.waiters[fromAddress] {
		close(ch)
	}
	delete(s.waiters, fromAddress)
}

// markRefreshed stamps addr's activity_address row as having completed at least one background
// refresh (successful or not) — read back by RegisterAndAwait/isRefreshed to decide whether the
// address is ready to answer, or the client should be told to retry later (see
// activityCommand.Execute's 503 + Retry-After). A no-op if the row is missing (defensive: the
// caller always registered it first); a write failure is logged, not propagated, same as
// registerAddress's own best-effort bookkeeping writes
func (s *sqliteActivityStore) markRefreshed(addr string) {
	if _, err := s.db.Exec("UPDATE activity_address SET refreshed = 1 WHERE from_address = ?", addr); err != nil {
		s.logger.Warnf("bridgetracker: marking activity address %s refreshed: %v", addr, err)
	}
}

// signalTrigger notifies ActivityEngine that fromAddress was just registered. It never blocks:
// a full buffer just means this address waits for the next regular poll tick like before
func (s *sqliteActivityStore) signalTrigger(fromAddress common.Address) {
	select {
	case s.trigger <- fromAddress:
	default:
	}
}

// Triggers implements domain.ActivityTriggerable
func (s *sqliteActivityStore) Triggers() <-chan common.Address {
	return s.trigger
}

// GetActiveAddresses implements domain.ActivitySupervisedStore: every currently supervised
// from_address, for ActivityEngine's poll tick to iterate
func (s *sqliteActivityStore) GetActiveAddresses() ([]common.Address, error) {
	rows, err := s.db.Query("SELECT from_address FROM activity_address")
	if err != nil {
		return nil, fmt.Errorf("listing active activity addresses: %w", err)
	}
	defer rows.Close()

	var addrs []common.Address
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("scanning activity_address row: %w", err)
		}
		addrs = append(addrs, common.HexToAddress(addr))
	}
	return addrs, rows.Err()
}

// PruneIdle implements domain.ActivitySupervisedStore: it deletes every activity_address (and,
// via ON DELETE CASCADE, its activity_bridge rows) whose last_access is before olderThan,
// returning how many addresses were forgotten. This is the real retention sweep that used to be
// sweepIdle's no-op (see issue #1822) — now driven by ActivityEngine's own poll tick instead of
// a sweep-on-every-request
func (s *sqliteActivityStore) PruneIdle(olderThan time.Time) (int, error) {
	res, err := s.db.Exec("DELETE FROM activity_address WHERE last_access < ?", olderThan.Unix())
	if err != nil {
		return 0, fmt.Errorf("pruning idle activity addresses: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting pruned activity addresses: %w", err)
	}
	if affected > 0 {
		s.countMu.Lock()
		s.numAddresses -= int(affected)
		s.countMu.Unlock()
	}
	return int(affected), nil
}

// RefreshAddress implements domain.ActivitySupervisedStore, mirroring ActivityCache.
// RefreshAddress: it rechecks every bridge already cached for fromAddress that is not yet
// settled, scans for bridges not seen before (see domain.ActivityBridgeScanner.BridgesFrom),
// forgets whatever the scan reports as invalidated, records the scan's warnings, and finally
// wakes every RegisterAndAwait call currently blocked on fromAddress. This is what used to run
// inline inside GetActivity; it is now only ever called by ActivityEngine. A missing address row
// (not currently registered) is a silent no-op — the regular tick already only iterates
// GetActiveAddresses
func (s *sqliteActivityStore) RefreshAddress(ctx context.Context, fromAddress common.Address) error {
	addr := fromAddress.Hex()
	defer s.notifyWaiters(fromAddress)
	defer s.markRefreshed(addr)

	addrRow, err := s.selectAddressRow(addr)
	if err != nil {
		if errors.Is(err, aggkitdb.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("loading activity address %s: %w", fromAddress, err)
	}
	includeTracking := addrRow.IncludeTracking

	rows, err := s.bridgeRows(addr)
	if err != nil {
		return fmt.Errorf("listing cached bridges for %s: %w", fromAddress, err)
	}
	// A stale-schema row is deliberately excluded from known too, not just from the recheck loop
	// below: otherwise the scanner's own dedup (see domain.ActivityBridgeScanner.BridgesFrom)
	// would treat it as already seen and never return it again, leaving it neither refreshed
	// nor ever overwritten
	known := make(map[string]domain.KnownBridge, len(rows))
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		scanned, decodeErr := row.scannedBridge()
		if decodeErr != nil {
			return decodeErr
		}
		known[row.GlobalIndex] = domain.KnownBridge{
			TxHash: scanned.Bridge.TxHash, BlockNum: scanned.Bridge.BlockNum,
			Source: scanned.Source, NetworkID: row.OriginNetworkID,
		}
		if err := s.upsert(ctx, addr, scanned, includeTracking); err != nil {
			return err
		}
	}

	newItems, invalidated, warnings, err := s.scanner.BridgesFrom(ctx, fromAddress, known)
	if err != nil {
		return fmt.Errorf("scanning bridges from %s: %w", fromAddress, err)
	}
	if err := s.recordScanWarnings(addr, warnings, s.now()); err != nil {
		return fmt.Errorf("recording scan warnings for %s: %w", fromAddress, err)
	}
	if len(invalidated) > 0 {
		// forget these before upserting newItems (not after), mirroring ActivityCache.
		// RefreshAddress: a GlobalIndex the scanner reports as both found and invalidated in the
		// very same call (it should never, but this way a bug in that regard fails toward losing
		// a stale entry rather than a fresh one) must end up cached, not forgotten
		if err := s.forgetBridges(invalidated); err != nil {
			return fmt.Errorf("forgetting invalidated bridges for %s: %w", fromAddress, err)
		}
	}
	for _, item := range newItems {
		if err := s.upsert(ctx, addr, item, includeTracking); err != nil {
			return err
		}
	}

	lastWarnings, err := encodeWarnings(warnings)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		"UPDATE activity_address SET last_warnings = ? WHERE from_address = ?", lastWarnings, addr,
	); err != nil {
		return fmt.Errorf("recording last_warnings for %s: %w", fromAddress, err)
	}
	return nil
}

// scannedBridge rebuilds the domain.ScannedBridge a row was cached from, for the re-check loop
// in RefreshAddress (mirrors ActivityCache re-wrapping its cached entries the same way)
func (row *activityBridgeRow) scannedBridge() (*domain.ScannedBridge, error) {
	var data activityBridgeData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return nil, fmt.Errorf("decoding activity_bridge row %s: %w", row.GlobalIndex, err)
	}
	return &domain.ScannedBridge{Bridge: data.Bridge, NetworkID: row.OriginNetworkID, Source: data.Source}, nil
}

// bridgeRows returns every activity_bridge row cached for addr
func (s *sqliteActivityStore) bridgeRows(addr string) ([]*activityBridgeRow, error) {
	var rows []*activityBridgeRow
	if err := meddler.QueryAll(s.db, &rows, "SELECT * FROM activity_bridge WHERE from_address = ?", addr); err != nil {
		return nil, err
	}
	return rows, nil
}

// forgetBridges deletes every activity_bridge row keyed by one of globalIndexes — the invalidated
// bridges domain.ActivityBridgeScanner.BridgesFrom reports (see RefreshAddress), mirroring
// ActivityCache's own delete-from-cache handling of the same list
func (s *sqliteActivityStore) forgetBridges(globalIndexes []string) error {
	for _, globalIndex := range globalIndexes {
		if _, err := s.db.Exec("DELETE FROM activity_bridge WHERE global_index = ?", globalIndex); err != nil {
			return fmt.Errorf("forgetting activity_bridge row %s: %w", globalIndex, err)
		}
	}
	return nil
}

// FlushActivity implements domain.ActivityQuerier: deleting fromAddress's activity_address row
// cascades to every activity_bridge row cached for it (see the table's ON DELETE CASCADE), so the
// next background refresh rescans and rechecks everything from scratch, exactly as if fromAddress
// had never been requested before — mirrors ActivityCache.FlushActivity. Safe to call for an
// address with nothing cached (no-op); a failure is logged, not propagated, same as
// registerAddress/sqliteRegistry's own best-effort bookkeeping writes
func (s *sqliteActivityStore) FlushActivity(fromAddress common.Address) {
	res, err := s.db.Exec("DELETE FROM activity_address WHERE from_address = ?", fromAddress.Hex())
	if err != nil {
		s.logger.Warnf("bridgetracker: flushing activity cache for %s: %v", fromAddress, err)
		return
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return
	}
	s.countMu.Lock()
	s.numAddresses -= int(affected)
	s.countMu.Unlock()
}

// selectAddressRow's sibling for scan_state updates: recordScanWarnings merges warnings into
// addr's scan_state (see networkScanState); a no-op when warnings is empty, so a fully
// successful scan never rewrites scan_state
func (s *sqliteActivityStore) recordScanWarnings(addr string, warnings []domain.ActivityWarning, now time.Time) error {
	if len(warnings) == 0 {
		return nil
	}
	row, err := s.selectAddressRow(addr)
	if err != nil {
		return err
	}
	scanState, err := decodeScanState(row.ScanState)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		scanState[w.NetworkID] = networkScanState{LastError: w.Message, LastErrorAt: now.Unix()}
	}
	data, err := json.Marshal(scanState)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		"UPDATE activity_address SET scan_state = ?, updated_at = ? WHERE from_address = ?",
		data, now.Unix(), addr)
	return err
}

// upsert (re)computes item's entry via refresh and stores it, unless it is already cached and
// settled — in which case it is left untouched. Mirrors ActivityCache.upsert
func (s *sqliteActivityStore) upsert(
	ctx context.Context, addr string, item *domain.ScannedBridge, includeTracking bool,
) error {
	key := string(item.Bridge.GlobalIndex)

	existingRow, err := s.selectBridgeRow(key)
	if err != nil && !errors.Is(err, aggkitdb.ErrNotFound) {
		return err
	}
	var existing *domain.ActivityEntry
	if err == nil && !existingRow.staleSchema() {
		existing, err = existingRow.entry()
		if err != nil {
			return err
		}
		if settled(existing) {
			return nil
		}
	}

	entry := s.refresh(ctx, item, existing, includeTracking)
	return s.saveBridgeRow(addr, item, entry)
}

// selectBridgeRow returns global_index's row, or db.ErrNotFound if there is none
func (s *sqliteActivityStore) selectBridgeRow(globalIndex string) (*activityBridgeRow, error) {
	var row activityBridgeRow
	err := meddler.QueryRow(s.db, &row, "SELECT * FROM activity_bridge WHERE global_index = ?", globalIndex)
	if err != nil {
		return nil, aggkitdb.ReturnErrNotFound(err)
	}
	return &row, nil
}

// settled reports whether entry is done being rechecked: claimed, with the claim record already
// fetched. Mirrors ActivityCache's settled
func settled(entry *domain.ActivityEntry) bool {
	return entry.ClaimStatus == types.ClaimStatusClaimed && entry.Claim != nil
}

// refresh (re)computes the claim/tracking state of a single bridge item. This is
// ActivityCache.refresh verbatim (same branches, same logging), operating on the same driven
// ports (scanner/claims/supervised) — only where the result ends up (a SQL row instead of a map
// entry) differs, which is why the two are not shared: sharing would need a storage interface
// neither package's existing tests are written against yet
func (s *sqliteActivityStore) refresh(
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry, includeTracking bool,
) *domain.ActivityEntry {
	entry := &domain.ActivityEntry{Bridge: item.Bridge, BridgeNetworkID: item.NetworkID, Source: item.Source}
	if existing != nil {
		entry.CreatedAt = existing.CreatedAt
	} else {
		entry.CreatedAt = s.now()
	}
	entry.UpdatedAt = s.now()

	if existing != nil && existing.ClaimStatus == types.ClaimStatusClaimed {
		entry.ClaimStatus = types.ClaimStatusClaimed
	} else {
		claimed, err := s.claims.IsClaimed(ctx, item)
		if err != nil {
			s.logger.Warnf("activity: checking claim state of bridge tx=%s (network=%d, deposit=%d): %v",
				item.Bridge.TxHash, item.NetworkID, item.Bridge.DepositCount, err)
			entry.ClaimStatus = types.ClaimStatusError
			entry.TrackerClaimStatus = types.TrackerClaimStatusError
			entry.Errors = map[string]string{"claim": err.Error()}
			return entry
		}
		if claimed {
			entry.ClaimStatus = types.ClaimStatusClaimed
		} else {
			entry.ClaimStatus = types.ClaimStatusUnclaimed
		}
	}

	if entry.ClaimStatus == types.ClaimStatusClaimed {
		entry.TrackerClaimStatus = types.TrackerClaimStatusClaimed
		claim, err := s.claims.ClaimInfo(ctx, item)
		if err != nil {
			s.logger.Warnf("activity: fetching claim record of bridge tx=%s: %v", item.Bridge.TxHash, err)
		}
		entry.Claim = claim
		return entry
	}

	// Unclaimed: conservatively "pending" until proven otherwise, either by the tracker's own
	// snapshot (includeTracking) or the direct readiness check below
	entry.TrackerClaimStatus = types.TrackerClaimStatusPending

	if includeTracking {
		id := domain.TrackingID{NetworkID: item.NetworkID, TxHash: common.HexToHash(string(item.Bridge.TxHash))}
		tracking, err := s.supervised.Get(id, true)
		if err != nil {
			s.logger.Warnf("activity: registering bridge tx=%s with the tracker: %v", item.Bridge.TxHash, err)
		} else {
			entry.Tracking = tracking
			entry.TrackerClaimStatus = tracking.ClaimStatus()
			return entry
		}
	}

	// includeTracking was not requested, or registering with the tracker failed: fall back to
	// asking the bridge-service endpoints directly whether the bridge is already ready to claim,
	// without the cost of registering it with the tracker
	ready, err := s.claims.IsReadyToClaim(ctx, item)
	if err != nil {
		s.logger.Warnf("activity: checking claim readiness of bridge tx=%s (network=%d, deposit=%d): %v",
			item.Bridge.TxHash, item.NetworkID, item.Bridge.DepositCount, err)
		if entry.Errors == nil {
			entry.Errors = make(map[string]string)
		}
		entry.Errors["readiness"] = err.Error()
	} else if ready {
		entry.TrackerClaimStatus = types.TrackerClaimStatusReadyToClaim
	}
	return entry
}

// saveBridgeRow upserts entry as addr's row for item's global index
func (s *sqliteActivityStore) saveBridgeRow(
	addr string, item *domain.ScannedBridge, entry *domain.ActivityEntry,
) error {
	var trackingSnapshot *activityTrackingSnapshot
	if entry.Tracking != nil {
		trackingSnapshot = &activityTrackingSnapshot{
			Tx: entry.Tracking.TrackingBridgeTx(), Steps: entry.Tracking.AllSteps(),
		}
	}
	data, err := json.Marshal(activityBridgeData{
		Bridge: entry.Bridge, Claim: entry.Claim, Errors: entry.Errors, Source: entry.Source,
		Tracking: trackingSnapshot,
	})
	if err != nil {
		return err
	}

	_, err = s.db.Exec(
		`INSERT INTO activity_bridge
			(global_index, from_address, schema_version, origin_network_id, destination_network,
			 tx_hash, claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (global_index) DO UPDATE SET
			from_address = excluded.from_address, schema_version = excluded.schema_version,
			origin_network_id = excluded.origin_network_id, destination_network = excluded.destination_network,
			tx_hash = excluded.tx_hash, claim_status = excluded.claim_status,
			tracker_claim_status = excluded.tracker_claim_status, block_timestamp = excluded.block_timestamp,
			updated_at = excluded.updated_at, data = excluded.data`,
		string(item.Bridge.GlobalIndex), addr, activitySchemaVersion, item.NetworkID, item.Bridge.DestinationNetwork,
		string(item.Bridge.TxHash), entry.ClaimStatus.String(), entry.TrackerClaimStatus.String(),
		int64(item.Bridge.BlockTimestamp), entry.CreatedAt.UnixNano(), entry.UpdatedAt.UnixNano(), data)
	if err != nil {
		return fmt.Errorf("saving activity_bridge row %s: %w", item.Bridge.GlobalIndex, err)
	}
	return nil
}

// GetActivity implements domain.ActivityQuerier: a cache-only read of whatever the last
// background refresh (see RefreshAddress) computed for fromAddress, filtered per filter.
// includeTracking additionally sets the sticky include_tracking column for future refreshes —
// it does not itself fetch anything. An address never registered returns an empty result, not
// an error
func (s *sqliteActivityStore) GetActivity(
	_ context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	addr := fromAddress.Hex()

	addrRow, err := s.selectAddressRow(addr)
	if err != nil {
		if errors.Is(err, aggkitdb.ErrNotFound) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("loading activity address %s: %w", fromAddress, err)
	}

	now := s.now()
	if _, err := s.db.Exec(
		"UPDATE activity_address SET last_access = ? WHERE from_address = ?", now.Unix(), addr,
	); err != nil {
		return nil, nil, fmt.Errorf("touching last_access for %s: %w", fromAddress, err)
	}
	if includeTracking && !addrRow.IncludeTracking {
		if _, err := s.db.Exec(
			"UPDATE activity_address SET include_tracking = 1 WHERE from_address = ?", addr,
		); err != nil {
			return nil, nil, fmt.Errorf("setting include_tracking for %s: %w", fromAddress, err)
		}
	}

	warnings, err := decodeWarnings(addrRow.LastWarnings)
	if err != nil {
		return nil, nil, err
	}

	rows, err := s.bridgeRows(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listing cached bridges for %s: %w", fromAddress, err)
	}
	out := make([]*domain.ActivityEntry, 0, len(rows))
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		entry, err := row.entry()
		if err != nil {
			return nil, nil, err
		}
		if matchesFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	return out, warnings, nil
}

// matchesFilter reports whether entry belongs in a GetActivity result under filter. Mirrors
// ActivityCache's matchesFilter
func matchesFilter(entry *domain.ActivityEntry, filter types.ActivityFilter) bool {
	switch filter {
	case types.ActivityFilterClaimed:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusClaimed
	case types.ActivityFilterPending:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusPending
	case types.ActivityFilterReadyToClaim:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusReadyToClaim
	case types.ActivityFilterError:
		return entry.TrackerClaimStatus == types.TrackerClaimStatusError
	case types.ActivityFilterAll:
		return true
	default:
		return true
	}
}
