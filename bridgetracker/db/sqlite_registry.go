// Package db holds everything SQLite-related for the bridge tracker: the sqliteRegistry
// adapter of domain.SupervisedRegistry and its migrations (see migrations/), mirroring how
// aggsender/db holds its own component's SQLite storage. It depends only on domain/types, never
// on the bridgetracker package itself, to avoid an import cycle (bridgetracker constructs this
// package's registry, see NewSQLiteRegistry's callers).
package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/agglayer/aggkit/bridgetracker/db/migrations"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

// defaultMaxTrackedBridges mirrors bridgetracker.DefaultMaxTrackedBridges; kept in sync by hand
// (like the [Tracker]/EngineConfig default pairs in bridgetracker/config.go) since this package
// cannot import bridgetracker itself — see the package doc
const defaultMaxTrackedBridges = 100_000

// triggerBufferSize bounds the backlog of newly registered ids awaiting the engine's immediate
// attention (see sqliteRegistry.trigger), mirroring bridgetracker's own memoryRegistry. A full
// buffer never blocks registration or loses the entry itself — a dropped signal just leaves
// that one bridge for the next regular poll tick, exactly as if GetAndAwait had not signaled it
const triggerBufferSize = 256

// trackedBridgeSchemaVersion is the version of the Go shapes (domain.TrackingBridgeTx,
// domain.BridgeStepPath) serialized into trackedBridgeRow.Data. Bump it whenever a change to
// either shape would break decoding an older row: a row whose SchemaVersion does not match is
// treated as a cache miss (discarded, re-resolved from scratch, see sqliteRegistry.create)
// rather than risking misinterpreting stale/incompatible JSON — this is a cache, not a source
// of truth, so "discard and recompute" is always a safe fallback.
const trackedBridgeSchemaVersion = 1

// trackedBridgeRow is the tracked_bridge row shape (see migrations/bridgetracker0001.sql),
// mapped through meddler like the rest of the repo's SQLite storage
type trackedBridgeRow struct {
	NetworkID         uint32      `meddler:"network_id"`
	TxHash            common.Hash `meddler:"tx_hash,hash"`
	SchemaVersion     int         `meddler:"schema_version"`
	ClaimStatus       string      `meddler:"claim_status"`
	OriginBlockNumber uint64      `meddler:"origin_block_number"`
	OriginBlockHash   common.Hash `meddler:"origin_block_hash,hash"`
	UpdatedAt         int64       `meddler:"updated_at"`
	LastAccess        int64       `meddler:"last_access"`
	TerminalSince     int64       `meddler:"terminal_since"`
	Data              []byte      `meddler:"data"`
}

// trackedBridgeData is what trackedBridgeRow.Data holds, JSON-encoded: everything
// domain.TrackingData needs besides its id (see domain.NewTrackingData)
type trackedBridgeData struct {
	Tx    domain.TrackingBridgeTx
	Steps []domain.BridgeStepPath
}

// id reconstructs the TrackingID the row was stored under
func (row *trackedBridgeRow) id() domain.TrackingID {
	return domain.TrackingID{NetworkID: row.NetworkID, TxHash: row.TxHash}
}

// decode unmarshals Data into its tx/steps parts
func (row *trackedBridgeRow) decode() (domain.TrackingBridgeTx, []domain.BridgeStepPath, error) {
	var data trackedBridgeData
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return domain.TrackingBridgeTx{}, nil, fmt.Errorf("decoding tracked_bridge row for %s: %w", row.id(), err)
	}
	return data.Tx, data.Steps, nil
}

// trackingData decodes Data and rebuilds the domain.TrackingData it represents; TrackingStatus,
// StepIndex, etc. stay derived in memory on every call, exactly as they are for bridgetracker's
// own in-memory adapter — nothing else needs storing (see domain.TrackingData)
func (row *trackedBridgeRow) trackingData() (*domain.TrackingData, error) {
	tx, steps, err := row.decode()
	if err != nil {
		return nil, err
	}
	return domain.NewTrackingData(row.id(), tx, steps), nil
}

// staleSchema reports whether row was written by a different schema version than this binary
// knows how to decode — see trackedBridgeSchemaVersion
func (row *trackedBridgeRow) staleSchema() bool {
	return row.SchemaVersion != trackedBridgeSchemaVersion
}

