package storage

import (
	"context"
	"database/sql"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	"github.com/agglayer/aggkit/autoclaim/storage/migrations"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) (*Storage, *sql.DB) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")
	storage, err := NewStandalone(logger.GetDefaultLogger(), dbPath, 30*time.Second)
	require.NoError(t, err)

	return storage, storage.database
}

func makeRequest(
	depositCount uint32,
	destinationNetwork uint32,
	status autoclaimtypes.RequestStatus,
) autoclaimtypes.AutoClaimRequest {
	now := time.Date(2026, 6, 3, 12, 0, int(depositCount), 0, time.UTC)
	bridge := autoclaimtypes.BridgeExit{
		BlockNum:           100 + uint64(depositCount),
		BlockPos:           uint64(depositCount),
		TxHash:             common.BigToHash(big.NewInt(int64(depositCount))),
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: destinationNetwork,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(1000 + int64(depositCount)),
		Metadata:           []byte{byte(depositCount)},
		DepositCount:       depositCount,
		TxnSender:          common.HexToAddress("0x3000000000000000000000000000000000000003"),
		ToAddress:          common.HexToAddress("0x4000000000000000000000000000000000000004"),
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, depositCount),
	}

	return autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, bridge.DepositCount),
		Status:      status,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		MaxRetries:  4,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func enqueueRequest(t *testing.T, ctx context.Context, storage *Storage, request autoclaimtypes.AutoClaimRequest) {
	t.Helper()

	_, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
}

func TestMigrationCreatesExpectedSchema(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()

	for _, table := range []string{
		"autoclaim_request",
		"autoclaim_transaction_attempt",
		"autoclaim_bridge_cursor",
	} {
		var name string
		err := database.QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&name)
		require.NoError(t, err)
		require.Equal(t, table, name)
	}
}

func TestBridgeCursorPersistence(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	cursor := autoclaimtypes.BridgeCursor{
		FromBlock: 10,
		ToBlock:   20,
		BlockNum:  20,
		BlockPos:  3,
	}
	require.NoError(t, storage.SaveBridgeCursor(ctx, "l1-to-l2", cursor, time.Now().UTC()))

	stored, found, err := storage.GetBridgeCursor(ctx, "l1-to-l2")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursor, *stored)

	cursor.ToBlock = 25
	cursor.BlockNum = 25
	require.NoError(t, storage.SaveBridgeCursor(ctx, "l1-to-l2", cursor, time.Now().UTC()))

	stored, found, err = storage.GetBridgeCursor(ctx, "l1-to-l2")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursor, *stored)

	_, found, err = storage.GetBridgeCursor(ctx, "missing")
	require.NoError(t, err)
	require.False(t, found)
}

// --- issue #1651: per-destination LER cursor storage ---
//
// TestLERCursorPersistence (source-only keying) was removed here: autoclaim0003 re-keys the durable
// LER cursor from source_network alone to (source_network, destination_network), so
// Storage.GetLERCursor/SaveLERCursor no longer have a source-only overload to test. Its scenarios
// (round-trip, independent cursors, "not found") are fully superseded by
// TestLERCursorPersistencePerDestination below, one level more specific (per (source, destination) pair
// instead of per source). The update-path (upsert-overwrite) scenario it also covered is restored by
// TestSaveLERCursorUpdatesExistingPair further down.
//
// TestLERCursorPersistencePerDestination pins down the exact per-pair independence
// Storage.GetLERCursor/SaveLERCursor provide: the durable LER cursor is keyed by
// (source_network, destination_network), and autoclaimtypes.LERCursor carries DestinationNetwork.
func TestLERCursorPersistencePerDestination(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	const sourceNetwork uint32 = 2
	const destA uint32 = 20
	const destB uint32 = 21

	cursorA := autoclaimtypes.LERCursor{
		SourceNetwork: sourceNetwork, DestinationNetwork: destA,
		LastLER: common.HexToHash("0xaaaa"), LastVerifyBlockNum: 100,
	}
	require.NoError(t, storage.SaveLERCursor(ctx, sourceNetwork, destA, cursorA, time.Now().UTC()))

	storedA, found, err := storage.GetLERCursor(ctx, sourceNetwork, destA)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursorA, *storedA)

	// Destination B is newly added on the SAME source: it must not inherit A's already-advanced
	// cursor. This is exactly the issue #1651 permanent-loss bug expressed at the storage layer.
	_, found, err = storage.GetLERCursor(ctx, sourceNetwork, destB)
	require.NoError(t, err)
	require.False(t, found,
		"a newly added destination must not inherit another destination's cursor on the same source")

	cursorB := autoclaimtypes.LERCursor{
		SourceNetwork: sourceNetwork, DestinationNetwork: destB,
		LastLER: common.HexToHash("0xbbbb"), LastVerifyBlockNum: 5,
	}
	require.NoError(t, storage.SaveLERCursor(ctx, sourceNetwork, destB, cursorB, time.Now().UTC()))

	storedB, found, err := storage.GetLERCursor(ctx, sourceNetwork, destB)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursorB, *storedB)

	// A's cursor is unaffected by B's arrival and progress.
	storedA, found, err = storage.GetLERCursor(ctx, sourceNetwork, destA)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, cursorA, *storedA)

	_, found, err = storage.GetLERCursor(ctx, sourceNetwork, uint32(99))
	require.NoError(t, err)
	require.False(t, found)
}

