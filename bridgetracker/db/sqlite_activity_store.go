package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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

// activitySchemaVersion is the version of the Go shapes serialized into activity_address.
// scan_state and activity_bridge.data. Bumped whenever either shape changes in a way that
// would break decoding an older row — see trackedBridgeSchemaVersion's doc for the same idea
// applied to tracked_bridge; a stale row here is reset/discarded rather than decoded, exactly
// the same reasoning
const activitySchemaVersion = 1

// networkScanState is one network's entry in activity_address.scan_state: the last scan error
// for it, if any, so it survives across GetActivity calls without touching bridges already
// cached from a previous successful scan of that same network (mirrors upsert's per-bridge
// failure handling, just for a whole network's scan). It is only ever updated on a failure —
// there is no reliable "this network just succeeded" signal from ActivityBridgeScanner.
// BridgesFrom today (it reports failed networks via ActivityWarning, but not which of the rest
// were actually attempted vs skipped), so LastErrorAt can go stale once a network recovers;
// treat it as "last known failure", not "current health"
type networkScanState struct {
	LastError   string `json:"last_error"`
	LastErrorAt int64  `json:"last_error_at"`
}

// activityAddressRow is the activity_address row shape (see migrations/bridgetracker0002.sql)
type activityAddressRow struct {
	FromAddress   string `meddler:"from_address"`
	SchemaVersion int    `meddler:"schema_version"`
	UpdatedAt     int64  `meddler:"updated_at"`
	LastAccess    int64  `meddler:"last_access"`
	ScanState     []byte `meddler:"scan_state"`
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

// activityBridgeData is what activityBridgeRow.Data holds, JSON-encoded: everything
// domain.ActivityEntry needs besides its indexed columns and its Tracking (always sourced live
// from tracked_bridge, never duplicated here — see sqliteActivityStore.refresh)
type activityBridgeData struct {
	Bridge *bridgeservicetypes.BridgeResponse
	Claim  *bridgeservicetypes.ClaimResponse
	Errors map[string]string
}

// entry decodes row into the domain.ActivityEntry it represents, for use as upsert's existing
// argument (the settled check, and refresh's own "already confirmed claimed" shortcut).
// Tracking is always nil here: it is never persisted (see activityBridgeData) and refresh only
// ever needs Tracking for a still-unclaimed entry, which is never settled and always refreshed
// again this same call anyway — see GetActivity's doc for where a fresh Tracking comes from
func (row *activityBridgeRow) entry() (*domain.ActivityEntry, error) {
	var data activityBridgeData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return nil, fmt.Errorf("decoding activity_bridge row %s: %w", row.GlobalIndex, err)
	}
	return &domain.ActivityEntry{
		Bridge:             data.Bridge,
		BridgeNetworkID:    row.OriginNetworkID,
		ClaimStatus:        parseClaimStatus(row.ClaimStatus),
		Claim:              data.Claim,
		TrackerClaimStatus: parseTrackerClaimStatus(row.TrackerClaimStatus),
		Errors:             data.Errors,
		CreatedAt:          time.Unix(0, row.CreatedAt).UTC(),
		UpdatedAt:          time.Unix(0, row.UpdatedAt).UTC(),
	}, nil
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

// sqliteActivityStore is the SQLite-backed implementation of domain.ActivityQuerier: it mirrors
// bridgetracker's in-memory ActivityCache (same scan/refresh logic against scanner/claims/
// supervised) but persists the per-address scan state and per-bridge cache to SQLite instead of
// in-process maps, so both survive a restart instead of re-scanning/re-checking every bridge's
// claim state from scratch.
//
// Safe for concurrent use (every mutation is a self-contained SQL statement; SQLite itself
// serializes writers).
type sqliteActivityStore struct {
	db         *sql.DB
	scanner    domain.ActivityBridgeScanner
	claims     domain.ActivityClaimChecker
	supervised domain.SupervisedStore
	logger     aggkitcommon.Logger

	// idleTimeout is currently unused: sweepIdle (the sweep it would drive) is a no-op for now
	// (see its doc) — kept as a constructor argument so re-enabling it later needs no signature
	// change
	idleTimeout time.Duration
	// now is the clock updated_at/last_access is stamped with, injectable for tests
	now func() time.Time
}

// compile-time check: the SQLite adapter fulfils the port
var _ domain.ActivityQuerier = (*sqliteActivityStore)(nil)

// NewSQLiteActivityStore returns a domain.ActivityQuerier backed by a SQLite database at
// dbPath, creating the file and running its migrations if it does not exist yet. dbPath may
// (and typically does) point at the same file bridgetracker/db.NewSQLiteRegistry uses — every
// migration in this package is always applied together, regardless of which store is
// constructed first (see migrations.RunMigrations). idleTimeout <= 0 falls back to
// defaultActivityIdleTimeout, exactly like bridgetracker.NewActivityCache — though, for now,
// see sweepIdle's doc, it has no actual effect yet
func NewSQLiteActivityStore(
	dbPath string, scanner domain.ActivityBridgeScanner, claims domain.ActivityClaimChecker,
	supervised domain.SupervisedStore, logger aggkitcommon.Logger, idleTimeout time.Duration,
) (domain.ActivityQuerier, error) {
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

	return &sqliteActivityStore{
		db:          sqlDB,
		scanner:     scanner,
		claims:      claims,
		supervised:  supervised,
		logger:      logger,
		idleTimeout: idleTimeout,
		now:         time.Now,
	}, nil
}

// Close releases the underlying DB connection. Not part of domain.ActivityQuerier: callers that
// construct a sqliteActivityStore via NewSQLiteActivityStore own the returned value's lifetime
func (s *sqliteActivityStore) Close() error {
	return s.db.Close()
}

// GetActivity implements domain.ActivityQuerier, mirroring ActivityCache.GetActivity: it
// rechecks every bridge already cached for fromAddress that is not yet settled, then scans for
// bridges not seen before (see domain.ActivityBridgeScanner.BridgesFrom), and returns everything
// cached for fromAddress that matches filter.
//
// Unlike a plain SELECT of what's now in activity_bridge, the result is built straight from
// what upsert just (re)computed in this same call: entry.Tracking (populated by refresh, for a
// still-unclaimed bridge with includeTracking) is deliberately never written to the row — it's
// always sourced live from tracked_bridge, never duplicated — so a row decoded fresh from the DB
// would come back with a nil Tracking even right after refreshing it. Reusing refresh's own
// in-memory result avoids both that gap and a second, redundant supervised.Get call per bridge
func (s *sqliteActivityStore) GetActivity(
	ctx context.Context, fromAddress common.Address, includeTracking bool, filter types.ActivityFilter,
) ([]*domain.ActivityEntry, []domain.ActivityWarning, error) {
	addr := fromAddress.Hex()
	now := s.now()

	if err := s.sweepIdle(now); err != nil {
		return nil, nil, fmt.Errorf("sweeping idle activity addresses: %w", err)
	}
	if err := s.ensureAddress(addr, now); err != nil {
		return nil, nil, fmt.Errorf("registering activity address %s: %w", fromAddress, err)
	}

	rows, err := s.bridgeRows(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("listing cached bridges for %s: %w", fromAddress, err)
	}
	// A stale-schema row is deliberately excluded from known too, not just from the recheck loop
	// below: otherwise the scanner's own dedup (see domain.ActivityBridgeScanner.BridgesFrom)
	// would treat it as already seen and never return it again, leaving it neither refreshed
	// nor ever overwritten
	known := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		known[row.GlobalIndex] = struct{}{}
	}

	entries := make(map[string]*domain.ActivityEntry, len(rows))
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		scanned, decodeErr := row.scannedBridge()
		if decodeErr != nil {
			return nil, nil, decodeErr
		}
		entry, err := s.upsert(ctx, addr, scanned, includeTracking, filter)
		if err != nil {
			return nil, nil, err
		}
		entries[row.GlobalIndex] = entry
	}

	newItems, warnings, err := s.scanner.BridgesFrom(ctx, fromAddress, known)
	if err != nil {
		return nil, nil, fmt.Errorf("scanning bridges from %s: %w", fromAddress, err)
	}
	if err := s.recordScanWarnings(addr, warnings, now); err != nil {
		return nil, nil, fmt.Errorf("recording scan warnings for %s: %w", fromAddress, err)
	}
	for _, item := range newItems {
		entry, err := s.upsert(ctx, addr, item, includeTracking, filter)
		if err != nil {
			return nil, nil, err
		}
		entries[item.Bridge.GlobalIndex.String()] = entry
	}

	out := make([]*domain.ActivityEntry, 0, len(entries))
	for _, entry := range entries {
		if matchesFilter(entry, filter) {
			out = append(out, entry)
		}
	}
	return out, warnings, nil
}

