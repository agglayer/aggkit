package sources

import (
	"math"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/mocks"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// allActivity is a GetActivity page holding every entry, for tests that read the whole activity
var allActivity = domain.ActivityPage{PageNumber: 1, PageSize: math.MaxUint32}

// TestActivity_LegacyGlobalIndexCollisionKeepsBothBridges drives two real bridge-service
// responses whose global index collides under the legacy pre-Etrog encoding (the production
// converter bridgeservice.NewBridgeResponse, see bridgesync.GlobalIndexForBridge) through the real
// ActivitySource scan and the real ActivityCache: both survive, with their own network and
// contents, in either network order
func TestActivity_LegacyGlobalIndexCollisionKeepsBothBridges(t *testing.T) {
	const etrogUpgradeBlock = 1000
	from := common.HexToAddress(testFromAddress)
	legacyL1 := bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: 100, FromAddress: &from, TxHash: common.HexToHash("0x100"), BlockTimestamp: 1_000,
		DestinationNetwork: bridgesync.LegacyZkEVMRollupNetworkID, DestinationAddress: common.HexToAddress("0xd1"),
		Amount: big.NewInt(100), DepositCount: 7,
	}, 0, etrogUpgradeBlock)
	modernNet1 := bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
		BlockNum: etrogUpgradeBlock + 1, FromAddress: &from, TxHash: common.HexToHash("0x200"), BlockTimestamp: 2_000,
		OriginNetwork: 1, DestinationNetwork: 2, DestinationAddress: common.HexToAddress("0xd2"),
		Amount: big.NewInt(200), DepositCount: 7,
	}, 1, etrogUpgradeBlock)
	require.Equal(t, legacyL1.GlobalIndex, modernNet1.GlobalIndex, "the fixture must collide")

	for name, networkIDs := range map[string][]uint32{"L1 first": {0, 1}, "network 1 first": {1, 0}} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeActivityBridgeService{}
			svc.setBridges(0, legacyL1)
			svc.setBridges(1, modernNet1)
			source := mustNewActivitySource(t, fakeNetworkLister{networkIDs: networkIDs, url: svc.start(t)}, nil,
				testLogger)

			items, invalidated, warnings, err := source.BridgesFrom(t.Context(), from, nil)
			require.NoError(t, err)
			require.Empty(t, invalidated)
			require.Empty(t, warnings)
			require.Len(t, items, 2)

			claims := mocks.NewActivityClaimChecker(t)
			claims.EXPECT().IsClaimed(mock.Anything, mock.Anything).Return(false, nil)
			claims.EXPECT().IsReadyToClaim(mock.Anything, mock.Anything).Return(false, nil)
			cache := bridgetracker.NewActivityCache(source, claims, bridgetracker.NewMemoryRegistry(10), nil, testLogger)
			_, err = cache.RegisterAndAwait(from, false, 0)
			require.NoError(t, err)
			require.NoError(t, cache.RefreshAddress(t.Context(), from))
			entries, _, _, err := cache.GetActivity(t.Context(), from, false, types.ActivityFilterAll, allActivity)
			require.NoError(t, err)
			require.Len(t, entries, 2)
			byNetwork := map[uint32]*bridgeservicetypes.BridgeResponse{}
			for _, e := range entries {
				byNetwork[e.BridgeNetworkID] = e.Bridge
			}
			require.Equal(t, legacyL1.TxHash, byNetwork[0].TxHash)
			require.Equal(t, legacyL1.Amount, byNetwork[0].Amount)
			require.Equal(t, modernNet1.TxHash, byNetwork[1].TxHash)
			require.Equal(t, modernNet1.Amount, byNetwork[1].Amount)
		})
	}
}

