package db

import (
	"fmt"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

// TestSQLiteActivityStoreBackfillsRevisionHashes drops every stored revision hash (what
// bridgetracker0008 does to the hashes of the v1 scheme) and reopens the store: it must store the
// hash of every decodable row again, in more than one batch, so the revision is the same as before
// and is served without decoding any row; an undecodable row is left without one
func TestSQLiteActivityStoreBackfillsRevisionHashes(t *testing.T) {
	store := newTestSQLiteActivityStore(t, &fakeActivityScanner{}, &fakeActivityClaims{})
	mustRegisterActivity(t, store, testFromAddress, 0)
	addr := testFromAddress.Hex()
	const n = revisionBackfillBatch + 7
	var want domain.ActivityRevision
	for i := range n {
		bridge := &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(fmt.Sprint(i)), DepositCount: uint32(i), BlockPos: uint64(i),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%x", i)), DestinationNetwork: 2, Amount: "1",
		}
		entry := &domain.ActivityEntry{
			Bridge: bridge, BridgeNetworkID: uint32(i % 2), TrackerClaimStatus: types.TrackerClaimStatusPending,
			ClaimStatus: types.ClaimStatusUnclaimed, CreatedAt: time.Unix(int64(i), 0),
		}
		require.NoError(t, store.saveBridgeRow(addr, &domain.ScannedBridge{Bridge: bridge, NetworkID: uint32(i % 2)},
			entry))
		want = want.Add(domain.HashActivityEntry(entry))
	}
	got, err := store.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)
	require.Equal(t, want.String(), got)

	_, err = store.db.Exec("UPDATE activity_bridge SET revision_hash = NULL")
	require.NoError(t, err)
	_, err = store.db.Exec("UPDATE activity_bridge SET data = x'00' WHERE deposit_count = 3")
	require.NoError(t, err)

	require.NoError(t, backfillRevisionHashes(store.db, log.WithFields("module", "activity_test")))
	var missing int
	require.NoError(t, store.db.QueryRow(
		"SELECT COUNT(*) FROM activity_bridge WHERE revision_hash IS NULL").Scan(&missing))
	require.Equal(t, 1, missing, "only the undecodable row is left without a hash")

	_, err = store.db.Exec("DELETE FROM activity_bridge WHERE deposit_count = 3")
	require.NoError(t, err)
	_, err = store.db.Exec("UPDATE activity_bridge SET data = x'00'") // nothing may be decoded any more
	require.NoError(t, err)
	store.revisions.reset() // the rows were changed behind the store's back
	got, err = store.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)
	var wantWithout3 domain.ActivityRevision
	for i := range n {
		if i == 3 {
			continue
		}
		bridge := &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(fmt.Sprint(i)), DepositCount: uint32(i), BlockPos: uint64(i),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%x", i)), DestinationNetwork: 2, Amount: "1",
		}
		wantWithout3 = wantWithout3.Add(domain.HashActivityEntry(&domain.ActivityEntry{
			Bridge: bridge, BridgeNetworkID: uint32(i % 2), TrackerClaimStatus: types.TrackerClaimStatusPending,
		}))
	}
	require.Equal(t, wantWithout3.String(), got)
}
