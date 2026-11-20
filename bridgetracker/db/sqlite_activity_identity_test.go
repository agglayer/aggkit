package db

import (
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// otherFromAddress is a second watched address, for the cross-address isolation tests
var otherFromAddress = common.HexToAddress("0x2222222222222222222222222222222222222222")

// scanFor scripts the next BridgesFrom call for addr
func (h *activityHarness) scanFor(addr common.Address, found []*domain.ScannedBridge, invalidated ...domain.BridgeKey) {
	h.scanner.EXPECT().BridgesFrom(mock.Anything, addr, mock.Anything).Return(found, invalidated, nil, nil).Once()
}

// entriesOf refreshes nothing: it returns what the store serves for addr
func (h *activityHarness) entriesOf(addr common.Address) []*domain.ActivityEntry {
	h.t.Helper()
	entries, _, err := h.store.GetActivity(h.t.Context(), addr, false, types.ActivityFilterAll)
	require.NoError(h.t, err)
	return entries
}

func (h *activityHarness) refreshOf(addr common.Address) []*domain.ActivityEntry {
	h.t.Helper()
	require.NoError(h.t, h.store.RefreshAddress(h.t.Context(), addr))
	return h.entriesOf(addr)
}

// collidingBridges are A's authentic bridge and B's contradictory one with the very same key
// (network 1, deposit count 42) and global index, as two clients' scans can report them
func collidingBridges(final bool) (authentic, fake *domain.ScannedBridge) {
	authentic = reorgBridge(42, 100, "0xreal", final)
	authentic.Bridge.Amount, authentic.Bridge.DestinationNetwork = "100", 2
	fake = reorgBridge(42, 101, "0xfake", final)
	fake.Bridge.Amount, fake.Bridge.DestinationNetwork = "999", 99
	return authentic, fake
}

// TestActivityIdentity_SameBridgeOfTwoAddressesStaysSeparate: on both stores, two watched
// addresses whose scans report the same bridge key and global index with contradictory contents
// keep one row each, in either refresh order: neither moves, overwrites nor hides the other's
// row. Forgetting the bridge for one address leaves the other's
func TestActivityIdentity_SameBridgeOfTwoAddressesStaysSeparate(t *testing.T) {
	for _, bFirst := range []bool{false, true} {
		runOnBothStores(t, func(h *activityHarness) {
			mustRegisterActivity(t, h.store, otherFromAddress, 0)
			authentic, fake := collidingBridges(false)
			refreshA := func() {
				h.scanFor(testFromAddress, []*domain.ScannedBridge{authentic})
				h.unclaimed(false)
				h.refresh()
			}
			refreshB := func() {
				h.scanFor(otherFromAddress, []*domain.ScannedBridge{fake})
				h.unclaimed(false)
				h.refreshOf(otherFromAddress)
			}
			if bFirst {
				refreshB()
				refreshA()
			} else {
				refreshA()
				refreshB()
			}
			revisionA := h.revision()

			entriesA, entriesB := h.entries(), h.entriesOf(otherFromAddress)
			require.Len(t, entriesA, 1)
			require.Equal(t, authentic.Bridge, entriesA[0].Bridge)
			require.Len(t, entriesB, 1)
			require.Equal(t, fake.Bridge, entriesB[0].Bridge)

			// B's bridge is reorged out for B: A keeps its own
			h.scanFor(otherFromAddress, nil, fake.Key())
			require.Empty(t, h.refreshOf(otherFromAddress))
			require.Len(t, h.entries(), 1)
			require.Equal(t, revisionA, h.revision(), "B's changes never touch A's revision")

			// flushing B cascades to B only
			h.store.FlushActivity(otherFromAddress)
			require.Len(t, h.entries(), 1)
		})
	}
}

// TestActivityIdentity_SettledRowOfOneAddressDoesNotHideAnother: on both stores, A's settled row
// for a bridge key does not stop B's row for the same key from being stored and checked on its own
func TestActivityIdentity_SettledRowOfOneAddressDoesNotHideAnother(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		mustRegisterActivity(t, h.store, otherFromAddress, 0)
		authentic, fake := collidingBridges(true)
		h.scanFor(testFromAddress, []*domain.ScannedBridge{authentic})
		h.claimed(reorgClaim(300, "0xc1", true))
		entriesA := h.refresh()
		require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entriesA[0].TrackerClaimStatus)

		h.scanFor(otherFromAddress, []*domain.ScannedBridge{fake})
		h.unclaimed(true) // B's own claim state is checked
		entriesB := h.refreshOf(otherFromAddress)
		require.Len(t, entriesB, 1)
		require.Equal(t, fake.Bridge, entriesB[0].Bridge)
		require.Equal(t, types.TrackerClaimStatusReadyToClaim, entriesB[0].TrackerClaimStatus)

		entriesA = h.entries()
		require.Len(t, entriesA, 1)
		require.Equal(t, authentic.Bridge, entriesA[0].Bridge)
		require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entriesA[0].TrackerClaimStatus)
	})
}

