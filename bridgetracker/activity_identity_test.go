package bridgetracker

import (
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// legacyCollisionEtrogUpgradeBlock is the L1 Etrog upgrade block of the legacy collision fixture
const legacyCollisionEtrogUpgradeBlock = 1000

// legacyCollisionBridges returns two different bridges sent by from that the production encoder
// (bridgeservice.NewBridgeResponse -> bridgesync.GlobalIndexForBridge) gives the same global index
// 7: a pre-Etrog L1 bridge (network 0) destined to the legacy zkEVM network 1 — encoded as the bare
// deposit count — and a modern bridge emitted on network 1 with the same deposit count
func legacyCollisionBridges(t *testing.T, from common.Address) (legacyL1, modernNet1 *domain.ScannedBridge) {
	t.Helper()
	const depositCount = 7
	l1 := &bridgesync.Bridge{
		BlockNum: 100, BlockPos: 0, FromAddress: &from, TxHash: common.HexToHash("0x100"), BlockTimestamp: 1_000,
		DestinationNetwork: bridgesync.LegacyZkEVMRollupNetworkID, DestinationAddress: common.HexToAddress("0xd1"),
		Amount: big.NewInt(100), DepositCount: depositCount,
	}
	net1 := &bridgesync.Bridge{
		BlockNum: legacyCollisionEtrogUpgradeBlock + 1, BlockPos: 0, FromAddress: &from,
		TxHash: common.HexToHash("0x200"), BlockTimestamp: 2_000, OriginNetwork: 1, DestinationNetwork: 2,
		DestinationAddress: common.HexToAddress("0xd2"), Amount: big.NewInt(200), DepositCount: depositCount,
	}
	legacyL1 = &domain.ScannedBridge{
		Bridge:    bridgeservice.NewBridgeResponse(l1, 0, legacyCollisionEtrogUpgradeBlock),
		NetworkID: 0, Source: domain.ActivitySourceBridgeService,
	}
	modernNet1 = &domain.ScannedBridge{
		Bridge:    bridgeservice.NewBridgeResponse(net1, 1, legacyCollisionEtrogUpgradeBlock),
		NetworkID: 1, Source: domain.ActivitySourceBridgeService,
	}
	require.Equal(t, bridgeservicetypes.BigIntString("7"), legacyL1.Bridge.GlobalIndex)
	require.Equal(t, legacyL1.Bridge.GlobalIndex, modernNet1.Bridge.GlobalIndex, "the fixture must collide")
	require.NotEqual(t, legacyL1.Bridge.BridgeHash, modernNet1.Bridge.BridgeHash)
	return legacyL1, modernNet1
}

// TestActivityCache_LegacyGlobalIndexCollisionKeepsBothEntries verifies two different bridges
// sharing a global index under the legacy pre-Etrog encoding are both kept, with their own
// network and contents, whatever order the networks are scanned in — and that the address's
// revision counts both, and drops exactly the one removed
func TestActivityCache_LegacyGlobalIndexCollisionKeepsBothEntries(t *testing.T) {
	legacyL1, modernNet1 := legacyCollisionBridges(t, testFromAddress)
	for name, order := range map[string][]*domain.ScannedBridge{
		"L1 first":        {legacyL1, modernNet1},
		"network 1 first": {modernNet1, legacyL1},
	} {
		t.Run(name, func(t *testing.T) {
			scanner := &fakeActivityScanner{bridges: order}
			claims := &fakeActivityClaims{isClaimed: []bool{false, false, false}}
			cache := newTestActivityCache(scanner, claims)

			entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
			require.NoError(t, err)
			require.Len(t, entries, 2, "a shared global index must not collapse two bridges")
			byNetwork := map[uint32]*domain.ActivityEntry{}
			for _, e := range entries {
				byNetwork[e.BridgeNetworkID] = e
			}
			require.Equal(t, legacyL1.Bridge, byNetwork[0].Bridge)
			require.Equal(t, modernNet1.Bridge, byNetwork[1].Bridge)

			want := domain.ActivityRevision{}.
				Add(domain.HashActivityEntry(byNetwork[0])).Add(domain.HashActivityEntry(byNetwork[1]))
			revision, err := cache.GetActivityRevision(t.Context(), testFromAddress)
			require.NoError(t, err)
			require.Equal(t, want.String(), revision)

			// the network-1 bridge is reorged out: only its own entry (and hash) goes
			scanner.bridges = nil
			scanner.invalidated = []domain.BridgeKey{modernNet1.Key()}
			entries, _, err = refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
			require.NoError(t, err)
			require.Len(t, entries, 1)
			require.Equal(t, uint32(0), entries[0].BridgeNetworkID)
			revision, err = cache.GetActivityRevision(t.Context(), testFromAddress)
			require.NoError(t, err)
			require.Equal(t, domain.ActivityRevision{}.Add(domain.HashActivityEntry(entries[0])).String(), revision)
		})
	}
}

// TestActivityCache_SequentialDepositsInOneTxStayDistinct verifies two deposits of one
// transaction (different deposit counts and log positions) are two entries
func TestActivityCache_SequentialDepositsInOneTxStayDistinct(t *testing.T) {
	first, second := testBridge(1), testBridge(2)
	second.BlockPos = 1
	scanner := &fakeActivityScanner{bridges: []*domain.ScannedBridge{
		scannedBridge(first, testScannedNetworkID), scannedBridge(second, testScannedNetworkID),
	}}
	cache := newTestActivityCache(scanner, &fakeActivityClaims{isClaimed: []bool{false, false}})

	entries, _, err := refreshAndGet(t, cache, testFromAddress, false, types.ActivityFilterAll)
	require.NoError(t, err)
	require.Len(t, entries, 2)
}

// TestActivityEntryRefresher_ContradictoryReplacementDoesNotInheritClaim verifies an observation
// of the same key at the same tx/block but with other contents (another block position, bridge
// hash or amount) is not the same bridge: none of the cached entry's claim state, claim record,
// finality or creation time is carried over, and isClaimed() is asked again
func TestActivityEntryRefresher_ContradictoryReplacementDoesNotInheritClaim(t *testing.T) {
	const createdAt = 1_700_000_000
	cachedBridge := func() *bridgeservicetypes.BridgeResponse {
		b := testBridge(1)
		b.BlockNum, b.BlockTimestamp, b.BridgeHash, b.Amount = 100, createdAt, "0xb1", "100"
		return b
	}
	contradictions := map[string]func(b *bridgeservicetypes.BridgeResponse){
		"another block_pos":   func(b *bridgeservicetypes.BridgeResponse) { b.BlockPos = 5 },
		"another bridge_hash": func(b *bridgeservicetypes.BridgeResponse) { b.BridgeHash = "0xfake" },
		"another amount":      func(b *bridgeservicetypes.BridgeResponse) { b.Amount = "999" },
	}
	existingStates := map[string]*domain.ActivityEntry{
		// claimed, claim record not indexed yet: the window under test
		"claimed without record": {ClaimStatus: types.ClaimStatusClaimed, BridgeFinal: true},
		// settled: the final-claim shortcut must not apply either
		"settled": {
			ClaimStatus: types.ClaimStatusClaimed, Claim: &bridgeservicetypes.ClaimResponse{TxHash: "0xc"},
			BridgeFinal: true, ClaimFinal: true, Finalized: true,
		},
	}
	for name, contradict := range contradictions {
		for state, existing := range existingStates {
			t.Run(name+"/"+state, func(t *testing.T) {
				existing := *existing
				existing.Bridge, existing.BridgeNetworkID = cachedBridge(), testScannedNetworkID
				existing.CreatedAt = time.Unix(1, 0).UTC() // not the bridge's own timestamp: must not survive

				observed := cachedBridge()
				contradict(observed)
				item := &domain.ScannedBridge{
					Bridge: observed, NetworkID: testScannedNetworkID, Source: domain.ActivitySourceRPC,
				}
				claims := &fakeActivityClaims{isClaimed: []bool{false}}
				refresher := NewActivityEntryRefresher(claims, nil, reportedFinality(), log.WithFields("module", "test"))

				entry := refresher.Refresh(t.Context(), item, &existing, false, time.Now())
				require.Equal(t, 1, claims.isClaimedCalls, "isClaimed() must be asked again for different contents")
				require.Equal(t, types.ClaimStatusUnclaimed, entry.ClaimStatus)
				require.Nil(t, entry.Claim, "the previous claim record must not be kept")
				require.False(t, entry.BridgeFinal)
				require.False(t, entry.ClaimFinal)
				require.Equal(t, time.Unix(createdAt, 0).UTC(), entry.CreatedAt)
				require.Equal(t, observed, entry.Bridge)
			})
		}
	}
}

// TestActivityEntryRefresher_SameEventKeepsSettledClaim is the control of the test above: the
// very same bridge event, settled, needs no isClaimed() call and keeps its claim
func TestActivityEntryRefresher_SameEventKeepsSettledClaim(t *testing.T) {
	b := testBridge(1)
	b.BridgeHash = "0xb1"
	existing := &domain.ActivityEntry{
		Bridge: b, BridgeNetworkID: testScannedNetworkID, ClaimStatus: types.ClaimStatusClaimed,
		Claim: &bridgeservicetypes.ClaimResponse{TxHash: "0xc"}, BridgeFinal: true, ClaimFinal: true, Finalized: true,
	}
	same := *b
	same.TxHash = "0XTX" // only the case differs
	claims := &fakeActivityClaims{}
	refresher := NewActivityEntryRefresher(claims, nil, reportedFinality(), log.WithFields("module", "test"))

	entry := refresher.Refresh(t.Context(), scannedBridge(&same, testScannedNetworkID), existing, false, time.Now())
	require.Zero(t, claims.isClaimedCalls)
	require.Equal(t, existing.Claim, entry.Claim)
	require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entry.TrackerClaimStatus)
}