// TestSaveLERCursorUpdatesExistingPair proves SaveLERCursor's UPSERT-update branch: saving the same
// (source, destination) pair a second time with different values must overwrite the stored cursor,
// not silently keep the first one. This coverage was lost when the old, source-keyed
// TestLERCursorPersistence was deleted in favor of TestLERCursorPersistencePerDestination above, which
// only ever saves each pair once; degrading SaveLERCursor's "ON CONFLICT(...) DO UPDATE SET ..." to
// "DO NOTHING" leaves the rest of the suite green with no other test noticing.
func TestSaveLERCursorUpdatesExistingPair(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	const sourceNetwork uint32 = 3
	const destinationNetwork uint32 = 30

	first := autoclaimtypes.LERCursor{
		SourceNetwork: sourceNetwork, DestinationNetwork: destinationNetwork,
		LastLER: common.HexToHash("0x1111"), LastVerifyBlockNum: 100,
	}
	require.NoError(t, storage.SaveLERCursor(ctx, sourceNetwork, destinationNetwork, first, time.Now().UTC()))

	second := autoclaimtypes.LERCursor{
		SourceNetwork: sourceNetwork, DestinationNetwork: destinationNetwork,
		LastLER: common.HexToHash("0x2222"), LastVerifyBlockNum: 200,
	}
	require.NoError(t, storage.SaveLERCursor(ctx, sourceNetwork, destinationNetwork, second, time.Now().UTC()))

	stored, found, err := storage.GetLERCursor(ctx, sourceNetwork, destinationNetwork)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, second.LastLER, stored.LastLER,
		"a second save of the same pair must update the stored LER, not keep the first one")
	require.Equal(t, second.LastVerifyBlockNum, stored.LastVerifyBlockNum,
		"a second save of the same pair must update the stored verify block, not keep the first one")
}

