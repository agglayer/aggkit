package db

import (
	"encoding/json"
	"path"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/db/migrations"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestSQLiteRegistryTracksEachBridgeEventOfATx pins per-event tracking on the SQLite adapter,
// same scenario as the in-memory adapter's TestRegistryTracksEachBridgeEventOfATx: the tx-only
// id and one id per BridgeEvent log of the same tx are independent rows — a publish, an error or
// a Forget on one never reaches the others — and each keeps its identity across a restart
func TestSQLiteRegistryTracksEachBridgeEventOfATx(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "bridgetracker_test")
	registry, err := NewSQLiteRegistry(dbPath, 0, logger, nil)
	require.NoError(t, err)
	r, ok := registry.(*sqliteRegistry)
	require.True(t, ok)

	txOnly := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	first := domain.TrackingID{NetworkID: 1, TxHash: testHash, Log: domain.LogIndex(3)}
	second := domain.TrackingID{NetworkID: 1, TxHash: testHash, Log: domain.LogIndex(9)}
	for _, id := range []domain.TrackingID{txOnly, first, second} {
		_, err := r.Get(id, true)
		require.NoError(t, err)
	}
	require.Equal(t, 3, r.GetNumTracker())

	firstInfo, secondInfo := testBridgeInfo(), testBridgeInfo()
	firstInfo.LogIndex, firstInfo.DepositCount = 3, 7
	secondInfo.LogIndex, secondInfo.DepositCount = 9, 8
	require.NoError(t, publishStatus(r, first, firstInfo, testAllSteps(false)))
	require.NoError(t, publishStatus(r, second, secondInfo, testAllSteps(false)))
	require.NoError(t, publishError(r, txOnly, &types.ErrorStep{ErrorType: types.StepErrorPermanent}))

	// a restart: every row keeps its own identity and facts
	require.NoError(t, r.Close())
	registry, err = NewSQLiteRegistry(dbPath, 0, logger, nil)
	require.NoError(t, err)
	r, ok = registry.(*sqliteRegistry)
	require.True(t, ok)
	t.Cleanup(func() { require.NoError(t, r.Close()) })
	require.Equal(t, 3, r.GetNumTracker())

	tracking, err := r.Get(first, false)
	require.NoError(t, err)
	require.Equal(t, first, tracking.ID())
	require.Equal(t, uint32(7), tracking.Info().DepositCount)
	tracking, err = r.Get(second, false)
	require.NoError(t, err)
	require.Equal(t, second, tracking.ID())
	require.Equal(t, uint32(8), tracking.Info().DepositCount)
	tracking, err = r.Get(txOnly, false)
	require.NoError(t, err)
	require.Equal(t, txOnly, tracking.ID())
	require.True(t, tracking.Failed(), "the tx-only id fails on its own, its events keep running")

	active, err := r.GetTrackerActives(nil)
	require.NoError(t, err)
	activeIDs := make([]domain.TrackingID, 0, len(active))
	for _, a := range active {
		activeIDs = append(activeIDs, a.ID())
	}
	require.ElementsMatch(t, []domain.TrackingID{first, second}, activeIDs)

	r.Forget(first)
	_, err = r.Get(first, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound)
	_, err = r.Get(second, false)
	require.NoError(t, err, "forgetting one event of the tx must not forget the others")
	_, err = r.Get(txOnly, false)
	require.NoError(t, err)
	require.Equal(t, 2, r.GetNumTracker())
}

// TestSQLiteRegistryMigration0009KeepsTxOnlyRows pins bridgetracker0009: a tracked_bridge row
// written before log_index existed survives the table rebuild as a tx-only row (log_index -1),
// still served to the same tx-only request, while the same tx's events can now be registered
// next to it. Rolling the migration back keeps the tx-only rows and drops the per-event ones
func TestSQLiteRegistryMigration0009KeepsTxOnlyRows(t *testing.T) {
	dbPath := path.Join(t.TempDir(), "bridgetracker_test.sqlite")
	logger := log.WithFields("module", "bridgetracker_test")

	require.NoError(t, aggkitdb.RunMigrations(dbPath, migrations.GetUpTo("bridgetracker0005")))
	info := testBridgeInfo()
	info.BlockNumber, info.BlockHash = 12345, common.HexToHash("0xb10c")
	steps := testAllSteps(false)
	data, err := json.Marshal(trackedBridgeData{Tx: domain.TrackingBridgeTx{Info: info}, Steps: steps})
	require.NoError(t, err)
	oldDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	_, err = oldDB.Exec(`INSERT INTO tracked_bridge
			(network_id, tx_hash, schema_version, claim_status,
			 origin_block_number, origin_block_hash, updated_at, last_access, terminal_since, data)
		 VALUES (?, ?, ?, ?, ?, ?, 1, 1, 0, ?)`,
		1, testHash.Hex(), trackedBridgeSchemaVersion, types.TrackerClaimStatusPending.String(),
		info.BlockNumber, info.BlockHash.Hex(), data)
	require.NoError(t, err)
	require.NoError(t, oldDB.Close())

	// NewSQLiteRegistry applies the remaining migrations, bridgetracker0009 among them
	registry, err := NewSQLiteRegistry(dbPath, 0, logger, nil)
	require.NoError(t, err)
	r, ok := registry.(*sqliteRegistry)
	require.True(t, ok)
	require.Equal(t, 1, r.GetNumTracker())

	var logIndex int64
	require.NoError(t, r.db.QueryRow("SELECT log_index FROM tracked_bridge").Scan(&logIndex))
	require.Equal(t, txOnlyLogIndex, logIndex)

	txOnly := domain.TrackingID{NetworkID: 1, TxHash: testHash}
	tracking, err := r.Get(txOnly, false)
	require.NoError(t, err)
	require.Equal(t, txOnly, tracking.ID())
	require.Equal(t, info, tracking.Info(), "the pre-migration row is still served as-is")
	require.Equal(t, steps, tracking.AllSteps())

	event := domain.TrackingID{NetworkID: 1, TxHash: testHash, Log: domain.LogIndex(3)}
	_, err = r.Get(event, false)
	require.ErrorIs(t, err, domain.ErrTrackingNotFound, "the tx-only row is not any event's row")
	_, err = r.Get(event, true)
	require.NoError(t, err)
	require.Equal(t, 2, r.GetNumTracker())
	require.NoError(t, r.Close())

	require.NoError(t, aggkitdb.RunMigrationsDown(dbPath, migrations.GetUpTo("bridgetracker0009"), nil, 1))
	downDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer downDB.Close()
	var rows int
	require.NoError(t, downDB.QueryRow("SELECT COUNT(*) FROM tracked_bridge WHERE tx_hash = ?", testHash.Hex()).
		Scan(&rows))
	require.Equal(t, 1, rows, "rolling back keeps only the tx-only row")
}
