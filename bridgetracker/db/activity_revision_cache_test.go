package db

import (
	"fmt"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

// TestActivityRevisionCacheDropsAComputationRacingAWrite verifies a revision computed while the
// address was written is never cached, while one computed with no write in between is
func TestActivityRevisionCacheDropsAComputationRacingAWrite(t *testing.T) {
	c := newActivityRevisionCache()
	gen := c.begin("a")
	c.invalidate("a") // a write lands while the revision is being computed
	c.store("a", gen, "stale")
	_, ok := c.get("a")
	require.False(t, ok, "a revision read before a write must not be cached")

	gen = c.begin("a")
	c.store("a", gen, "fresh")
	got, ok := c.get("a")
	require.True(t, ok)
	require.Equal(t, "fresh", got)

	gen = c.begin("b")
	c.forget("b")
	c.begin("b") // seen again: a new generation, never the forgotten one
	c.store("b", gen, "stale")
	_, ok = c.get("b")
	require.False(t, ok)

	c.reset()
	_, ok = c.get("a")
	require.False(t, ok)
}

// TestSQLiteActivityStoreRevisionIsReadOncePerChange verifies the revision is read from the rows
// once and then served from the cache — the rows are made unreadable behind the store's back and
// it is still served — until a write through the store (a row saved, bridges forgotten, the
// address flushed) changes it
func TestSQLiteActivityStoreRevisionIsReadOncePerChange(t *testing.T) {
	store := newTestSQLiteActivityStore(t, &fakeActivityScanner{}, &fakeActivityClaims{})
	mustRegisterActivity(t, store, testFromAddress, 0)
	addr := testFromAddress.Hex()
	save := func(i int) *domain.ActivityEntry {
		t.Helper()
		bridge := &bridgeservicetypes.BridgeResponse{
			GlobalIndex: bridgeservicetypes.BigIntString(fmt.Sprint(i)), DepositCount: uint32(i),
			TxHash: bridgeservicetypes.Hash(fmt.Sprintf("0x%x", i)), DestinationNetwork: 2,
		}
		entry := &domain.ActivityEntry{
			Bridge: bridge, BridgeNetworkID: 1, TrackerClaimStatus: types.TrackerClaimStatusPending,
			ClaimStatus: types.ClaimStatusUnclaimed, CreatedAt: time.Unix(int64(i), 0),
		}
		require.NoError(t, store.saveBridgeRow(addr, &domain.ScannedBridge{Bridge: bridge, NetworkID: 1}, entry))
		return entry
	}
	revision := func() string {
		t.Helper()
		got, err := store.GetActivityRevision(t.Context(), testFromAddress)
		require.NoError(t, err)
		return got
	}
	one, two := save(1), save(2)
	want := domain.ActivityRevision{}.Add(domain.HashActivityEntry(one)).Add(domain.HashActivityEntry(two))
	require.Equal(t, want.String(), revision())

	_, err := store.db.Exec("UPDATE activity_bridge SET revision_hash = NULL, data = x'00'")
	require.NoError(t, err)
	require.Equal(t, want.String(), revision(), "served from the cache: the rows are not read again")

	three := save(3)
	_, err = store.db.Exec("UPDATE activity_bridge SET revision_hash = NULL, data = x'00' WHERE deposit_count < 3")
	require.NoError(t, err)
	_, err = store.GetActivityRevision(t.Context(), testFromAddress)
	require.Error(t, err, "a write invalidates the cache: the (now unreadable) rows are read again")
	require.NoError(t, store.forgetBridges(addr, []domain.BridgeKey{{NetworkID: 1, DepositCount: 1},
		{NetworkID: 1, DepositCount: 2}}))
	require.Equal(t, domain.ActivityRevision{}.Add(domain.HashActivityEntry(three)).String(), revision())

	store.FlushActivity(testFromAddress)
	require.Equal(t, domain.EmptyActivityRevision, revision())
}