// TestSeedLERCursorsFromLegacy pins down Storage.SeedLERCursorsFromLegacy's legacy-seed contract: a
// pre-autoclaim0003 per-source row (parked in autoclaim_ler_cursor_legacy by the autoclaim0003
// migration's Up step) is fanned out to the destinations configured when the source is first
// observed, the legacy row is then deleted, and a second seed attempt is a no-op (idempotent) --
// while a destination added AFTER the seed already ran gets no legacy row to inherit and must start
// from its own baseline (the actual issue #1651 fix).
func TestSeedLERCursorsFromLegacy(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	const sourceNetwork uint32 = 7
	legacyLER := common.HexToHash("0xdead")
	const legacyVerifyBlock uint64 = 555

	// Simulate a pre-autoclaim0003 database: a per-source row parked in the legacy table by the 0003
	// migration's Up step.
	_, err := database.Exec(`
		INSERT INTO autoclaim_ler_cursor_legacy (source_network, last_ler, last_verify_block_num, updated_at)
		VALUES (?, ?, ?, ?)`,
		sourceNetwork, legacyLER.Hex(), legacyVerifyBlock, now,
	)
	require.NoError(t, err)

	destinations := []uint32{20, 21}
	seeded, err := storage.SeedLERCursorsFromLegacy(ctx, sourceNetwork, destinations, now)
	require.NoError(t, err)
	require.True(t, seeded)

	for _, destination := range destinations {
		cursor, found, err := storage.GetLERCursor(ctx, sourceNetwork, destination)
		require.NoError(t, err)
		require.True(t, found, "destination %d must be seeded from the parked legacy row", destination)
		require.Equal(t, legacyLER, cursor.LastLER)
		require.Equal(t, legacyVerifyBlock, cursor.LastVerifyBlockNum)
	}

	// The legacy row is consumed: it must not be seeded again.
	var legacyCount int
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM autoclaim_ler_cursor_legacy WHERE source_network = ?", sourceNetwork,
	).Scan(&legacyCount))
	require.Equal(t, 0, legacyCount)

	// Idempotent: a second attempt finds no legacy row left and does nothing.
	seededAgain, err := storage.SeedLERCursorsFromLegacy(ctx, sourceNetwork, destinations, now)
	require.NoError(t, err)
	require.False(t, seededAgain, "seeding must be a no-op once the legacy row has been consumed")

	// A destination added AFTER the seed already ran has no legacy row to inherit from: this is the
	// issue #1651 fix in action -- it starts from its own baseline instead of a stale shared cursor.
	_, found, err := storage.GetLERCursor(ctx, sourceNetwork, uint32(22))
	require.NoError(t, err)
	require.False(t, found,
		"a destination added after the legacy seed ran must not inherit the seeded destinations' LER")
}

// TestSeedLERCursorsFromLegacy_NoWriteLockWhenNothingToSeed proves the F4 fix: when there is no
// legacy row to seed (the common case on every poll, for every source, once a deployment has fully
// migrated), SeedLERCursorsFromLegacy must not open a write transaction at all -- db.NewSQLiteDB
// sets _txlock=immediate, so a BeginTx issues "BEGIN IMMEDIATE" and takes SQLite's single write
// lock even for a transaction that only runs one SELECT and commits a no-op.
//
// This is proven, not merely asserted, by holding an open write transaction on a second connection
// to the same database file for the whole test and giving the storage under test a short
// dbQueryTimeout: a lock-free read-only probe is unaffected by the other connection's write lock
// (WAL readers are never blocked by a writer), so it returns quickly regardless; a BeginTx call
// would instead block behind that write lock until either it is released or dbQueryTimeout's
// context deadline fires, whichever comes first -- so this test fails fast (within dbQueryTimeout,
// not SQLite's own much longer _busy_timeout) if the write-transaction path is taken when there is
// nothing to seed.
func TestSeedLERCursorsFromLegacy_NoWriteLockWhenNothingToSeed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "autoclaim.sqlite")

	mainDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer mainDB.Close()
	require.NoError(t, migrations.RunMigrations(logger.GetDefaultLogger(), mainDB))

	const dbQueryTimeout = 200 * time.Millisecond
	storage := New(logger.GetDefaultLogger(), mainDB, dbQueryTimeout)

	// A second, independent connection to the same file holds an open write transaction for the
	// whole test -- SQLite's single write (RESERVED) lock is held from BeginTx onward under
	// _txlock=immediate, before any statement runs.
	holderDB, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer holderDB.Close()
	holderTx, err := holderDB.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = holderTx.Rollback() })

	const sourceNetwork uint32 = 42 // no legacy row exists for this source
	start := time.Now()
	seeded, err := storage.SeedLERCursorsFromLegacy(context.Background(), sourceNetwork, []uint32{1, 2}, time.Now().UTC())
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.False(t, seeded)
	require.Less(t, elapsed, dbQueryTimeout/2,
		"a no-op seed must not block behind another connection's write lock -- it must never open a write transaction")
}

