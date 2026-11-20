package bridgetracker

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

// TestActivityCache_GetActivityHonorsContext verifies a cancelled context fails the read with
// context.Canceled, like the SQLite store's
func TestActivityCache_GetActivityHonorsContext(t *testing.T) {
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{testScannedBridge(1)}}
	cache := newTestActivityCache(scanner, &fakeActivityClaims{isClaimed: []bool{false}})
	_, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	entries, _, _, err := cache.GetActivity(ctx, testFromAddress, false, types.ActivityFilterAll,
		domain.ActivityPage{PageNumber: 1, PageSize: 10})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, entries)
}
