package bridgetracker_test

import (
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/db/migrations"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitdb "github.com/agglayer/aggkit/db"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// TestMultiBridgeTxMigratedTxOnlyRowServedThroughAPI pins bridgetracker0006 end to end: a
// tracked_bridge row an instance at bridgetracker0005 (one row per tx) persisted for a resolved
// bridge survives the upgrade as a tx-only row (log_index -1). The same tx-only request is still
// served it as-is, through the REST API and without resolving the tx again, while the same tx's
// bridge can now also be tracked by its log_index, in a row of its own
func TestMultiBridgeTxMigratedTxOnlyRowServedThroughAPI(t *testing.T) {
	// the row an older instance persisted: produced by resolving a single-bridge tx tx-only
	sourcePath := filepath.Join(t.TempDir(), "source.sqlite")
	source := newTrackerUnderTest(t, sourcePath, []*gethtypes.Log{packBridgeEventLog(t, firstBridge)})
	source.get(t, nil)
	bridgetracker.TickOnce(t.Context(), source.engine)
	requireTracks(t, firstBridge, source.get(t, nil))
	closeRegistry(t, source.registry)
	sourceDB, err := aggkitdb.NewSQLiteDB(sourcePath)
	require.NoError(t, err)
	var (
		schemaVersion, originBlockNumber, updatedAt, lastAccess, terminalSince int64
		claimStatus, originBlockHash                                           string
		data                                                                   []byte
	)
	require.NoError(t, sourceDB.QueryRow(`SELECT schema_version, claim_status, origin_block_number,
			origin_block_hash, updated_at, last_access, terminal_since, data
		  FROM tracked_bridge WHERE tx_hash = ?`, multiBridgeTxHash.Hex()).
		Scan(&schemaVersion, &claimStatus, &originBlockNumber, &originBlockHash,
			&updatedAt, &lastAccess, &terminalSince, &data))
	require.NoError(t, sourceDB.Close())

	// a DB at bridgetracker0005 holding that row, under the pre-0006 key (network_id, tx_hash)
	dbPath := filepath.Join(t.TempDir(), "tracker.sqlite")
	require.NoError(t, aggkitdb.RunMigrations(dbPath, migrations.GetUpTo("bridgetracker0005")))
	oldDB, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	_, err = oldDB.Exec(`INSERT INTO tracked_bridge
			(network_id, tx_hash, schema_version, claim_status, origin_block_number, origin_block_hash,
			 updated_at, last_access, terminal_since, data)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		multiBridgeNetwork, multiBridgeTxHash.Hex(), schemaVersion, claimStatus, originBlockNumber,
		originBlockHash, updatedAt, lastAccess, terminalSince, data)
	require.NoError(t, err)
	require.NoError(t, oldDB.Close())

	// the upgraded instance applies bridgetracker0006; its JSON-RPC client has no expectations,
	// so the migrated row must be served without resolving the tx again
	tracker := newTrackerUnderTest(t, dbPath, nil)
	txOnly := tracker.get(t, nil)
	requireTracks(t, firstBridge, txOnly)
	require.Nil(t, txOnly.LogIndex)
	require.Equal(t, 1, tracker.trackedRows(t))

	// the tx's bridge by its log_index is another identity: registered next to the tx-only row
	selected := tracker.get(t, logIdx(firstBridge.logIndex))
	require.Equal(t, types.TrackingStatusRegistered.String(), selected.TrackingStatus)
	require.NotNil(t, selected.LogIndex)
	require.Equal(t, 2, tracker.trackedRows(t))

	db, err := aggkitdb.NewSQLiteDB(dbPath)
	require.NoError(t, err)
	defer db.Close()
	rows, err := db.Query("SELECT log_index FROM tracked_bridge WHERE tx_hash = ? ORDER BY log_index",
		multiBridgeTxHash.Hex())
	require.NoError(t, err)
	defer rows.Close()
	var logIndexes []int64
	for rows.Next() {
		var logIndex int64
		require.NoError(t, rows.Scan(&logIndex))
		logIndexes = append(logIndexes, logIndex)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []int64{-1, int64(firstBridge.logIndex)}, logIndexes)
}
