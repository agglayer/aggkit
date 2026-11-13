package bridgesync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/bridgesync/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
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