// TestActivitySource_RPCNeverDisplacesTheBridgeService verifies an RPC observation contradicting
// the bridge service for the same key (another tx, amount, destination and bridge hash) is dropped:
// whether the bridge service lists the bridge in this very scan or it is cached from it
func TestActivitySource_RPCNeverDisplacesTheBridgeService(t *testing.T) {
	authentic := bridgeResponse(1, 2, 0, testFromAddress, 1)
	authentic.BridgeHash, authentic.Amount, authentic.DestinationAddress = "0xreal", "100", "0xd2"
	fake := bridgeResponse(1, 2, 0, testFromAddress, 1)
	fake.TxHash, fake.BlockNum, fake.BridgeHash, fake.Amount, fake.DestinationAddress = "0xfake", 50, "0xfake", "999", "0x99"
	rpc := stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
		1: {{Bridge: fake, NetworkID: 1, Source: domain.ActivitySourceRPC}},
	}}

	t.Run("listed by the bridge service in the same scan", func(t *testing.T) {
		svc := &fakeActivityBridgeService{
			syncStatus: &bridgeservicetypes.SyncStatus{L2Info: &bridgeservicetypes.NetworkSyncInfo{IsSynced: true}},
		}
		svc.setBridges(1, authentic)
		source := activitySourceWithRPC(fakeNetworkLister{networkIDs: []uint32{1}, url: svc.start(t)}, rpc)

		items, _, warnings, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), nil)
		require.NoError(t, err)
		require.Empty(t, warnings)
		require.Len(t, items, 1)
		require.Equal(t, authentic, items[0].Bridge)
		require.Equal(t, domain.ActivitySourceBridgeService, items[0].Source)
	})

	t.Run("cached from the bridge service, which cannot be scanned now", func(t *testing.T) {
		lister := fakeMixedNetworkLister{networkIDs: []uint32{1}, urls: map[uint32]string{}}
		source := activitySourceWithRPC(lister, rpc)
		known := map[domain.BridgeKey]domain.KnownBridge{
			keyOf(authentic): knownBridge(authentic, domain.ActivitySourceBridgeService, false),
		}

		items, invalidated, _, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
		require.NoError(t, err)
		require.Empty(t, items)
		require.Empty(t, invalidated)
	})
}

// TestActivitySource_SameLocationWithOtherContentsIsACorrection verifies a bridge reported again
// at the same tx and block but with another block position or other contents is not classified as
// unchanged: it is returned so the cache is corrected, through the RPC fallback and the bridge
// service alike — even when the cached one was final
func TestActivitySource_SameLocationWithOtherContentsIsACorrection(t *testing.T) {
	changes := map[string]func(b *bridgeservicetypes.BridgeResponse){
		"block_pos":           func(b *bridgeservicetypes.BridgeResponse) { b.BlockPos = 4 },
		"bridge_hash":         func(b *bridgeservicetypes.BridgeResponse) { b.BridgeHash = "0xother" },
		"amount":              func(b *bridgeservicetypes.BridgeResponse) { b.Amount = "999" },
		"destination address": func(b *bridgeservicetypes.BridgeResponse) { b.DestinationAddress = "0x99" },
	}
	for name, change := range changes {
		t.Run("rpc/"+name, func(t *testing.T) {
			cached := bridgeResponse(2, 1, 0, testFromAddress, 3)
			cached.BlockNum, cached.BridgeHash, cached.Amount = 100, "0xcached", "100"
			observed := *cached
			change(&observed)
			lister := fakeMixedNetworkLister{networkIDs: []uint32{2}, urls: map[uint32]string{}}
			source := activitySourceWithRPC(lister, stubRPCScanner{bridges: map[uint32][]*domain.ScannedBridge{
				2: {{Bridge: &observed, NetworkID: 2, Source: domain.ActivitySourceRPC}},
			}})
			known := map[domain.BridgeKey]domain.KnownBridge{
				keyOf(cached): knownBridge(cached, domain.ActivitySourceRPC, false),
			}

			items, _, _, err := source.BridgesFrom(t.Context(), common.HexToAddress(testFromAddress), known)
			require.NoError(t, err)
			require.Len(t, items, 1, "a cached RPC record must not stay frozen just because tx and block match")
			require.Equal(t, &observed, items[0].Bridge)
		})

		t.Run("bridge service/"+name, func(t *testing.T) {
			f := newReorgScanFixture(t, 10)
			cached := *f.bridges[2] // known and final
			cached.BridgeHash, cached.Amount = "0xcached", "100"
			f.known[f.key(2)] = knownBridge(&cached, domain.ActivitySourceBridgeService, true)
			listed := cached
			change(&listed)
			f.bridges[2] = &listed
			f.svc.setBridges(1, f.bridges[5], f.bridges[4], f.bridges[3], f.bridges[2], f.bridges[1])

			items, invalidated, _ := f.scan(t)
			require.Empty(t, invalidated)
			require.Contains(t, byGlobalIndex(items), int64(2), "a final entry with other contents is corrected")
			require.Equal(t, &listed, byGlobalIndex(items)[2].Bridge)
		})
	}
}