// TestSeedLERCursorsFromLegacy_EmptyDestinationsDoesNotConsumeLegacyRow proves the F7 latent-bug
// guard: calling SeedLERCursorsFromLegacy with an empty destination slice must not delete the
// parked legacy row. processSource (l2_to_lx.go) never calls this with no destinations today, but
// nothing enforced that at the storage layer -- were it ever called this way, it would otherwise
// commit a transaction that deletes the source's only resumption point while seeding nothing to
// replace it, an unrecoverable and silent loss of history.
func TestSeedLERCursorsFromLegacy_EmptyDestinationsDoesNotConsumeLegacyRow(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	const sourceNetwork uint32 = 11
	_, err := database.Exec(`
		INSERT INTO autoclaim_ler_cursor_legacy (source_network, last_ler, last_verify_block_num, updated_at)
		VALUES (?, ?, ?, ?)`,
		sourceNetwork, common.HexToHash("0xbeef").Hex(), uint64(321), now,
	)
	require.NoError(t, err)

	seeded, err := storage.SeedLERCursorsFromLegacy(ctx, sourceNetwork, []uint32{}, now)
	require.NoError(t, err)
	require.False(t, seeded, "an empty destination set must never report a seed as having happened")

	var legacyCount int
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM autoclaim_ler_cursor_legacy WHERE source_network = ?", sourceNetwork,
	).Scan(&legacyCount))
	require.Equal(t, 1, legacyCount,
		"the legacy row must survive an empty-destinations call so a real seed can still happen later")
}

// TestSeedLERCursorsFromLegacy_DoesNotRollBackAnAdvancedPair proves the seed's no-rollback guard:
// SeedLERCursorsFromLegacy's per-destination INSERT is "ON CONFLICT(...) DO NOTHING", so a pair that
// somehow already has a cursor more advanced than the legacy value (e.g. it was seeded and advanced
// by an earlier crash-interrupted attempt, then this call is retried) must keep its own value, not be
// rolled backwards to the legacy one. Degrading that clause to "DO UPDATE" leaves the rest of the
// suite green with nothing else noticing.
func TestSeedLERCursorsFromLegacy_DoesNotRollBackAnAdvancedPair(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()
	now := time.Now().UTC()

	const sourceNetwork uint32 = 15
	const destAdvanced uint32 = 150 // already has a cursor more advanced than the legacy value
	const destNew uint32 = 151      // genuinely has nothing yet: must be seeded from legacy

	legacyLER := common.HexToHash("0x1111")
	const legacyVerifyBlock uint64 = 1000
	_, err := database.Exec(`
		INSERT INTO autoclaim_ler_cursor_legacy (source_network, last_ler, last_verify_block_num, updated_at)
		VALUES (?, ?, ?, ?)`,
		sourceNetwork, legacyLER.Hex(), legacyVerifyBlock, now,
	)
	require.NoError(t, err)

	advancedLER := common.HexToHash("0x9999")
	const advancedVerifyBlock uint64 = 9000
	require.NoError(t, storage.SaveLERCursor(ctx, sourceNetwork, destAdvanced, autoclaimtypes.LERCursor{
		SourceNetwork: sourceNetwork, DestinationNetwork: destAdvanced,
		LastLER: advancedLER, LastVerifyBlockNum: advancedVerifyBlock,
	}, now))

	seeded, err := storage.SeedLERCursorsFromLegacy(ctx, sourceNetwork, []uint32{destAdvanced, destNew}, now)
	require.NoError(t, err)
	require.True(t, seeded)

	advancedCursor, found, err := storage.GetLERCursor(ctx, sourceNetwork, destAdvanced)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, advancedLER, advancedCursor.LastLER,
		"an already-advanced pair must not be rolled back to the legacy value")
	require.Equal(t, advancedVerifyBlock, advancedCursor.LastVerifyBlockNum,
		"an already-advanced pair's verify block must not be rolled back to the legacy value")

	newCursor, found, err := storage.GetLERCursor(ctx, sourceNetwork, destNew)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, legacyLER, newCursor.LastLER, "a pair with no prior cursor must be seeded from legacy")
	require.Equal(t, legacyVerifyBlock, newCursor.LastVerifyBlockNum)
}

