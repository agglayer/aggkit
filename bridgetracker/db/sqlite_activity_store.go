package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/db/migrations"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/finality"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

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
	// reported, or NULL if nothing has refreshed yet or the last refresh reported none. Messages
	// are stored verbatim, backend URLs included, exactly as they were logged: this column is
	// operator-facing, and redaction happens where the value becomes client-facing, when the wire
	// ActivityWarningItem is marshalled (see api.ActivityWarningItem.MarshalJSON)
	LastWarnings []byte `meddler:"last_warnings"`
	// Refreshed is set once RefreshAddress has completed at least once for this address
	// (successful or not) — RegisterAndAwait's ready return value
	Refreshed bool `meddler:"refreshed"`
	// Generation changes every time this row is (re)created from scratch — see registerAddress
	// and RefreshAddress's own doc for why: it lets a refresh started against one registration
	// tell that a later flush_cache + re-register replaced the row out from under it, instead of
	// stamping refreshed=1 (and implicitly signalling readiness) for a registration it never
	// actually refreshed
	Generation int64 `meddler:"generation"`
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

// decodeWarnings unmarshals an activity_address row's last_warnings column. Messages are
// returned exactly as stored, backend URLs included - they are operator-facing here (logs, the
// store itself) and are redacted only where they become client-facing, when the wire
// ActivityWarningItem is marshalled (see api.ActivityWarningItem.MarshalJSON).
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

// activityBridgeRow is the activity_bridge row shape (see migrations/bridgetracker0002.sql,
// bridgetracker0005.sql)
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
	// RevisionHash is domain.HashActivityEntry of the entry this row holds (32 bytes), written
	// together with it; nil for a row written before bridgetracker0005 (see revisionHash)
	RevisionHash []byte `meddler:"revision_hash"`
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
//
// BridgeFinal/ClaimFinal/Finalized persist the entry's finality (see domain.ActivityEntry), so a
// restart in the middle of a non-final state resumes exactly where it was: a bridge already seen
// final is still handed to the scanner as final (and so never re-read), and a settled entry is
// never refreshed again. They are absent (false) in a row written before they existed, which
// only means that row is rechecked once more
type activityBridgeData struct {
	Bridge      *bridgeservicetypes.BridgeResponse
	Claim       *bridgeservicetypes.ClaimResponse
	Errors      map[string]string
	Source      domain.ActivitySourceKind
	Tracking    *activityTrackingSnapshot
	BridgeFinal bool `json:",omitempty"`
	ClaimFinal  bool `json:",omitempty"`
	Finalized   bool `json:",omitempty"`
}

// entry decodes row into the domain.ActivityEntry it represents, for use as the cached entry of a
// refresh (see bridgetracker.PlanActivityRefresh) and as GetActivity's result. data.Errors is
// returned exactly as stored, backend URLs included - it is operator-facing here and is redacted
// only where it becomes client-facing, when the wire ActivityItem is marshalled (see
// api.ActivityItem.MarshalJSON)
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
		BridgeFinal:        data.BridgeFinal,
		ClaimFinal:         data.ClaimFinal,
		Finalized:          data.Finalized,
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

// parseTrackerClaimStatus reverses types.TrackerClaimStatus.String() (claimed_finalized
// included, see types.ParseTrackerClaimStatus), with the same unrecognized-value fallback as
// parseClaimStatus
func parseTrackerClaimStatus(s string) types.TrackerClaimStatus {
	status, err := types.ParseTrackerClaimStatus(s)
	if err != nil {
		return types.TrackerClaimStatusError
	}
	return status
}

