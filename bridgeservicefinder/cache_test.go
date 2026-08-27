package bridgeservicefinder

import (
	"testing"

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