// sqliteRegistry is the SQLite-backed adapter of the domain.SupervisedRegistry port: the
// tracking snapshot (trackedBridgeRow) is persisted, so the tracker survives restarts instead
// of re-resolving every bridge — and re-issuing every bridge-service/agglayer call behind it —
// from scratch each time. Active WebSocket subscriptions and the trigger signal are, like
// bridgetracker's own in-memory registry, inherently local to whichever instance holds the
// connection/goroutine: they are never persisted, only the tracking snapshot is (see
// domain.SupervisedRegistry's doc on a shared-store adapter).
//
// Safe for concurrent use.
type sqliteRegistry struct {
	db     *sql.DB
	logger aggkitcommon.Logger
	// now is the clock updated_at/last_access/terminal_since are stamped with, injectable for tests
	now func() time.Time

	// maxEntries bounds how many distinct bridges can be registered at once, mirroring
	// bridgetracker's own in-memory registry
	maxEntries int

	// countMu guards numEntries, the in-memory mirror of tracked_bridge's row count: checking
	// capacity via a full-table COUNT(*) on every registration would defeat the point of a
	// cache meant to be fast, so it is tracked here instead and adjusted on every insert. It
	// currently only ever grows: PruneTerminal/PruneIdle are no-ops for now (rows are never
	// deleted, see their doc), so once maxEntries is reached this store stops accepting new
	// registrations for good — a known, temporary trade-off while the DB-side retention policy
	// is still undecided (see issue #1822), not an oversight
	countMu    sync.Mutex
	numEntries int

	// trigger carries the ids of freshly registered bridges out to the tracking engine,
	// exactly like bridgetracker's own in-memory registry
	trigger chan domain.TrackingID

	// subMu guards subscribers, and also serializes create's "insert row, then register the
	// caller as a subscriber, then signal the trigger" ordering in GetAndAwait/Subscribe — so a
	// caller can never miss the update its own registration provokes (see create)
	subMu       sync.Mutex
	subscribers map[domain.TrackingID]map[chan *domain.TrackingData]struct{}
}

// compile-time check: the SQLite adapter fulfils the full port
var _ domain.SupervisedRegistry = (*sqliteRegistry)(nil)

// NewSQLiteRegistry returns a domain.SupervisedRegistry backed by a SQLite database at dbPath,
// creating the file and running its migrations if it does not exist yet. maxEntries <= 0 falls
// back to defaultMaxTrackedBridges, exactly like bridgetracker.NewMemoryRegistry
func NewSQLiteRegistry(dbPath string, maxEntries int, logger aggkitcommon.Logger) (domain.SupervisedRegistry, error) {
	if maxEntries <= 0 {
		maxEntries = defaultMaxTrackedBridges
	}
	if err := migrations.RunMigrations(dbPath); err != nil {
		return nil, fmt.Errorf("running bridgetracker migrations on %s: %w", dbPath, err)
	}
	sqlDB, err := aggkitdb.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening bridgetracker db %s: %w", dbPath, err)
	}

	var numEntries int
	if err := sqlDB.QueryRow("SELECT COUNT(*) FROM tracked_bridge").Scan(&numEntries); err != nil {
		return nil, fmt.Errorf("counting tracked_bridge rows: %w", err)
	}

	return &sqliteRegistry{
		db:          sqlDB,
		logger:      logger,
		now:         time.Now,
		maxEntries:  maxEntries,
		numEntries:  numEntries,
		trigger:     make(chan domain.TrackingID, triggerBufferSize),
		subscribers: make(map[domain.TrackingID]map[chan *domain.TrackingData]struct{}),
	}, nil
}

// Close releases the underlying DB connection. Not part of domain.SupervisedRegistry: callers
// that construct a sqliteRegistry via NewSQLiteRegistry own the returned value's lifetime and
// should call this on shutdown, the same way any other *sql.DB owner would
func (r *sqliteRegistry) Close() error {
	return r.db.Close()
}