func TestEnqueueRequestPersistsSourceLER(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	const (
		sourceNetwork      uint32 = 2
		destinationNetwork uint32 = 0
		depositCount       uint32 = 9
	)

	bridge := autoclaimtypes.BridgeExit{
		SourceNetwork:      sourceNetwork,
		BlockNum:           500,
		TxHash:             common.HexToHash("0xdead"),
		OriginNetwork:      7,
		DestinationNetwork: destinationNetwork,
		DepositCount:       depositCount,
		Amount:             big.NewInt(1),
	}
	request := autoclaimtypes.AutoClaimRequest{
		Status:         autoclaimtypes.RequestStatusDetected,
		Bridge:         bridge,
		LER:            common.HexToHash("0xfeed"),
		VerifyBlockNum: 4242,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	stored, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)

	// Key and global index are derived from the source network, not the token origin network.
	require.Equal(t,
		autoclaimtypes.DeriveRequestKey(sourceNetwork, destinationNetwork, depositCount),
		stored.Key,
	)
	require.Equal(t, 0,
		autoclaimtypes.DeriveGlobalIndexForSource(sourceNetwork, depositCount).Cmp(stored.GlobalIndex),
	)
	require.Equal(t, sourceNetwork, stored.Bridge.SourceNetwork)
	require.Equal(t, common.HexToHash("0xfeed"), stored.LER)
	require.Equal(t, uint64(4242), stored.VerifyBlockNum)

	// Round-trips through a fresh read as well.
	reread, err := storage.GetRequest(ctx, stored.Key)
	require.NoError(t, err)
	require.Equal(t, sourceNetwork, reread.Bridge.SourceNetwork)
	require.Equal(t, common.HexToHash("0xfeed"), reread.LER)
	require.Equal(t, uint64(4242), reread.VerifyBlockNum)
}

func TestEnqueueRequestIsIdempotentAndDetectsDuplicates(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(1, 10, autoclaimtypes.RequestStatusDetected)

	first, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Equal(t, request.Key, first.Key)

	duplicate := request
	duplicate.LastError = "must not overwrite"
	second, inserted, err := storage.EnqueueRequest(ctx, duplicate)
	require.NoError(t, err)
	require.False(t, inserted)
	require.Equal(t, request.Key, second.Key)
	require.Empty(t, second.LastError)

	page, err := storage.ListRequests(ctx, autoclaimtypes.RequestFilter{})
	require.NoError(t, err)
	require.Equal(t, 1, page.Count)
	require.Len(t, page.Requests, 1)
}

func TestListRequestsFiltersAndPagination(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	requests := []autoclaimtypes.AutoClaimRequest{
		makeRequest(1, 10, autoclaimtypes.RequestStatusDetected),
		makeRequest(2, 10, autoclaimtypes.RequestStatusDetected),
		makeRequest(3, 11, autoclaimtypes.RequestStatusDetected),
	}
	for _, request := range requests {
		enqueueRequest(t, ctx, storage, request)
	}

	approved := autoclaimtypes.PolicyDecision{
		PolicyName: "allow-all",
		Result:     autoclaimtypes.PolicyResultApproved,
		Reason:     "test",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	require.NoError(t, storage.RecordPolicyDecision(ctx, requests[1].Key, approved))

	sentAt := time.Now().UTC()
	attempt := autoclaimtypes.TransactionAttempt{
		RequestKey:       requests[1].Key,
		AttemptNumber:    1,
		TxManagerID:      common.HexToHash("0x1234"),
		ClaimTxHash:      common.HexToHash("0x5678"),
		Status:           ethtxtypes.MonitoredTxStatusSent,
		RetryCount:       1,
		MaxRetries:       4,
		SentAt:           &sentAt,
		CreatedAt:        sentAt,
		UpdatedAt:        sentAt,
		TargetBridgeAddr: common.HexToAddress("0x5000000000000000000000000000000000000005"),
	}
	require.NoError(t, storage.RecordTransactionAttempt(ctx, requests[1].Key, attempt))

	destinationNetwork := uint32(10)
	status := autoclaimtypes.RequestStatusDetected
	policyResult := autoclaimtypes.PolicyResultApproved
	bridgeTxHash := requests[1].Bridge.TxHash
	claimTxHash := attempt.ClaimTxHash
	fromBlock := uint64(101)
	toBlock := uint64(103)

	page, err := storage.ListRequests(ctx, autoclaimtypes.RequestFilter{
		DestinationNetwork: &destinationNetwork,
		Status:             &status,
		PolicyResult:       &policyResult,
		BridgeTxHash:       &bridgeTxHash,
		ClaimTxHash:        &claimTxHash,
		FromBlock:          &fromBlock,
		ToBlock:            &toBlock,
		PageSize:           1,
	})
	require.NoError(t, err)
	require.Equal(t, 1, page.Count)
	require.Len(t, page.Requests, 1)
	require.Equal(t, requests[1].Key, page.Requests[0].Key)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, page.Requests[0].PolicyDecision.Result)
	require.Equal(t, attempt.ClaimTxHash, *page.Requests[0].ClaimTxHash)

	originNetwork := autoclaimtypes.L1OriginNetwork
	page, err = storage.ListRequests(ctx, autoclaimtypes.RequestFilter{
		OriginNetwork: &originNetwork,
		PageSize:      2,
	})
	require.NoError(t, err)
	require.Equal(t, 3, page.Count)
	require.Len(t, page.Requests, 2)

	page, err = storage.ListRequests(ctx, autoclaimtypes.RequestFilter{
		OriginNetwork: &originNetwork,
		PageNumber:    1,
		PageSize:      2,
	})
	require.NoError(t, err)
	require.Equal(t, 3, page.Count)
	require.Len(t, page.Requests, 1)
}

