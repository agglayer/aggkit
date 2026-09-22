package bridgeservicefinder

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestCache_NetworkIDs verifies networkIDs returns exactly the networkIDs currently cached,
// with no duplicates and regardless of insertion order.
func TestCache_NetworkIDs(t *testing.T) {
	c := newCache()
	require.Empty(t, c.networkIDs())

	c.set(1, cacheEntry{url: "http://network-1"})
	c.set(0, cacheEntry{url: "http://network-0"})
	c.set(42, cacheEntry{url: "http://network-42"})

	require.ElementsMatch(t, []uint32{0, 1, 42}, c.networkIDs())

	// Overwriting an existing entry does not duplicate it
	c.set(1, cacheEntry{url: "http://network-1-updated"})
	require.ElementsMatch(t, []uint32{0, 1, 42}, c.networkIDs())
}

// TestFinder_NetworkIDs verifies finder.NetworkIDs delegates to the cache, i.e. it reports
// exactly the networks GetURL would presently succeed for.
func TestFinder_NetworkIDs(t *testing.T) {
	f := &finder{cache: newCache()}
	require.Empty(t, f.NetworkIDs())

	f.cache.set(1, cacheEntry{url: "http://network-1"})
	f.cache.set(7, cacheEntry{url: "http://network-7"})

	ids := f.NetworkIDs()
	require.ElementsMatch(t, []uint32{1, 7}, ids)

	for _, id := range ids {
		urls, err := f.GetURL(id)
		require.NoError(t, err)
		require.NotEmpty(t, urls.BridgeURL)
	}
}

// TestCache_PendingListSortedAscending pins pendingList's documented "sorted by ascending
// NetworkID" contract (also promised by Finder.PendingNetworks, the tracker API docs and the
// swagger description) even when networks are recorded out of order, not just when they happen to
// already be inserted in order.
func TestCache_PendingListSortedAscending(t *testing.T) {
	c := newCache()
	require.Nil(t, c.pendingList())

	now := time.Now().UTC()
	require.True(t, c.setPending(PendingNetwork{NetworkID: 5, RollupAddress: common.Address{5}, FirstSeen: now}))
	require.True(t, c.setPending(PendingNetwork{NetworkID: 3, RollupAddress: common.Address{3}, FirstSeen: now}))
	require.True(t, c.setPending(PendingNetwork{NetworkID: 9, RollupAddress: common.Address{9}, FirstSeen: now}))

	list := c.pendingList()
	require.Len(t, list, 3)
	require.Equal(t, []uint32{3, 5, 9}, []uint32{list[0].NetworkID, list[1].NetworkID, list[2].NetworkID},
		"pendingList must sort by ascending NetworkID regardless of insertion order")
}