// selectRow returns the stored row for id, or db.ErrNotFound if there is none — including a row
// whose staleSchema() is true, which is reported the same as if it did not exist at all (see
// trackedBridgeSchemaVersion)
func (r *sqliteRegistry) selectRow(id domain.TrackingID) (*trackedBridgeRow, error) {
	var row trackedBridgeRow
	err := meddler.QueryRow(r.db, &row,
		"SELECT * FROM tracked_bridge WHERE network_id = ? AND tx_hash = ?", id.NetworkID, id.TxHash.Hex())
	if err != nil {
		return nil, aggkitdb.ReturnErrNotFound(err)
	}
	if row.staleSchema() {
		return nil, aggkitdb.ErrNotFound
	}
	return &row, nil
}

// freshRow builds the (Registered, nil BridgeStatus) row a fresh registration starts as
func (r *sqliteRegistry) freshRow(id domain.TrackingID, now time.Time) (*trackedBridgeRow, error) {
	data, err := json.Marshal(trackedBridgeData{})
	if err != nil {
		return nil, err
	}
	tracking := domain.NewTrackingData(id, domain.TrackingBridgeTx{}, nil)
	return &trackedBridgeRow{
		NetworkID:     id.NetworkID,
		TxHash:        id.TxHash,
		SchemaVersion: trackedBridgeSchemaVersion,
		ClaimStatus:   tracking.ClaimStatus().String(),
		UpdatedAt:     now.Unix(),
		LastAccess:    now.Unix(),
		Data:          data,
	}, nil
}

// getOrCreate returns the row for id, registering a fresh one — via create — if it is missing
// or stale, unless createIfNotExists is false, in which case a missing/stale row reports
// domain.ErrTrackingNotFound. created reports whether this call is the one that (re)registered
// it, so callers know whether to signal the trigger
func (r *sqliteRegistry) getOrCreate(
	id domain.TrackingID, createIfNotExists bool,
) (row *trackedBridgeRow, created bool, err error) {
	row, err = r.selectRow(id)
	if err == nil {
		return row, false, nil
	}
	if !errors.Is(err, aggkitdb.ErrNotFound) {
		return nil, false, err
	}
	if !createIfNotExists {
		return nil, false, domain.ErrTrackingNotFound
	}
	row, err = r.create(id)
	if err != nil {
		return nil, false, err
	}
	return row, true, nil
}

// create registers a fresh row for id, or domain.ErrRegistryFull if the registry is already at
// maxEntries. Reaching the cap never evicts an existing entry to make room, whether idle or
// actively watched — PruneTerminal/PruneIdle are what keep the registry under the cap during
// normal operation, and a request that would exceed it is simply rejected. A stale row
// (staleSchema) is overwritten in place instead of inserted as a new one, so it never counts
// twice against maxEntries
func (r *sqliteRegistry) create(id domain.TrackingID) (*trackedBridgeRow, error) {
	now := r.now()
	row, err := r.freshRow(id, now)
	if err != nil {
		return nil, err
	}

	r.countMu.Lock()
	if r.numEntries >= r.maxEntries {
		r.countMu.Unlock()
		return nil, domain.ErrRegistryFull
	}
	_, insertErr := r.db.Exec(
		`INSERT INTO tracked_bridge
			(network_id, tx_hash, schema_version, claim_status,
			 origin_block_number, origin_block_hash, updated_at, last_access, terminal_since, data)
		 VALUES (?, ?, ?, ?, 0, ?, ?, ?, 0, ?)`,
		row.NetworkID, row.TxHash.Hex(), row.SchemaVersion, row.ClaimStatus,
		common.Hash{}.Hex(), row.UpdatedAt, row.LastAccess, row.Data)
	if insertErr == nil {
		r.numEntries++
		r.countMu.Unlock()
		return row, nil
	}
	r.countMu.Unlock()

	// Lost the race against a concurrent create for the same id, or overwriting a stale row:
	// either way the row already exists at this point, just possibly with an outdated schema
	existing, selErr := r.selectRowIgnoringSchema(id)
	if selErr != nil {
		return nil, fmt.Errorf("creating tracked_bridge row for %s: %w", id, insertErr)
	}
	if !existing.staleSchema() {
		// a genuine concurrent creation beat this one: return its row, not a fresh one
		return existing, nil
	}
	if _, err := r.db.Exec(
		`UPDATE tracked_bridge SET
			schema_version = ?, claim_status = ?, origin_block_number = 0, origin_block_hash = ?,
			updated_at = ?, last_access = ?, terminal_since = 0, data = ?
		 WHERE network_id = ? AND tx_hash = ?`,
		row.SchemaVersion, row.ClaimStatus, common.Hash{}.Hex(), row.UpdatedAt, row.LastAccess, row.Data,
		id.NetworkID, id.TxHash.Hex(),
	); err != nil {
		return nil, fmt.Errorf("replacing stale tracked_bridge row for %s: %w", id, err)
	}
	return row, nil
}