func TestListRequestsRejectsOversizedPageSize(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()

	_, err := storage.ListRequests(context.Background(), autoclaimtypes.RequestFilter{
		PageSize: autoclaimtypes.MaxRequestPageSize + 1,
	})

	require.ErrorContains(t, err, "exceeds maximum")
}

func TestTransitionRequestPreconditions(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(1, 10, autoclaimtypes.RequestStatusDetected)
	enqueueRequest(t, ctx, storage, request)

	now := time.Now().UTC().Add(time.Minute)
	transitioned, err := storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyApproved,
		now,
	)
	require.NoError(t, err)
	require.Equal(t, autoclaimtypes.RequestStatusPolicyApproved, transitioned.Status)
	require.Equal(t, now, transitioned.UpdatedAt)

	_, err = storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusQueued,
		time.Now().UTC(),
	)
	require.ErrorIs(t, err, ErrInvalidTransition)

	_, err = storage.TransitionRequest(
		ctx,
		request.Key,
		autoclaimtypes.RequestStatusDetected,
		autoclaimtypes.RequestStatusPolicyRejected,
		time.Now().UTC(),
	)
	require.ErrorIs(t, err, ErrPreconditionFailed)
}

func TestRecordTransactionAttemptAndTimestampUpdates(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(1, 10, autoclaimtypes.RequestStatusQueued)
	request.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	request.UpdatedAt = request.CreatedAt
	enqueueRequest(t, ctx, storage, request)

	before := request.UpdatedAt
	sentAt := before.Add(time.Hour)
	attempt := autoclaimtypes.TransactionAttempt{
		RequestKey:       request.Key,
		ClaimerID:        "claimer-10",
		AttemptNumber:    1,
		TxManagerID:      common.HexToHash("0x1111"),
		ClaimTxHash:      common.HexToHash("0x2222"),
		Status:           ethtxtypes.MonitoredTxStatusSent,
		StatusReason:     "submitted",
		RetryCount:       2,
		MaxRetries:       4,
		SentAt:           &sentAt,
		LastObservedAt:   &sentAt,
		CreatedAt:        sentAt,
		UpdatedAt:        sentAt,
		LastError:        "previous underpriced",
		TransactionData:  []byte{0xca, 0xfe},
		TargetBridgeAddr: common.HexToAddress("0x5000000000000000000000000000000000000005"),
	}

	require.NoError(t, storage.RecordTransactionAttempt(ctx, request.Key, attempt))

	stored, err := storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Equal(t, attempt.ClaimTxHash, *stored.ClaimTxHash)
	require.Equal(t, attempt.TxManagerID, *stored.TxManagerID)
	require.Equal(t, attempt.RetryCount, stored.RetryCount)
	require.Equal(t, attempt.LastError, stored.LastError)
	require.True(t, stored.UpdatedAt.After(before))

	var count int
	err = database.QueryRow(
		"SELECT COUNT(*) FROM autoclaim_transaction_attempt WHERE request_key = ? AND attempt_number = ?",
		request.Key,
		attempt.AttemptNumber,
	).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	later := sentAt.Add(time.Minute)
	proof := autoclaimtypes.ClaimProof{L1InfoTreeIndex: 7, PreparedAt: later}
	require.NoError(t, storage.SaveProof(ctx, request.Key, proof))
	stored, err = storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.NotNil(t, stored.Proof)
	require.Equal(t, uint32(7), stored.Proof.L1InfoTreeIndex)
	require.True(t, stored.UpdatedAt.After(attempt.UpdatedAt))

	require.NoError(t, storage.UpdateLastError(ctx, request.Key, "new error", later.Add(time.Minute)))
	stored, err = storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Equal(t, "new error", stored.LastError)
	require.Equal(t, later.Add(time.Minute), stored.UpdatedAt)
}