// revisionHash is the row's contribution to its address's revision: the stored revision_hash, or
// for a row written before that column existed, the hash recomputed from its data
func (row *activityBridgeRow) revisionHash() (domain.ActivityEntryHash, error) {
	var hash domain.ActivityEntryHash
	if len(row.RevisionHash) == len(hash) {
		copy(hash[:], row.RevisionHash)
		return hash, nil
	}
	entry, err := row.entry()
	if err != nil {
		return hash, err
	}
	return domain.HashActivityEntry(entry), nil
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
	db        *sql.DB
	scanner   domain.ActivityBridgeScanner
	refresher *bridgetracker.ActivityEntryRefresher
	logger    aggkitcommon.Logger

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

// compile-time check: the SQLite adapter serves the per-address revision
var _ domain.ActivityRevisionReader = (*sqliteActivityStore)(nil)

// NewSQLiteActivityStore returns a domain.ActivityRegistry backed by a SQLite database at
// dbPath, creating the file and running its migrations if it does not exist yet. dbPath may
// (and typically does) point at the same file bridgetracker/db.NewSQLiteRegistry uses — every
// migration in this package is always applied together, regardless of which store is
// constructed first (see migrations.RunMigrations). resolver decides whether a bridge or claim
// is final (nil: nothing is ever final), exactly as for bridgetracker.NewActivityCache. Idle
// eviction is entirely driven by whoever wires an ActivityEngine over the returned store (see
// ActivityEngineConfig.IdleTimeout, PruneIdle) — this constructor has no idleTimeout of its own
// to fall back to
func NewSQLiteActivityStore(
	dbPath string, scanner domain.ActivityBridgeScanner, claims domain.ActivityClaimChecker,
	supervised domain.SupervisedStore, resolver finality.Resolver, logger aggkitcommon.Logger,
) (domain.ActivityRegistry, error) {
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
		refresher:    bridgetracker.NewActivityEntryRefresher(claims, supervised, resolver, logger),
		logger:       logger,
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
	// generation changes on every (re)creation of this row (see activityAddressRow.Generation's
	// doc); a nanosecond timestamp is unique enough between any two registrations of the same
	// address without needing a shared counter or an extra round trip
	generation := now.UnixNano()

	if !isNew {
		// A stale-schema row already exists and already counts toward numAddresses: overwrite
		// it in place, no capacity check or count bookkeeping involved. This is still a genuine
		// (re)registration — created must be true, exactly like the isNew INSERT path below, so
		// RegisterAndAwait wakes ActivityEngine and waits for the resulting refresh instead of
		// answering not-ready with nothing scheduled to change that
		_, err = s.db.Exec(
			`UPDATE activity_address SET
				schema_version = ?, updated_at = ?, last_access = ?, scan_state = ?,
				include_tracking = 0, last_warnings = NULL, refreshed = 0, generation = ?
			 WHERE from_address = ?`,
			activitySchemaVersion, now.Unix(), now.Unix(), emptyScanState, generation, addr,
		)
		return true, err
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
			 last_warnings, refreshed, generation)
		 VALUES (?, ?, ?, ?, ?, 0, NULL, 0, ?)`,
		addr, activitySchemaVersion, now.Unix(), now.Unix(), emptyScanState, generation,
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
// (ready) or timeout elapsed first with nothing to show yet. includeTracking, when true, sets
// the sticky include_tracking flag before signalling the trigger — not just on a later
// GetActivity call — so even the very first triggered refresh (the one a caller with timeout > 0
// blocks on) already enriches tracking, instead of the flag only taking effect on the refresh
// after that
func (s *sqliteActivityStore) RegisterAndAwait(
	fromAddress common.Address, includeTracking bool, timeout time.Duration,
) (bool, error) {
	addr := fromAddress.Hex()
	created, err := s.registerAddress(addr, s.now())
	if err != nil {
		if errors.Is(err, domain.ErrActivityRegistryFull) {
			return false, err
		}
		return false, fmt.Errorf("registering activity address %s: %w", fromAddress, err)
	}
	if includeTracking {
		if err := s.setIncludeTracking(addr); err != nil {
			return false, fmt.Errorf("setting include_tracking for %s: %w", fromAddress, err)
		}
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
// activityCommand.Execute's 503 + Retry-After). generation must be whatever RefreshAddress read
// from the row before doing any work: the UPDATE only takes effect if the row is still on that
// same generation, so a refresh started against one registration can never stamp refreshed=1 for
// a *later* registration of the same address that replaced it mid-flight (flush_cache + re-
// register, or a stale-schema reset — see registerAddress/activityAddressRow.Generation) — that
// later registration gets its own, separate refresh and its own generation-gated markRefreshed
// call. A no-op if the row is missing or already on a different generation; a write failure is
// logged, not propagated, same as registerAddress's own best-effort bookkeeping writes
func (s *sqliteActivityStore) markRefreshed(addr string, generation int64) {
	if _, err := s.db.Exec(
		"UPDATE activity_address SET refreshed = 1 WHERE from_address = ? AND generation = ?", addr, generation,
	); err != nil {
		s.logger.Warnf("bridgetracker: marking activity address %s refreshed: %v", addr, err)
	}
}

// setIncludeTracking sets addr's sticky include_tracking flag, if not already set — used both by
// RegisterAndAwait (so even the very first triggered refresh sees it) and GetActivity (for a
// caller that only asks for tracking on a later request)
func (s *sqliteActivityStore) setIncludeTracking(addr string) error {
	_, err := s.db.Exec(
		"UPDATE activity_address SET include_tracking = 1 WHERE from_address = ? AND include_tracking = 0", addr,
	)
	return err
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
// a sweep-on-every-request. An address with an in-flight RegisterAndAwait waiter is never pruned
// even if it is idle by olderThan, mirroring ActivityCache.PruneIdle's own "len(cache.waiters) ==
// 0" guard: without it, a request blocked waiting for a first refresh could have its row deleted
// out from under it (see subMu/waiters), landing RefreshAddress's ErrNotFound no-op and forcing
// the caller to burn its whole timeout before 503ing, instead of the two implementations of the
// same port agreeing on whether a waiter protects a row
func (s *sqliteActivityStore) PruneIdle(olderThan time.Time) (int, error) {
	s.subMu.Lock()
	excluded := make([]string, 0, len(s.waiters))
	for addr := range s.waiters {
		excluded = append(excluded, addr.Hex())
	}
	s.subMu.Unlock()

	query := "DELETE FROM activity_address WHERE last_access < ?"
	args := make([]any, 0, len(excluded)+1)
	args = append(args, olderThan.Unix())
	if len(excluded) > 0 {
		query += " AND from_address NOT IN (" + strings.TrimSuffix(strings.Repeat("?,", len(excluded)), ",") + ")"
		for _, addr := range excluded {
			args = append(args, addr)
		}
	}

	res, err := s.db.Exec(query, args...)
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
// RefreshAddress: it scans fromAddress's bridges (new ones plus a fresh read of every non-final
// one, see domain.ActivityBridgeScanner.BridgesFrom), merges the result into the stored rows (see
// bridgetracker.PlanActivityRefresh: corrections overwrite, invalidated non-final entries are
// deleted, every entry not settled yet is rechecked through bridgetracker.ActivityEntryRefresher),
// records the scan's warnings, and finally wakes every RegisterAndAwait call currently blocked on
// fromAddress. A failed scan still rechecks the stored entries (keeping them all) before the error
// is returned. Only ever called by ActivityEngine. A missing address row (not currently
// registered) is a silent no-op — the regular tick already only iterates GetActiveAddresses
func (s *sqliteActivityStore) RefreshAddress(ctx context.Context, fromAddress common.Address) error {
	addr := fromAddress.Hex()
	defer s.notifyWaiters(fromAddress)

	addrRow, err := s.selectAddressRow(addr)
	if err != nil {
		if errors.Is(err, aggkitdb.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("loading activity address %s: %w", fromAddress, err)
	}
	// generation is captured now, before any work: markRefreshed below only takes effect if the
	// row is still on this same generation once the refresh finishes (see its own doc) — a
	// flush_cache + re-register (or a stale-schema reset) racing with this call replaces the row
	// with a new generation, and that later registration's own refresh must be the one to mark it
	// ready, not this one finishing late against data that already predates it
	generation := addrRow.Generation
	defer s.markRefreshed(addr, generation)

	includeTracking := addrRow.IncludeTracking

	cached, err := s.cachedEntries(addr)
	if err != nil {
		return fmt.Errorf("listing cached bridges for %s: %w", fromAddress, err)
	}
	// A stale-schema row is deliberately excluded from known too (see cachedEntries): otherwise
	// the scanner's own dedup would treat it as already seen and never return it again, leaving
	// it neither refreshed nor ever overwritten
	known := make(map[domain.BridgeKey]domain.KnownBridge, len(cached))
	for key, entry := range cached {
		known[key] = bridgetracker.KnownBridgeOf(entry)
	}

	found, invalidated, warnings, scanErr := s.scanner.BridgesFrom(ctx, fromAddress, known)
	if scanErr != nil {
		// nothing the failed scan returned can be trusted: keep every entry, just recheck them
		found, invalidated = nil, nil
	}
	plan := bridgetracker.PlanActivityRefresh(cached, found, invalidated)
	if err := s.forgetBridges(addr, plan.Removed); err != nil {
		return fmt.Errorf("forgetting invalidated bridges for %s: %w", fromAddress, err)
	}
	for _, work := range plan.Items {
		entry := s.refresher.Refresh(ctx, work.Item, work.Existing, includeTracking, s.now())
		if err := s.saveBridgeRow(addr, work.Item, entry); err != nil {
			return err
		}
	}
	if scanErr != nil {
		return fmt.Errorf("scanning bridges from %s: %w", fromAddress, scanErr)
	}

	if err := s.recordScanWarnings(addr, warnings, s.now()); err != nil {
		return fmt.Errorf("recording scan warnings for %s: %w", fromAddress, err)
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

// cachedEntries decodes every current-schema activity_bridge row of addr, keyed by
// domain.BridgeKey (stale-schema rows are skipped: they are neither served nor refreshed, only
// overwritten once the scanner reports the bridge again, see activitySchemaVersion)
func (s *sqliteActivityStore) cachedEntries(addr string) (map[domain.BridgeKey]*domain.ActivityEntry, error) {
	rows, err := s.bridgeRows(addr)
	if err != nil {
		return nil, err
	}
	cached := make(map[domain.BridgeKey]*domain.ActivityEntry, len(rows))
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		entry, err := row.entry()
		if err != nil {
			return nil, err
		}
		cached[entry.Key()] = entry
	}
	return cached, nil
}

// bridgeRows returns every activity_bridge row cached for addr
func (s *sqliteActivityStore) bridgeRows(addr string) ([]*activityBridgeRow, error) {
	var rows []*activityBridgeRow
	if err := meddler.QueryAll(s.db, &rows, "SELECT * FROM activity_bridge WHERE from_address = ?", addr); err != nil {
		return nil, err
	}
	return rows, nil
}

// forgetBridges deletes addr's activity_bridge rows of keys — the invalidated bridges
// bridgetracker.PlanActivityRefresh decided to remove (see RefreshAddress), mirroring
// ActivityCache's own delete-from-cache handling of the same list. Scoped to addr, so a row another
// address holds for the same bridge is never touched, and matched by domain.BridgeKey (the
// reporting network and the deposit count), never by global index alone
func (s *sqliteActivityStore) forgetBridges(addr string, keys []domain.BridgeKey) error {
	for _, key := range keys {
		if _, err := s.db.Exec(
			`DELETE FROM activity_bridge WHERE from_address = ? AND origin_network_id = ?
				AND json_extract(data, '$.Bridge.deposit_count') = ?`,
			addr, key.NetworkID, key.DepositCount,
		); err != nil {
			return fmt.Errorf("forgetting activity_bridge row %s: %w", key, err)
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
		Tracking: trackingSnapshot, BridgeFinal: entry.BridgeFinal, ClaimFinal: entry.ClaimFinal,
		Finalized: entry.Finalized,
	})
	if err != nil {
		return err
	}
	revisionHash := domain.HashActivityEntry(entry)

	_, err = s.db.Exec(
		`INSERT INTO activity_bridge
			(global_index, from_address, schema_version, origin_network_id, destination_network,
			 tx_hash, claim_status, tracker_claim_status, block_timestamp, created_at, updated_at, data,
			 revision_hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (global_index) DO UPDATE SET
			from_address = excluded.from_address, schema_version = excluded.schema_version,
			origin_network_id = excluded.origin_network_id, destination_network = excluded.destination_network,
			tx_hash = excluded.tx_hash, claim_status = excluded.claim_status,
			tracker_claim_status = excluded.tracker_claim_status, block_timestamp = excluded.block_timestamp,
			created_at = excluded.created_at, updated_at = excluded.updated_at, data = excluded.data,
			revision_hash = excluded.revision_hash`,
		string(item.Bridge.GlobalIndex), addr, activitySchemaVersion, item.NetworkID, item.Bridge.DestinationNetwork,
		string(item.Bridge.TxHash), entry.ClaimStatus.String(), entry.TrackerClaimStatus.String(),
		int64(item.Bridge.BlockTimestamp), entry.CreatedAt.UnixNano(), entry.UpdatedAt.UnixNano(), data,
		revisionHash[:])
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
	if includeTracking {
		if err := s.setIncludeTracking(addr); err != nil {
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
		if bridgetracker.MatchesActivityFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	return out, warnings, nil
}

// GetActivityRevision implements domain.ActivityRevisionReader: the sum (see
// domain.ActivityRevision) of the revision hashes of fromAddress's current-schema rows — exactly
// the entries GetActivity serves, so it is the same value ActivityCache gives for the same
// entries. Computed from the stored rows (no address-level counter is persisted), so it is the
// same after a restart and after the address's rows are rebuilt. domain.EmptyActivityRevision for
// an address with nothing stored. Like ActivityCache's, it does not count as an access
func (s *sqliteActivityStore) GetActivityRevision(_ context.Context, fromAddress common.Address) (string, error) {
	rows, err := s.bridgeRows(fromAddress.Hex())
	if err != nil {
		return "", fmt.Errorf("listing cached bridges for %s: %w", fromAddress, err)
	}
	var revision domain.ActivityRevision
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		hash, err := row.revisionHash()
		if err != nil {
			return "", err
		}
		revision = revision.Add(hash)
	}
	return revision.String(), nil
}