// selectRowIgnoringSchema is selectRow without the staleSchema check, used by create to tell a
// stale row apart from a genuinely concurrent creation
func (r *sqliteRegistry) selectRowIgnoringSchema(id domain.TrackingID) (*trackedBridgeRow, error) {
	var row trackedBridgeRow
	err := meddler.QueryRow(r.db, &row,
		"SELECT * FROM tracked_bridge WHERE network_id = ? AND tx_hash = ?", id.NetworkID, id.TxHash.Hex())
	if err != nil {
		return nil, aggkitdb.ReturnErrNotFound(err)
	}
	return &row, nil
}

// touchLastAccess stamps last_access with now; failures are logged, not propagated, mirroring
// how little Get callers can do about a bookkeeping write failing when the read itself succeeded
func (r *sqliteRegistry) touchLastAccess(id domain.TrackingID, now time.Time) {
	if _, err := r.db.Exec(
		"UPDATE tracked_bridge SET last_access = ? WHERE network_id = ? AND tx_hash = ?",
		now.Unix(), id.NetworkID, id.TxHash.Hex(),
	); err != nil {
		r.logger.Warnf("bridgetracker: touching last_access of %s: %v", id, err)
	}
}

// signalTrigger notifies the tracking engine that id was just (re)registered. It never blocks:
// a full buffer just means this particular id waits for the next regular poll tick like before
func (r *sqliteRegistry) signalTrigger(id domain.TrackingID) {
	select {
	case r.trigger <- id:
	default:
	}
}

// Get implements domain.SupervisedStore
func (r *sqliteRegistry) Get(id domain.TrackingID, createIfNotExists bool) (*domain.TrackingData, error) {
	row, created, err := r.getOrCreate(id, createIfNotExists)
	if err != nil {
		return nil, err
	}
	if created {
		r.signalTrigger(id)
	} else {
		r.touchLastAccess(id, r.now())
	}
	return row.trackingData()
}

// GetAndAwait implements domain.SupervisedStore. On an already-registered id it behaves exactly
// like Get(id, true): no trigger, no wait. On a newly created id it registers itself as a
// subscriber before signaling the trigger — under subMu, so the update the trigger provokes
// cannot be delivered (and dropped, latest-value semantics) before this subscription exists —
// then blocks up to timeout for that first update, falling back to the freshly created row's
// snapshot if timeout elapses first. Unlike the in-memory adapter, the returned snapshot on a
// timeout is never stale mid-resolution: it is simply the just-created (Registered) row, since
// nothing else could have written to it yet without also delivering an update this call would
// have received
func (r *sqliteRegistry) GetAndAwait(id domain.TrackingID, timeout time.Duration) (*domain.TrackingData, error) {
	row, created, err := r.getOrCreate(id, true)
	if err != nil {
		return nil, err
	}
	if !created || timeout <= 0 {
		if !created {
			r.touchLastAccess(id, r.now())
		} else {
			r.signalTrigger(id)
		}
		return row.trackingData()
	}

	r.subMu.Lock()
	ch := make(chan *domain.TrackingData, 1)
	r.addSubscriberLocked(id, ch)
	r.subMu.Unlock()
	r.signalTrigger(id)

	defer r.removeSubscriber(id, ch)

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case updated := <-ch:
		return updated, nil
	case <-timer.C:
		return row.trackingData()
	}
}

