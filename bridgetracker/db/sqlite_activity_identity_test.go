package db

import (
	"testing"

	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgetracker/domain"
	"github.com/agglayer/aggkit/bridgetracker/types"
	"github.com/stretchr/testify/require"
)

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