// TestPlanActivityRefresh_SettledEntryWithOtherContentsIsRefreshed verifies a settled entry
// reported again at the same location but with other contents is refreshed, not left untouched
// as an unchanged settled entry would be
func TestPlanActivityRefresh_SettledEntryWithOtherContentsIsRefreshed(t *testing.T) {
	settled := &domain.ActivityEntry{
		Bridge: testBridge(1), BridgeNetworkID: testScannedNetworkID, ClaimStatus: types.ClaimStatusClaimed,
		Claim: &bridgeservicetypes.ClaimResponse{TxHash: "0xc"}, BridgeFinal: true, ClaimFinal: true,
	}
	cached := map[domain.BridgeKey]*domain.ActivityEntry{settled.Key(): settled}

	unchanged := PlanActivityRefresh(cached, []*domain.ScannedBridge{testScannedBridge(1)}, nil)
	require.Empty(t, unchanged.Items, "the same settled event is never refreshed again")

	other := testBridge(1)
	other.BlockPos = 9
	plan := PlanActivityRefresh(cached, []*domain.ScannedBridge{scannedBridge(other, testScannedNetworkID)}, nil)
	require.Len(t, plan.Items, 1)
	require.Same(t, other, plan.Items[0].Item.Bridge)
	require.Same(t, settled, plan.Items[0].Existing)
}