// scannedBridge rebuilds the domain.ScannedBridge a row was cached from, for the re-check loop
// in GetActivity (mirrors ActivityCache re-wrapping its cached entries the same way)
func (row *activityBridgeRow) scannedBridge() (*domain.ScannedBridge, error) {
	var data activityBridgeData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return nil, fmt.Errorf("decoding activity_bridge row %s: %w", row.GlobalIndex, err)
	}
	return &domain.ScannedBridge{Bridge: data.Bridge, NetworkID: row.OriginNetworkID}, nil
}

// bridgeRows returns every activity_bridge row cached for addr
func (s *sqliteActivityStore) bridgeRows(addr string) ([]*activityBridgeRow, error) {
	var rows []*activityBridgeRow
	if err := meddler.QueryAll(s.db, &rows, "SELECT * FROM activity_bridge WHERE from_address = ?", addr); err != nil {
		return nil, err
	}
	return rows, nil
}

// sweepIdle would forget every activity_address (and, via ON DELETE CASCADE, its activity_bridge
// rows) whose last_access is before now-idleTimeout — the same idle-eviction sweep-on-every-call
// ActivityCache.addrCache does over its in-memory map. Deliberately a no-op for now: unlike the
// in-memory adapter, this store never deletes a persisted row on its own — pruning/retention
// stays an in-memory-only concern until a DB-side retention policy is decided (see issue #1822,
// and sqliteRegistry.PruneTerminal/PruneIdle for the same call on the tracker side). Both tables
// are left to grow unbounded for the time being; this is a conscious, temporary trade-off
func (s *sqliteActivityStore) sweepIdle(time.Time) error {
	return nil
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

// ensureAddress makes sure addr has a current-schema row, stamping last_access with now: it
// creates one if missing, or resets scan_state (see networkScanState) if the existing one is
// stale — the same "discard rather than misinterpret" rule tracked_bridge follows
func (s *sqliteActivityStore) ensureAddress(addr string, now time.Time) error {
	row, err := s.selectAddressRow(addr)
	if err != nil && !errors.Is(err, aggkitdb.ErrNotFound) {
		return err
	}
	if err == nil && !row.staleSchema() {
		_, err := s.db.Exec("UPDATE activity_address SET last_access = ? WHERE from_address = ?", now.Unix(), addr)
		return err
	}

	empty, marshalErr := json.Marshal(map[uint32]networkScanState{})
	if marshalErr != nil {
		return marshalErr
	}
	_, err = s.db.Exec(
		`INSERT INTO activity_address (from_address, schema_version, updated_at, last_access, scan_state)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (from_address) DO UPDATE SET
			schema_version = excluded.schema_version, updated_at = excluded.updated_at,
			last_access = excluded.last_access, scan_state = excluded.scan_state`,
		addr, activitySchemaVersion, now.Unix(), now.Unix(), empty)
	return err
}

// recordScanWarnings merges warnings into addr's scan_state (see networkScanState); a no-op
// when warnings is empty, so a fully successful scan never rewrites scan_state
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
// settled — in which case existing is returned untouched. Mirrors ActivityCache.upsert
func (s *sqliteActivityStore) upsert(
	ctx context.Context, addr string, item *domain.ScannedBridge, includeTracking bool, filter types.ActivityFilter,
) (*domain.ActivityEntry, error) {
	key := item.Bridge.GlobalIndex.String()

	existingRow, err := s.selectBridgeRow(key)
	if err != nil && !errors.Is(err, aggkitdb.ErrNotFound) {
		return nil, err
	}
	var existing *domain.ActivityEntry
	if err == nil && !existingRow.staleSchema() {
		existing, err = existingRow.entry()
		if err != nil {
			return nil, err
		}
		if settled(existing) {
			return existing, nil
		}
	}

	entry := s.refresh(ctx, item, existing, includeTracking, filter)
	if err := s.saveBridgeRow(addr, item, entry); err != nil {
		return nil, err
	}
	return entry, nil
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
	ctx context.Context, item *domain.ScannedBridge, existing *domain.ActivityEntry,
	includeTracking bool, filter types.ActivityFilter,
) *domain.ActivityEntry {
	entry := &domain.ActivityEntry{Bridge: item.Bridge, BridgeNetworkID: item.NetworkID}
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
		if skipsClaimInfo(filter) {
			return entry
		}
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

// skipsClaimInfo reports whether filter excludes a claimed bridge from its result, making the
// destination bridge service's claim record unnecessary to fetch right now. Mirrors
// ActivityCache's skipsClaimInfo
func skipsClaimInfo(filter types.ActivityFilter) bool {
	return filter == types.ActivityFilterPending ||
		filter == types.ActivityFilterReadyToClaim ||
		filter == types.ActivityFilterError
}

// saveBridgeRow upserts entry as addr's row for item's global index
func (s *sqliteActivityStore) saveBridgeRow(
	addr string, item *domain.ScannedBridge, entry *domain.ActivityEntry,
) error {
	data, err := json.Marshal(activityBridgeData{Bridge: entry.Bridge, Claim: entry.Claim, Errors: entry.Errors})
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
		item.Bridge.GlobalIndex.String(), addr, activitySchemaVersion, item.NetworkID, item.Bridge.DestinationNetwork,
		string(item.Bridge.TxHash), entry.ClaimStatus.String(), entry.TrackerClaimStatus.String(),
		int64(item.Bridge.BlockTimestamp), entry.CreatedAt.UnixNano(), entry.UpdatedAt.UnixNano(), data)
	if err != nil {
		return fmt.Errorf("saving activity_bridge row %s: %w", item.Bridge.GlobalIndex, err)
	}
	return nil
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
