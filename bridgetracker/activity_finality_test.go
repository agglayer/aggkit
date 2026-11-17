package bridgetracker

import (
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestActivityCache_GetActivityRevision verifies the per-address revision ActivityCache serves:
// the empty-activity value for an address with nothing cached, and whatever the address's cache
// holds otherwise (the follow-up issues are the ones that maintain it)
func TestActivityCache_GetActivityRevision(t *testing.T) {
	cache := NewActivityCache(nil, nil, nil, nil, nil)

	revision, err := cache.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)
	require.Equal(t, domain.EmptyActivityRevision, revision, "an address with nothing cached is empty")

	_, err = cache.RegisterAndAwait(testFromAddress, false, 0)
	require.NoError(t, err)
	revision, err = cache.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)
	require.Equal(t, domain.EmptyActivityRevision, revision)

	entryHash := domain.HashActivityEntry(&domain.ActivityEntry{
		Bridge: &bridgeservicetypes.BridgeResponse{GlobalIndex: "1", TxHash: "0xaa", BlockNum: 7},
	})
	cache.mu.Lock()
	cache.byAddr[testFromAddress].revision = cache.byAddr[testFromAddress].revision.Add(entryHash)
	want := cache.byAddr[testFromAddress].revision.String()
	cache.mu.Unlock()
	revision, err = cache.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)
	require.Equal(t, want, revision)
	require.NotEqual(t, domain.EmptyActivityRevision, revision)

	revision, err = cache.GetActivityRevision(t.Context(), common.HexToAddress("0x2222222222222222222222222222222222222222"))
	require.NoError(t, err)
	require.Equal(t, domain.EmptyActivityRevision, revision, "the revision is per address")
}

// TestActivityCache_GetActivityRevisionIsNotAnAccess verifies reading the revision does not
// refresh the address's lastAccess: only the GetActivity read that follows it counts
func TestActivityCache_GetActivityRevisionIsNotAnAccess(t *testing.T) {
	cache := NewActivityCache(nil, nil, nil, nil, nil)
	start := time.Unix(1_700_000_000, 0)
	cache.now = func() time.Time { return start }
	_, err := cache.RegisterAndAwait(testFromAddress, false, 0)
	require.NoError(t, err)

	cache.now = func() time.Time { return start.Add(time.Hour) }
	_, err = cache.GetActivityRevision(t.Context(), testFromAddress)
	require.NoError(t, err)

	cache.mu.Lock()
	defer cache.mu.Unlock()
	require.Equal(t, start, cache.byAddr[testFromAddress].lastAccess)
}

// TestMatchesFilter_ClaimedIncludesClaimedFinalized verifies filterBridges=claimed keeps matching
// every claimed bridge now that "claimed_finalized" exists, and that no other filter matches it
func TestMatchesFilter_ClaimedIncludesClaimedFinalized(t *testing.T) {
	entry := func(status types.TrackerClaimStatus) *domain.ActivityEntry {
		return &domain.ActivityEntry{TrackerClaimStatus: status}
	}

	require.True(t, MatchesActivityFilter(entry(types.TrackerClaimStatusClaimed), types.ActivityFilterClaimed))
	require.True(t, MatchesActivityFilter(entry(types.TrackerClaimStatusClaimedFinalized), types.ActivityFilterClaimed))
	require.True(t, MatchesActivityFilter(entry(types.TrackerClaimStatusClaimedFinalized), types.ActivityFilterAll))
	for _, filter := range []types.ActivityFilter{
		types.ActivityFilterPending, types.ActivityFilterReadyToClaim, types.ActivityFilterError,
	} {
		require.False(t, MatchesActivityFilter(entry(types.TrackerClaimStatusClaimedFinalized), filter), filter.String())
	}
}