func TestGetRequestTreatsJSONNullOptionalFieldsAsNil(t *testing.T) {
	storage, database := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	request := makeRequest(11, 1, autoclaimtypes.RequestStatusQueued)
	stored, inserted, err := storage.EnqueueRequest(ctx, request)
	require.NoError(t, err)
	require.True(t, inserted)
	require.Nil(t, stored.Proof)
	require.Nil(t, stored.PolicyDecision)
	require.Nil(t, stored.ManualDecision)

	_, err = database.ExecContext(ctx, `
		UPDATE autoclaim_request
		SET proof_json = 'null',
			policy_decision_json = 'null',
			manual_decision_json = 'null'
		WHERE request_key = ?`,
		request.Key,
	)
	require.NoError(t, err)

	stored, err = storage.GetRequest(ctx, request.Key)
	require.NoError(t, err)
	require.Nil(t, stored.Proof)
	require.Nil(t, stored.PolicyDecision)
	require.Nil(t, stored.ManualDecision)
}

func TestListRecoverableRequests(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()
	ctx := context.Background()

	for depositCount, status := range map[uint32]autoclaimtypes.RequestStatus{
		1: autoclaimtypes.RequestStatusQueued,
		2: autoclaimtypes.RequestStatusSending,
		3: autoclaimtypes.RequestStatusSent,
		4: autoclaimtypes.RequestStatusConfirmed,
		5: autoclaimtypes.RequestStatusFailed,
	} {
		enqueueRequest(t, ctx, storage, makeRequest(depositCount, 10, status))
	}
	enqueueRequest(t, ctx, storage, makeRequest(6, 11, autoclaimtypes.RequestStatusQueued))

	destinationNetwork := uint32(10)
	page, err := storage.ListRecoverableRequests(ctx, autoclaimtypes.RecoveryFilter{
		DestinationNetwork: &destinationNetwork,
		PageSize:           10,
	})
	require.NoError(t, err)
	require.Equal(t, 3, page.Count)
	require.Len(t, page.Requests, 3)
	for _, request := range page.Requests {
		require.Equal(t, destinationNetwork, request.Bridge.DestinationNetwork)
		require.Contains(t, []autoclaimtypes.RequestStatus{
			autoclaimtypes.RequestStatusQueued,
			autoclaimtypes.RequestStatusSending,
			autoclaimtypes.RequestStatusSent,
		}, request.Status)
	}

	page, err = storage.ListRecoverableRequests(ctx, autoclaimtypes.RecoveryFilter{
		DestinationNetwork: &destinationNetwork,
		Statuses:           []autoclaimtypes.RequestStatus{autoclaimtypes.RequestStatusSent},
		PageSize:           10,
	})
	require.NoError(t, err)
	require.Equal(t, 1, page.Count)
	require.Equal(t, autoclaimtypes.RequestStatusSent, page.Requests[0].Status)
}

func TestMissingRequestReturnsNotFound(t *testing.T) {
	storage, _ := newTestStorage(t)
	defer storage.Close()

	_, err := storage.GetRequest(context.Background(), autoclaimtypes.RequestKey("missing"))
	require.True(t, errors.Is(err, db.ErrNotFound))
}