// Subscribe implements domain.StatusNotifier. The channel has a buffer of one and updates are
// coalesced: if the subscriber is slow, older pending updates are replaced by the newest one —
// same semantics as the in-memory adapter
func (r *sqliteRegistry) Subscribe(id domain.TrackingID) (<-chan *domain.TrackingData, func(), error) {
	_, created, err := r.getOrCreate(id, true)
	if err != nil {
		return nil, nil, err
	}

	r.subMu.Lock()
	ch := make(chan *domain.TrackingData, 1)
	r.addSubscriberLocked(id, ch)
	r.subMu.Unlock()

	if created {
		r.signalTrigger(id)
	} else {
		r.touchLastAccess(id, r.now())
	}

	unsubscribe := func() { r.removeSubscriber(id, ch) }
	return ch, unsubscribe, nil
}

// addSubscriberLocked registers ch as a subscriber of id. Callers must hold subMu
func (r *sqliteRegistry) addSubscriberLocked(id domain.TrackingID, ch chan *domain.TrackingData) {
	subs, ok := r.subscribers[id]
	if !ok {
		subs = make(map[chan *domain.TrackingData]struct{})
		r.subscribers[id] = subs
	}
	subs[ch] = struct{}{}
}

// removeSubscriber unregisters ch, dropping id's subscriber set entirely once empty
func (r *sqliteRegistry) removeSubscriber(id domain.TrackingID, ch chan *domain.TrackingData) {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	subs, ok := r.subscribers[id]
	if !ok {
		return
	}
	delete(subs, ch)
	if len(subs) == 0 {
		delete(r.subscribers, id)
	}
}

// notify delivers update to every subscriber of id with latest-value semantics: a pending
// (unread) update is dropped in favor of the new one, so a slow subscriber always receives the
// most recent snapshot without blocking the caller
func (r *sqliteRegistry) notify(id domain.TrackingID, update *domain.TrackingData) {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	for ch := range r.subscribers[id] {
		select {
		case ch <- update:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- update:
			default:
			}
		}
	}
}

// UpdateTrackingBridgeTx implements domain.SupervisedStore, notifying every subscriber of id.
// Recomputes claim_status and, once terminal_since is first stamped, leaves it untouched
// forever after — see domain.TrackingData.IsTerminal
func (r *sqliteRegistry) UpdateTrackingBridgeTx(id domain.TrackingID, tx domain.TrackingBridgeTx) error {
	row, err := r.selectRow(id)
	if err != nil {
		return domain.ErrTrackingNotFound
	}

	_, steps, err := row.decode()
	if err != nil {
		return err
	}
	tracking := domain.NewTrackingData(id, tx, steps)

	data, err := json.Marshal(trackedBridgeData{Tx: tx, Steps: steps})
	if err != nil {
		return err
	}

	now := r.now()
	terminalSince := row.TerminalSince
	if terminalSince == 0 && tracking.IsTerminal() {
		terminalSince = now.Unix()
	}
	var originBlockNumber uint64
	originBlockHash := common.Hash{}
	if tx.Info != nil {
		originBlockNumber = tx.Info.BlockNumber
		originBlockHash = tx.Info.BlockHash
	}

	if _, err := r.db.Exec(
		`UPDATE tracked_bridge SET
			claim_status = ?, origin_block_number = ?, origin_block_hash = ?,
			updated_at = ?, last_access = ?, terminal_since = ?, data = ?
		 WHERE network_id = ? AND tx_hash = ?`,
		tracking.ClaimStatus().String(), originBlockNumber, originBlockHash.Hex(),
		now.Unix(), now.Unix(), terminalSince, data,
		id.NetworkID, id.TxHash.Hex(),
	); err != nil {
		return fmt.Errorf("updating tracked_bridge row for %s: %w", id, err)
	}

	r.notify(id, tracking)
	return nil
}

