package db

import (
	"fmt"
	"sync"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/stretchr/testify/require"
)

// TestSQLiteActivityStoreCountAndPageAreOneSnapshot reads one page holding every entry of an
// address while another connection keeps adding and removing half of its rows (what a background
// refresh landing during the request does): every response must be self-consistent, its total
// being exactly the number of entries a page holding every entry returns. The total and the page
// must come from the same snapshot of the table, not from two statements a write can land between
func TestSQLiteActivityStoreCountAndPageAreOneSnapshot(t *testing.T) {
	store := newTestSQLiteActivityStore(t, &fakeActivityScanner{}, &fakeActivityClaims{})
	mustRegisterActivity(t, store, testFromAddress, 0)
	addr := testFromAddress.Hex()
	const n, extra = 60, 60
	base := time.Unix(1_700_000_000, 0).UTC()
	for i := range n + extra {
		bridge := &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(fmt.Sprint(i)), DepositCount: uint32(i),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%x", i)), DestinationNetwork: 2,
		}
		item := &domain.ScannedBridge{Bridge: bridge, NetworkID: 1}
		entry := &domain.ActivityEntry{
			Bridge: bridge, BridgeNetworkID: 1, TrackerClaimStatus: types.TrackerClaimStatusPending,
			ClaimStatus: types.ClaimStatusUnclaimed, CreatedAt: base.Add(time.Duration(i) * time.Second),
		}
		require.NoError(t, store.saveBridgeRow(addr, item, entry))
	}
	_, err := store.db.Exec("CREATE TABLE extra_rows AS SELECT * FROM activity_bridge WHERE deposit_count >= ?", n)
	require.NoError(t, err)

	writer, err := aggkitdb.NewSQLiteDB(store.dbPathForTest(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, writer.Close()) })

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := writer.Exec("DELETE FROM activity_bridge WHERE deposit_count >= ?", n); err != nil {
				t.Errorf("deleting: %v", err)
				return
			}
			if _, err := writer.Exec("INSERT INTO activity_bridge SELECT * FROM extra_rows"); err != nil {
				t.Errorf("inserting: %v", err)
				return
			}
		}
	}()

	inconsistent := 0
	deadline := time.Now().Add(2 * time.Second)
	reads := 0
	for ; time.Now().Before(deadline); reads++ {
		entries, total, _, err := store.GetActivity(t.Context(), testFromAddress, false, types.ActivityFilterAll,
			domain.ActivityPage{PageNumber: 1, PageSize: 200})
		require.NoError(t, err)
		if len(entries) != total {
			inconsistent++
		}
	}
	close(stop)
	wg.Wait()
	require.Zero(t, inconsistent, "%d of %d responses report a total that is not the number of entries they hold",
		inconsistent, reads)
}

// dbPathForTest is the file s's connection is open on
func (s *sqliteActivityStore) dbPathForTest(t *testing.T) string {
	t.Helper()
	var seq int
	var name, file string
	require.NoError(t, s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &file))
	require.NotEmpty(t, file)
	return file
}