// TestActivityIdentity_LegacyGlobalIndexCollisionKeepsBothEntries: on both stores, the two bridges
// the production encoder gives the same global index under the legacy pre-Etrog exception (an L1
// bridge to network 1 and a network-1 bridge, both of deposit count 7) are two entries of one
// address, and the revision covers both
func TestActivityIdentity_LegacyGlobalIndexCollisionKeepsBothEntries(t *testing.T) {
	const etrogUpgradeBlock = 1000
	from := testFromAddress
	legacyL1 := &domain.ScannedBridge{
		Bridge: bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
			BlockNum: 100, FromAddress: &from, TxHash: common.HexToHash("0x100"), BlockTimestamp: 1_000,
			DestinationNetwork: bridgesync.LegacyZkEVMRollupNetworkID, Amount: big.NewInt(100), DepositCount: 7,
		}, 0, etrogUpgradeBlock),
		NetworkID: 0, Source: domain.ActivitySourceBridgeService,
	}
	modernNet1 := &domain.ScannedBridge{
		Bridge: bridgeservice.NewBridgeResponse(&bridgesync.Bridge{
			BlockNum: etrogUpgradeBlock + 1, FromAddress: &from, TxHash: common.HexToHash("0x200"),
			BlockTimestamp: 2_000, OriginNetwork: 1, DestinationNetwork: 2, Amount: big.NewInt(200), DepositCount: 7,
		}, 1, etrogUpgradeBlock),
		NetworkID: 1, Source: domain.ActivitySourceBridgeService,
	}
	require.Equal(t, legacyL1.Bridge.GlobalIndex, modernNet1.Bridge.GlobalIndex, "the fixture must collide")

	for _, order := range [][]*domain.ScannedBridge{{legacyL1, modernNet1}, {modernNet1, legacyL1}} {
		runOnBothStores(t, func(h *activityHarness) {
			h.scan(order)
			h.unclaimed(false)
			h.unclaimed(false)
			entries := h.refresh()
			require.Len(t, entries, 2, "a shared global index must not collapse two bridges")
			want := domain.ActivityRevision{}.
				Add(domain.HashActivityEntry(entries[0])).Add(domain.HashActivityEntry(entries[1]))
			require.Equal(t, want.String(), h.revision())

			// the network-1 bridge is reorged out: only its own row goes
			h.scan(nil, modernNet1.Key())
			h.unclaimed(false) // the L1 bridge, not final, is rechecked
			entries = h.refresh()
			require.Len(t, entries, 1)
			require.Equal(t, uint32(0), entries[0].BridgeNetworkID)
		})
	}
}

// TestActivityIdentity_SettledEntryReplacedByOtherContentsIsRechecked: on both stores, a settled
// entry (claimed, bridge and claim final) reported again at the same tx and block but with another
// bridge hash is a different bridge event: it is refreshed instead of skipped as settled, isClaimed()
// is asked again, the previous claim is not inherited, and the revision changes
func TestActivityIdentity_SettledEntryReplacedByOtherContentsIsRechecked(t *testing.T) {
	runOnBothStores(t, func(h *activityHarness) {
		authentic := reorgBridge(1, 100, "0xb1", true)
		authentic.Bridge.BridgeHash = "0xreal"
		h.scan([]*domain.ScannedBridge{authentic})
		h.claimed(reorgClaim(300, "0xc1", true))
		entries := h.refresh()
		require.Len(t, entries, 1)
		require.Equal(t, types.TrackerClaimStatusClaimedFinalized, entries[0].TrackerClaimStatus)

		contradicting := reorgBridge(1, 100, "0xb1", true)
		contradicting.Bridge.BridgeHash = "0xfake"
		h.scan([]*domain.ScannedBridge{contradicting})
		h.unclaimed(false) // isClaimed() asked again: exactly once
		entries = h.refresh()
		require.Len(t, entries, 1)
		require.Equal(t, bridgeservicetypes.Hash("0xfake"), entries[0].Bridge.BridgeHash)
		require.Equal(t, types.ClaimStatusUnclaimed, entries[0].ClaimStatus)
		require.Nil(t, entries[0].Claim, "the replaced bridge's claim must not be inherited")
		before, after := h.lastRevisions()
		require.NotEqual(t, before, after)
	})
}