// UpdateTrackingStep implements domain.SupervisedStore. Unlike UpdateTrackingBridgeTx, it does
// not itself notify subscribers or recompute claim_status/terminal_since — see the interface
// doc: callers must follow a batch of step changes with an UpdateTrackingBridgeTx call, even a
// no-op one, so subscribers see exactly one consistent, fully-merged snapshot
func (r *sqliteRegistry) UpdateTrackingStep(id domain.TrackingID, stepIndex uint, step domain.BridgeStepPath) error {
	row, err := r.selectRow(id)
	if err != nil {
		return domain.ErrTrackingNotFound
	}

	tx, prevSteps, err := row.decode()
	if err != nil {
		return err
	}
	tracking := domain.NewTrackingData(id, tx, prevSteps)
	if tracking.TerminallyFailed() {
		// terminal failure is final: nothing may resurrect the bridge afterwards
		return nil
	}

	allSteps := make([]domain.BridgeStepPath, max(len(prevSteps), int(stepIndex)+1))
	copy(allSteps, prevSteps)
	allSteps[stepIndex] = step

	data, err := json.Marshal(trackedBridgeData{Tx: tx, Steps: allSteps})
	if err != nil {
		return err
	}
	now := r.now()
	if _, err := r.db.Exec(
		"UPDATE tracked_bridge SET data = ?, updated_at = ? WHERE network_id = ? AND tx_hash = ?",
		data, now.Unix(), id.NetworkID, id.TxHash.Hex(),
	); err != nil {
		return fmt.Errorf("updating tracked_bridge row for %s: %w", id, err)
	}
	return nil
}

// GetTrackerActives implements domain.SupervisedStore: snapshots of every row not yet terminal
// (terminal_since = 0), optionally filtered to one network
func (r *sqliteRegistry) GetTrackerActives(networkID *uint32) ([]*domain.TrackingData, error) {
	var rows []*trackedBridgeRow
	var err error
	if networkID != nil {
		err = meddler.QueryAll(r.db, &rows,
			"SELECT * FROM tracked_bridge WHERE terminal_since = 0 AND network_id = ?", *networkID)
	} else {
		err = meddler.QueryAll(r.db, &rows, "SELECT * FROM tracked_bridge WHERE terminal_since = 0")
	}
	if err != nil {
		return nil, fmt.Errorf("listing active tracked bridges: %w", err)
	}

	active := make([]*domain.TrackingData, 0, len(rows))
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		tracking, err := row.trackingData()
		if err != nil {
			return nil, err
		}
		active = append(active, tracking)
	}
	return active, nil
}

// GetNetworks implements domain.SupervisedStore: the networks with at least one supervised
// bridge, optionally filtered to those with at least one bridge in the given TrackingStatus.
// Like the in-memory adapter, this walks every row: TrackingStatus is derived, not a stored
// column
func (r *sqliteRegistry) GetNetworks(status *types.TrackingStatus) ([]uint32, error) {
	var rows []*trackedBridgeRow
	if err := meddler.QueryAll(r.db, &rows, "SELECT * FROM tracked_bridge"); err != nil {
		return nil, fmt.Errorf("listing tracked bridges: %w", err)
	}

	seen := make(map[uint32]struct{})
	for _, row := range rows {
		if row.staleSchema() {
			continue
		}
		if status != nil {
			tracking, err := row.trackingData()
			if err != nil {
				return nil, err
			}
			if tracking.TrackingStatus() != *status {
				continue
			}
		}
		seen[row.NetworkID] = struct{}{}
	}

	networks := make([]uint32, 0, len(seen))
	for networkID := range seen {
		networks = append(networks, networkID)
	}
	slices.Sort(networks)
	return networks, nil
}

// GetNumTracker implements domain.SupervisedStore
func (r *sqliteRegistry) GetNumTracker() int {
	r.countMu.Lock()
	defer r.countMu.Unlock()
	return r.numEntries
}

// PruneTerminal implements domain.SupervisedStore. Deliberately a no-op for now: unlike the
// in-memory adapter, this store never deletes a persisted row on its own — pruning/retention
// stays an in-memory-only concern until a DB-side retention policy is decided (see issue #1822).
// tracked_bridge is left to grow unbounded for the time being; this is a conscious, temporary
// trade-off, not an oversight
func (r *sqliteRegistry) PruneTerminal(time.Time) (int, error) {
	return 0, nil
}

// PruneIdle implements domain.SupervisedStore. Deliberately a no-op for now — see PruneTerminal
func (r *sqliteRegistry) PruneIdle(time.Time) (int, error) {
	return 0, nil
}

// Triggers implements domain.Triggerable
func (r *sqliteRegistry) Triggers() <-chan domain.TrackingID {
	return r.trigger
}
