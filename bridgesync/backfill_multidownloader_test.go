package bridgesync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

// seedBackfillRecords runs the migrations and inserts one bridge row per entry, then clears
// txn_sender so every row becomes eligible for backfill.
func seedBackfillRecords(t *testing.T, dbPath string, entries []RecordToBackfill) {
	t.Helper()
	require.NoError(t, migrations.RunMigrations(dbPath))

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	ctx := t.Context()
	tx, err := db.NewTx(ctx, database)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO block (num) VALUES (1)`)
	require.NoError(t, err)
	for _, e := range entries {
		require.NoError(t, meddler.Insert(tx, bridgeTableName,
			newTestBridge(e.BlockNum, e.BlockPos, e.TxHash.Hex())))
	}
	require.NoError(t, tx.Commit())

	_, err = database.Exec("UPDATE bridge SET txn_sender = ''")
	require.NoError(t, err)
}

// TestBackfillTxnSender_MissingBackendRejectedBeforeDBOpen verifies that configuring the transaction
// provider without the download backend is rejected before the SQLite file is ever created.
func TestBackfillTxnSender_MissingBackendRejectedBeforeDBOpen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill_missing_backend.sqlite")

	backfiller, err := NewBackfillTxnSender(dbPath, mocksethclient.NewEthClienter(t),
		common.HexToAddress("0x1234"), true, log.WithFields("module", "test"),
		WithTransactionProvider(&fakeTransactionProvider{}))
	require.ErrorIs(t, err, ErrBackfillBackendRequired)
	require.Nil(t, backfiller)

	_, statErr := os.Stat(dbPath)
	require.True(t, os.IsNotExist(statErr),
		"the database must not be created when the provider/backend configuration is invalid")
}

// TestBackfillTxnSender_ProviderBackendUnavailable verifies that when the backend reports the range
// as not covered the backfill fails fast: it never queries the backend, never calls the provider and
// never touches the RPC client, and the failed record is retained for a later retry.
func TestBackfillTxnSender_ProviderBackendUnavailable(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill.sqlite")
	txHash := common.HexToHash("0xabc")
	seedBackfillRecords(t, dbPath, []RecordToBackfill{{BlockNum: 1, BlockPos: 0, TxHash: txHash}})

	bridge := common.HexToAddress("0x1234")
	// No expectations on the eth client: any RPC call would fail the test.
	ethClient := mocksethclient.NewEthClienter(t)
	provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
	}}
	backend := &fakeBackendMD{fakeMultidownloader: &fakeMultidownloader{}, available: false}

	backfiller, err := NewBackfillTxnSender(dbPath, ethClient, bridge, false,
		log.WithFields("module", "test"),
		WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()

	records := []RecordToBackfill{{
		BlockNum: 1, BlockPos: 0, TxHash: txHash, LeafType: bridgeLeafTypeMessage,
	}}
	processed, err := backfiller.processBatch(t.Context(), bridgeTableName, records)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
	require.Equal(t, 0, processed)
	require.Equal(t, 0, provider.callCount(), "provider must not be called when the range is not covered")
	require.Empty(t, backend.recordedQueries(),
		"the availability pre-check must short-circuit before FilterLogs")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	var remaining int
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM bridge WHERE block_num = 1 AND block_pos = 0 "+
			"AND (txn_sender = '' OR txn_sender IS NULL)").Scan(&remaining))
	require.Equal(t, 1, remaining, "the failed record must be retained for a later retry")
}

// TestBackfillTxnSender_ProviderPartialSuccess verifies that a batch mixing a covered and an
// uncovered record persists the successful update, retains the failed row and returns an error.
func TestBackfillTxnSender_ProviderPartialSuccess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill.sqlite")
	txHashOK := common.HexToHash("0xaaaa")
	txHashMissing := common.HexToHash("0xbbbb")
	seedBackfillRecords(t, dbPath, []RecordToBackfill{
		{BlockNum: 1, BlockPos: 0, TxHash: txHashOK},
		{BlockNum: 1, BlockPos: 1, TxHash: txHashMissing},
	})

	bridge := common.HexToAddress("0x1234")
	// No expectations on the eth client: any RPC call would fail the test.
	ethClient := mocksethclient.NewEthClienter(t)
	to := "0x2222222222222222222222222222222222222222"
	provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
		To:      &to,
	}}
	// Only the first record's log is covered by the backend.
	backend := &fakeBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		available:           true,
		logs: []types.Log{{
			Address:     bridge,
			Topics:      []common.Hash{bridgeEventSignature},
			TxHash:      txHashOK,
			BlockNumber: 1,
			Index:       0,
		}},
	}

	backfiller, err := NewBackfillTxnSender(dbPath, ethClient, bridge, false,
		log.WithFields("module", "test"),
		WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()

	records := []RecordToBackfill{
		{BlockNum: 1, BlockPos: 0, TxHash: txHashOK, LeafType: bridgeLeafTypeMessage},
		{BlockNum: 1, BlockPos: 1, TxHash: txHashMissing, LeafType: bridgeLeafTypeMessage},
	}
	processed, err := backfiller.processBatch(t.Context(), bridgeTableName, records)
	require.Error(t, err)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
	require.Equal(t, 1, processed)
	require.Equal(t, 1, provider.callCount(), "only the covered record may reach the provider")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()

	var sender string
	require.NoError(t, database.QueryRow(
		"SELECT txn_sender FROM bridge WHERE block_num = 1 AND block_pos = 0").Scan(&sender))
	require.Equal(t, "0x1111111111111111111111111111111111111111", sender,
		"the successful record must be persisted")

	var remaining int
	require.NoError(t, database.QueryRow(
		"SELECT COUNT(*) FROM bridge WHERE block_num = 1 AND block_pos = 1 "+
			"AND (txn_sender = '' OR txn_sender IS NULL)").Scan(&remaining))
	require.Equal(t, 1, remaining, "the failed record must be retained")
}

// scriptedBackendMD is a controllable multidownloader backend for the backfill retry tests. It can
// report its range as permanently unavailable or unavailable for the first N availability checks
// (to simulate a multidownloader whose history is still filling) and returns a scripted set of logs
// per FilterLogs call, repeating the last scripted entry once the script is exhausted.
type scriptedBackendMD struct {
	*fakeMultidownloader

	mu              sync.Mutex
	unavailable     bool
	availableAfter  int
	availabilityCnt int
	filterScripts   [][]types.Log
	filterCalls     int
	queries         []ethereum.FilterQuery
}

var _ aggkittypes.MultiDownloaderLegacy = (*scriptedBackendMD)(nil)

func (f *scriptedBackendMD) IsAvailable(mdrtypes.LogQuery) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return false
	}
	f.availabilityCnt++
	return f.availabilityCnt > f.availableAfter
}

func (f *scriptedBackendMD) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	if len(f.filterScripts) == 0 {
		return nil, nil
	}
	i := f.filterCalls
	f.filterCalls++
	if i >= len(f.filterScripts) {
		i = len(f.filterScripts) - 1
	}
	return f.filterScripts[i], nil
}

func (f *scriptedBackendMD) availabilityChecks() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.availabilityCnt
}

// failOnceTransactionProvider resolves every log through the shared provider but fails the first
// time it is asked about failTx. It lets a test build a deterministic "one row succeeds, one row
// fails and is retried" batch regardless of worker scheduling. It counts resolutions per transaction
// hash so a test can assert that an already-persisted row is not re-resolved on the retry.
type failOnceTransactionProvider struct {
	mu         sync.Mutex
	calls      int
	callsByTx  map[common.Hash]int
	failTx     common.Hash
	failedOnce bool
	tx         *aggkittypes.RPCTransaction
}

var _ aggkittypes.TransactionProvider = (*failOnceTransactionProvider)(nil)

func (f *failOnceTransactionProvider) TransactionByLog(
	_ context.Context, l types.Log,
) (*aggkittypes.RPCTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.callsByTx == nil {
		f.callsByTx = make(map[common.Hash]int)
	}
	f.callsByTx[l.TxHash]++
	if l.TxHash == f.failTx && !f.failedOnce {
		f.failedOnce = true
		return nil, errors.New("transient backfill provider error")
	}
	return f.tx, nil
}

func (f *failOnceTransactionProvider) callsFor(txHash common.Hash) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.callsByTx[txHash]
}

// bridgeLogForBackfill builds an exact, canonical bridge log for a backfill record.
func bridgeLogForBackfill(bridge common.Address, txHash common.Hash, index uint) types.Log {
	return types.Log{
		Address:     bridge,
		Topics:      []common.Hash{bridgeEventSignature},
		TxHash:      txHash,
		BlockNumber: 1,
		Index:       index,
	}
}

// TestBackfillTxnSender_RetriesUnavailableUntilCoverage reproduces the review scenario: an existing
// bridge database is switched to the multidownloader while the multidownloader history is still
// filling. The first availability checks report the range as uncovered (ErrTransactionUnavailable);
// once coverage advances the historical row is filled without a process restart.
func TestBackfillTxnSender_RetriesUnavailableUntilCoverage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill_retry_unavailable.sqlite")
	txHash := common.HexToHash("0xabc")
	seedBackfillRecords(t, dbPath, []RecordToBackfill{{BlockNum: 1, BlockPos: 0, TxHash: txHash}})

	bridge := common.HexToAddress("0x1234")
	ethClient := mocksethclient.NewEthClienter(t)
	to := "0x2222222222222222222222222222222222222222"
	provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
		To:      &to,
	}}
	// The range only becomes covered on the third availability check.
	backend := &scriptedBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		availableAfter:      2,
		filterScripts:       [][]types.Log{{bridgeLogForBackfill(bridge, txHash, 0)}},
	}

	backfiller, err := NewBackfillTxnSender(dbPath, ethClient, bridge, false,
		log.WithFields("module", "test"),
		WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()
	// Deterministic, fast retry: never wait for the real backoff in a unit test.
	backfiller.retryDelay = func(int) time.Duration { return 0 }

	require.NoError(t, backfiller.backfillTable(t.Context(), bridgeTableName))

	require.GreaterOrEqual(t, backend.availabilityChecks(), 3,
		"the backfill must retry until the multidownloader coverage appears, not give up on the first pass")

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()
	var sender string
	require.NoError(t, database.QueryRow(
		"SELECT txn_sender FROM bridge WHERE block_num = 1 AND block_pos = 0").Scan(&sender))
	require.Equal(t, "0x1111111111111111111111111111111111111111", sender,
		"the historical row must be filled once coverage advances without a restart")
}

// TestBackfillTxnSender_PartialSuccessRetriesFailedRowWithoutDuplicates verifies that a batch which
// persists one row and fails another keeps the successful work, then retries only the failed row on
// the next pass without re-resolving (duplicating) the already-persisted one.
func TestBackfillTxnSender_PartialSuccessRetriesFailedRowWithoutDuplicates(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill_partial_retry.sqlite")
	txHashOK := common.HexToHash("0xaaaa")
	txHashLate := common.HexToHash("0xbbbb")
	seedBackfillRecords(t, dbPath, []RecordToBackfill{
		{BlockNum: 1, BlockPos: 0, TxHash: txHashOK},
		{BlockNum: 1, BlockPos: 1, TxHash: txHashLate},
	})

	bridge := common.HexToAddress("0x1234")
	ethClient := mocksethclient.NewEthClienter(t)
	to := "0x2222222222222222222222222222222222222222"
	provider := &failOnceTransactionProvider{
		failTx: txHashLate,
		tx:     &aggkittypes.RPCTransaction{FromRaw: "0x1111111111111111111111111111111111111111", To: &to},
	}
	backend := &scriptedBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		filterScripts: [][]types.Log{{
			bridgeLogForBackfill(bridge, txHashOK, 0),
			bridgeLogForBackfill(bridge, txHashLate, 1),
		}},
	}

	backfiller, err := NewBackfillTxnSender(dbPath, ethClient, bridge, false,
		log.WithFields("module", "test"),
		WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()
	backfiller.retryDelay = func(int) time.Duration { return 0 }

	require.NoError(t, backfiller.backfillTable(t.Context(), bridgeTableName))

	database, err := db.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()
	for _, pos := range []uint64{0, 1} {
		var sender string
		require.NoError(t, database.QueryRow(
			"SELECT txn_sender FROM bridge WHERE block_num = 1 AND block_pos = ?", pos).Scan(&sender))
		require.Equal(t, "0x1111111111111111111111111111111111111111", sender,
			"both the first-pass success and the retried row must be filled")
	}
	require.Equal(t, 1, provider.callsFor(txHashOK),
		"the persisted successful record must not be re-resolved on the retry")
	require.Equal(t, 2, provider.callsFor(txHashLate),
		"the failed record must be resolved once (failed) and once (retried with success)")
}

// TestBackfillTxnSender_UnavailableRetryHonoursDelayAndCancellation verifies that an unavailable
// range spaces its retries with a positive delay and that cancelling the context terminates the
// backfill promptly instead of busy-spinning or leaving a pending request behind.
func TestBackfillTxnSender_UnavailableRetryHonoursDelayAndCancellation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill_cancel.sqlite")
	txHash := common.HexToHash("0xabc")
	seedBackfillRecords(t, dbPath, []RecordToBackfill{{BlockNum: 1, BlockPos: 0, TxHash: txHash}})

	bridge := common.HexToAddress("0x1234")
	ethClient := mocksethclient.NewEthClienter(t)
	provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
	}}
	// The range never becomes covered: the loop must keep scheduling spaced retries.
	backend := &scriptedBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		unavailable:         true,
	}

	backfiller, err := NewBackfillTxnSender(dbPath, ethClient, bridge, false,
		log.WithFields("module", "test"),
		WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()

	backfiller.retryDelay = func(attempt int) time.Duration {
		require.GreaterOrEqual(t, attempt, 1)
		return 25 * time.Millisecond
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var sleeps int
	backfiller.sleep = func(sleepCtx context.Context, d time.Duration) error {
		sleeps++
		require.Positive(t, d, "a failed retry must be spaced by a positive delay")
		// Simulate the delay elapsing, then cancel so the loop must return promptly.
		cancel()
		return sleepCtx.Err()
	}

	start := time.Now()
	err = backfiller.backfillTable(ctx, bridgeTableName)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, sleeps, "one spaced retry before cancellation")
	require.Less(t, time.Since(start), time.Second, "cancellation must terminate promptly")
}

// TestBackfillTxnSender_PermanentFailureStopsWithoutBusySpin verifies that a permanent error (a
// stale canonical inclusion) stops the backfill with a clear error and never schedules a retry.
func TestBackfillTxnSender_PermanentFailureStopsWithoutBusySpin(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "backfill_permanent.sqlite")
	txHash := common.HexToHash("0xabc")
	seedBackfillRecords(t, dbPath, []RecordToBackfill{{BlockNum: 1, BlockPos: 0, TxHash: txHash}})

	bridge := common.HexToAddress("0x1234")
	ethClient := mocksethclient.NewEthClienter(t)
	provider := &fakeTransactionProvider{err: aggkittypes.ErrTransactionInclusionMismatch}
	backend := &scriptedBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		filterScripts:       [][]types.Log{{bridgeLogForBackfill(bridge, txHash, 0)}},
	}

	backfiller, err := NewBackfillTxnSender(dbPath, ethClient, bridge, false,
		log.WithFields("module", "test"),
		WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()
	backfiller.sleep = func(context.Context, time.Duration) error {
		t.Fatal("a permanent failure must not schedule a retry")
		return nil
	}

	err = backfiller.backfillTable(t.Context(), bridgeTableName)
	require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	require.Contains(t, err.Error(), "permanent failure")
}
