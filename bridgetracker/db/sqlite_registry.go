// Package db holds everything SQLite-related for the bridge tracker: the sqliteRegistry
// adapter of domain.SupervisedRegistry and its migrations (see migrations/), mirroring how
// aggsender/db holds its own component's SQLite storage. It depends only on domain/types, never
// on the bridgetracker package itself, to avoid an import cycle (bridgetracker constructs this
// package's registry, see NewSQLiteRegistry's callers).
package db

import (
	"context"
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

// blockHashVerifyTimeout bounds how long selectFreshRow waits on BlockHashVerifier before giving
// up and trusting the persisted row as-is, so a slow/unreachable origin RPC endpoint never blocks
// a caller of the context-less SupervisedStore interface indefinitely
const blockHashVerifyTimeout = 3 * time.Second

// BlockHashVerifier reports the current canonical hash at blockNumber on networkID, used by
// selectFreshRow to detect a persisted row whose origin block was since reorged out from under
// it — see its doc. A nil BlockHashVerifier (see NewSQLiteRegistry) skips this check entirely:
// the same trust-what's-on-disk behavior this adapter had before reorg validation existed, and
// the same exposure the in-memory adapter already has for the far shorter window it stays alive
// without a restart.
type BlockHashVerifier interface {
	// CanonicalBlockHash returns the block hash currently canonical at blockNumber on networkID
	CanonicalBlockHash(ctx context.Context, networkID uint32, blockNumber uint64) (common.Hash, error)
}

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
	// verifier checks a persisted row's origin block hash against the chain on reload — see
	// BlockHashVerifier and selectFreshRow. nil skips the check
	verifier BlockHashVerifier

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

// compile-time check: the SQLite adapter also reports its own on-disk footprint
var _ domain.CacheStatsProvider = (*sqliteRegistry)(nil)

// NewSQLiteRegistry returns a domain.SupervisedRegistry backed by a SQLite database at dbPath,
// creating the file and running its migrations if it does not exist yet. maxEntries <= 0 falls
// back to defaultMaxTrackedBridges, exactly like bridgetracker.NewMemoryRegistry. verifier may be
// nil, which skips reorg validation on reload entirely (see BlockHashVerifier)
func NewSQLiteRegistry(
	dbPath string, maxEntries int, logger aggkitcommon.Logger, verifier BlockHashVerifier,
) (domain.SupervisedRegistry, error) {
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
		verifier:    verifier,
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

// selectFreshRow is selectRow plus a reorg check: a row whose persisted origin_block_hash no
// longer matches the origin chain's current hash at origin_block_number was accepted on a block
// since reorged out, and is reported as not-found exactly like a stale-schema row — so
// getOrCreate re-registers and re-resolves it from scratch instead of serving status for an
// orphaned deposit forever (see BlockHashVerifier and rowIsStale)
func (r *sqliteRegistry) selectFreshRow(id domain.TrackingID) (*trackedBridgeRow, error) {
	row, err := r.selectRow(id)
	if err != nil {
		return nil, err
	}
	if r.rowIsStale(id, row) {
		return nil, aggkitdb.ErrNotFound
	}
	return row, nil
}

// rowIsStale reports whether row can no longer be trusted as-is and must be discarded in favor
// of a fresh registration: either its schema is stale (see staleSchema, already filtered out of
// row by selectRow but not by selectRowIgnoringSchema), its Data no longer decodes under the
// current schema (a corrupted row left behind by e.g. a partial write or disk fault — treating
// it as a cache miss self-heals it on the next registration instead of wedging every caller
// that reads it forever), or, once resolved and with a BlockHashVerifier configured, its
// persisted origin block hash no longer matches the chain's current canonical hash at
// origin_block_number — a bridge accepted on a block since reorged out. A verifier error (e.g.
// the origin RPC briefly unreachable) is not itself evidence of a reorg, so it is logged and row
// is trusted as fresh rather than discarding the whole cache over a transient hiccup
func (r *sqliteRegistry) rowIsStale(id domain.TrackingID, row *trackedBridgeRow) bool {
	if row.staleSchema() {
		return true
	}
	if _, _, err := row.decode(); err != nil {
		r.logger.Warnf("bridgetracker: row for %s failed to decode, discarding: %v", id, err)
		return true
	}
	if r.verifier == nil || row.OriginBlockNumber == 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), blockHashVerifyTimeout)
	defer cancel()
	current, err := r.verifier.CanonicalBlockHash(ctx, id.NetworkID, row.OriginBlockNumber)
	if err != nil {
		r.logger.Warnf("bridgetracker: verifying origin block %d of %s is still canonical: %v",
			row.OriginBlockNumber, id, err)
		return false
	}
	if current == row.OriginBlockHash {
		return false
	}
	r.logger.Infof("bridgetracker: origin block %d of %s changed hash (%s -> %s), discarding stale row",
		row.OriginBlockNumber, id, row.OriginBlockHash, current)
	return true
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
	row, err = r.selectFreshRow(id)
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
// normal operation, and a request that would exceed it is simply rejected. A stale row (see
// rowIsStale: stale schema, corrupted data, or reorged-out origin block) is overwritten in place
// instead of inserted as a new one, so it never counts twice against maxEntries — and, crucially,
// this overwrite is checked for *before* the capacity gate below: id already has a row on disk
// either way, so replacing it can never be the thing that pushes the registry over maxEntries,
// and must not be blocked by a cap that is otherwise full of unrelated entries
func (r *sqliteRegistry) create(id domain.TrackingID) (*trackedBridgeRow, error) {
	now := r.now()
	row, err := r.freshRow(id, now)
	if err != nil {
		return nil, err
	}

	if existing, selErr := r.selectRowIgnoringSchema(id); selErr == nil {
		if !r.rowIsStale(id, existing) {
			// a row already exists and is still trustworthy: nothing to create
			return existing, nil
		}
		if err := r.overwriteStaleRow(id, row); err != nil {
			return nil, err
		}
		return row, nil
	} else if !errors.Is(selErr, aggkitdb.ErrNotFound) {
		return nil, fmt.Errorf("checking for an existing tracked_bridge row for %s: %w", id, selErr)
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

	// Lost the race against a concurrent create for the same id: the row already exists now
	existing, selErr := r.selectRowIgnoringSchema(id)
	if selErr != nil {
		return nil, fmt.Errorf("creating tracked_bridge row for %s: %w", id, insertErr)
	}
	if !r.rowIsStale(id, existing) {
		// a genuine concurrent creation beat this one: return its row, not a fresh one
		return existing, nil
	}
	if err := r.overwriteStaleRow(id, row); err != nil {
		return nil, err
	}
	return row, nil
}

// overwriteStaleRow replaces id's existing (stale, per rowIsStale) row in place with row, without
// touching numEntries — the row already counted against maxEntries once, and continues to
func (r *sqliteRegistry) overwriteStaleRow(id domain.TrackingID, row *trackedBridgeRow) error {
	if _, err := r.db.Exec(
		`UPDATE tracked_bridge SET
			schema_version = ?, claim_status = ?, origin_block_number = 0, origin_block_hash = ?,
			updated_at = ?, last_access = ?, terminal_since = 0, data = ?
		 WHERE network_id = ? AND tx_hash = ?`,
		row.SchemaVersion, row.ClaimStatus, common.Hash{}.Hex(), row.UpdatedAt, row.LastAccess, row.Data,
		id.NetworkID, id.TxHash.Hex(),
	); err != nil {
		return fmt.Errorf("replacing stale tracked_bridge row for %s: %w", id, err)
	}
	return nil
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
		if errors.Is(err, aggkitdb.ErrNotFound) {
			return domain.ErrTrackingNotFound
		}
		return fmt.Errorf("looking up tracked_bridge row for %s: %w", id, err)
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

	// last_access is deliberately left untouched here: it is the idle-eviction anchor (see
	// touchLastAccess), stamped only by a caller actually reading the row (Get/GetAndAwait/
	// Subscribe). The engine's own tick writes every active bridge on every poll, so bumping it
	// here would mean last_access could never age — defeating idle eviction entirely once it is
	// implemented on top of this column (see PruneIdle)
	if _, err := r.db.Exec(
		`UPDATE tracked_bridge SET
			claim_status = ?, origin_block_number = ?, origin_block_hash = ?,
			updated_at = ?, terminal_since = ?, data = ?
		 WHERE network_id = ? AND tx_hash = ?`,
		tracking.ClaimStatus().String(), originBlockNumber, originBlockHash.Hex(),
		now.Unix(), terminalSince, data,
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
		if errors.Is(err, aggkitdb.ErrNotFound) {
			return domain.ErrTrackingNotFound
		}
		return fmt.Errorf("looking up tracked_bridge row for %s: %w", id, err)
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
// (terminal_since = 0), optionally filtered to one network. A row that fails to decode (stale
// schema, or corrupted data — see rowIsStale) is skipped and logged rather than failing the
// whole call: the engine's poll tick calls this every tick, so one bad row must never wedge
// resolution for every other bridge forever
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
			r.logger.Warnf("bridgetracker: skipping undecodable tracked_bridge row for %s: %v", row.id(), err)
			continue
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
				r.logger.Warnf("bridgetracker: skipping undecodable tracked_bridge row for %s: %v", row.id(), err)
				continue
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

// Forget implements domain.SupervisedStore: unlike PruneTerminal/PruneIdle (deliberately no-ops
// for now, see their doc), this is an explicit, immediate discard of id's row regardless of its
// terminal/idle state — the tracker endpoints' ?flush_cache=true parameter uses it, so it must
// actually take effect rather than defer to the still-undecided DB-side retention policy. Safe to
// call for an id that is not currently registered (no-op); a failure is logged, not propagated,
// same as touchLastAccess's own best-effort bookkeeping writes
func (r *sqliteRegistry) Forget(id domain.TrackingID) {
	res, err := r.db.Exec("DELETE FROM tracked_bridge WHERE network_id = ? AND tx_hash = ?", id.NetworkID, id.TxHash.Hex())
	if err != nil {
		r.logger.Warnf("bridgetracker: forgetting %s: %v", id, err)
		return
	}
	affected, err := res.RowsAffected()
	if err != nil || affected == 0 {
		return
	}
	r.countMu.Lock()
	r.numEntries -= int(affected)
	r.countMu.Unlock()
}

// Triggers implements domain.Triggerable
func (r *sqliteRegistry) Triggers() <-chan domain.TrackingID {
	return r.trigger
}

// CacheStats implements domain.CacheStatsProvider: the SQLite file's current size, used by
// GET /health to report how much space this registry's cache is using on disk
func (r *sqliteRegistry) CacheStats() (domain.CacheStats, error) {
	size, err := sqliteFileSize(r.db)
	if err != nil {
		return domain.CacheStats{}, fmt.Errorf("reading tracked_bridge cache size: %w", err)
	}
	return domain.CacheStats{SizeBytes: size}, nil
}

// sqliteFileSize computes db's current on-disk size via PRAGMA page_count * page_size — shared
// by sqliteRegistry.CacheStats and sqliteActivityStore.CacheStats, since either may be asked
// for the size of what is, in the common case, the very same file (see NewSQLiteActivityStore)
func sqliteFileSize(db *sql.DB) (int64, error) {
	var pageCount, pageSize int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return 0, fmt.Errorf("querying page_count: %w", err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return 0, fmt.Errorf("querying page_size: %w", err)
	}
	return pageCount * pageSize, nil
}
