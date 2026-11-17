package bridgetracker

import (
	"context"
	"errors"
	"testing"
	"time"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	finalitymocks "github.com/agglayer/aggkit/bridgetracker/finality/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// reportedFinality is a finality resolver (the mockery mock of finality.Resolver) answering
// block_finalized exactly as the bridge service served it: final only when reported true. Test
// fixtures mark a bridge or claim final by setting its BlockFinalized
func reportedFinality() *finalitymocks.Resolver {
	resolver := &finalitymocks.Resolver{}
	resolver.EXPECT().IsFinal(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uint32, _ common.Hash, _ uint64, reported *bool) (bool, error) {
			return reported != nil && *reported, nil
		}).Maybe()
	return resolver
}

// finalBridge marks b as reported final by the bridge service (block_finalized=true)
func finalBridge(b *bridgeservicetypes.BridgeResponse) *bridgeservicetypes.BridgeResponse {
	final := true
	b.BlockFinalized = &final
	return b
}

// finalClaim marks c as reported final by the bridge service (block_finalized=true)
func finalClaim(c *bridgeservicetypes.ClaimResponse) *bridgeservicetypes.ClaimResponse {
	final := true
	c.BlockFinalized = &final
	return c
}

// finalScannedBridge is testScannedBridge with its bridge reported final
func finalScannedBridge(globalIndex int64) *domain.ScannedBridge {
	return scannedBridge(finalBridge(testBridge(globalIndex)), testScannedNetworkID)
}

// TestPlanActivityRefresh verifies how one scan result is merged into the cached entries
func TestPlanActivityRefresh(t *testing.T) {
	entry := func(gi int64, final, settled bool) *domain.ActivityEntry {
		e := &domain.ActivityEntry{Bridge: testBridge(gi), BridgeNetworkID: testScannedNetworkID, BridgeFinal: final}
		if settled {
			e.ClaimStatus = types.ClaimStatusClaimed
			e.Claim = &bridgeservicetypes.ClaimResponse{TxHash: "0xc"}
			e.ClaimFinal = true
		}
		return e
	}
	movedBridge := func(gi int64) *domain.ScannedBridge {
		b := testBridge(gi)
		b.BlockNum = 999
		return scannedBridge(b, testScannedNetworkID)
	}
	cached := map[string]*domain.ActivityEntry{
		"1": entry(1, false, false), // non-final, not returned: rechecked from cache
		"2": entry(2, false, false), // non-final, invalidated: removed
		"3": entry(3, true, true),   // settled, invalidated by mistake: never removed, untouched
		"4": entry(4, true, true),   // settled, returned at the same location: untouched
		"5": entry(5, true, true),   // settled, returned elsewhere (deep reorg): refreshed
		"6": entry(6, false, false), // invalidated but returned again: refreshed with the fresh data
	}
	found := []*domain.ScannedBridge{
		testScannedBridge(7), testScannedBridge(4), movedBridge(5), movedBridge(6), testScannedBridge(7),
	}

	plan := PlanActivityRefresh(cached, found, []string{"2", "3", "6"})

	require.Equal(t, []string{"2"}, plan.Removed)
	keys := make([]string, 0, len(plan.Items))
	for _, item := range plan.Items {
		keys = append(keys, string(item.Item.Bridge.GlobalIndex))
	}
	require.Equal(t, []string{"1", "5", "6", "7"}, keys, "cached first in key order, then new ones once")
	require.Same(t, cached["1"].Bridge, plan.Items[0].Item.Bridge, "rechecked with its cached data")
	require.Equal(t, uint64(999), plan.Items[1].Item.Bridge.BlockNum)
	require.Same(t, cached["5"], plan.Items[1].Existing)
	require.Equal(t, uint64(999), plan.Items[2].Item.Bridge.BlockNum)
	require.Nil(t, plan.Items[3].Existing)
}

// TestActivityEntryRefresher_RPCBridgeIsNeverFinal verifies a bridge only found through the RPC
// fallback is not final whatever the resolver would say, and the resolver is not even asked
func TestActivityEntryRefresher_RPCBridgeIsNeverFinal(t *testing.T) {
	resolver := finalitymocks.NewResolver(t) // no expectation: any call fails the test
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	refresher := NewActivityEntryRefresher(claims, nil, resolver, log.WithFields("module", "activity_test"))

	item := scannedBridge(finalBridge(testBridge(1)), testScannedNetworkID)
	item.Source = domain.ActivitySourceRPC
	entry := refresher.Refresh(t.Context(), item, nil, false, time.Now())
	require.False(t, entry.BridgeFinal)
	require.False(t, entry.Finalized)
}

// TestActivityEntryRefresher_ResolverErrorIsNotFinal verifies a resolver failure reads as "not
// final yet", never failing the refresh
func TestActivityEntryRefresher_ResolverErrorIsNotFinal(t *testing.T) {
	resolver := finalitymocks.NewResolver(t)
	resolver.EXPECT().IsFinal(mock.Anything, testScannedNetworkID, mock.Anything, mock.Anything, mock.Anything).
		Return(false, errors.New("rpc down")).Once()
	claims := &fakeActivityClaims{isClaimed: []bool{false}}
	refresher := NewActivityEntryRefresher(claims, nil, resolver, log.WithFields("module", "activity_test"))

	entry := refresher.Refresh(t.Context(), testScannedBridge(1), nil, false, time.Now())
	require.False(t, entry.BridgeFinal)
	require.Equal(t, types.TrackerClaimStatusPending, entry.TrackerClaimStatus)
}
