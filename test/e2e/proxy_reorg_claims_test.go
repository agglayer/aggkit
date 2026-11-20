package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// TestProxyReorgClaims covers the claim and GER injection scenarios of agglayer/aggkit#1972 (B1-B4, C1):
// reorgs of the L2 served by aggkit-001 (network 1, synced at LatestBlock) that remove or move the claim
// of an L1 -> L2 bridge, or the GER injection that made it claimable, and how claim_status of
// GET /tracker/v1/activity/from/{address} on the aggkit-proxy follows: back from claimed to readyToClaim
// or pending, and claimed_finalized only once the claim's block is final.
//
// Each scenario bridges from L1 with a sender created for it. See proxy_reorg_common_test.go for the order
// of the assertions (aggkit first, then the proxy).
func TestProxyReorgClaims(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	l2 := NewAnvilChain("L2", testEnv.L2.Client)
	requireProxyReorgEnv(ctx, t, l2)
	holdAggsender(ctx, t, l2)
	bs := testEnv.L2.BridgeService
	networkID := testEnv.L2.NetworkID
	// the destination of every bridge, and the sender of the first claim
	claimer := *testEnv.L2.Transactor

	t.Run("B1 claim reorged out before it is final", func(t *testing.T) {
		bridge := startL1ToL2Bridge(ctx, t, claimer.From)
		bridge.waitInjected(ctx, t)
		ready, _ := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)

		l2.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		claimTx := bridge.sendClaim(ctx, t, &claimer)
		claimReceipt := mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, claimTx)
		requireNonFinal(ctx, t, l2, claimReceipt.BlockNumber.Uint64())
		bridge.requireClaimServed(ctx, t, claimReceipt, false)

		claimed, item := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimed,
			func(i *proxyActivityItem) bool { return claimIs(i, claimReceipt) })
		// NOTE: a claimed bridge is not final until its claim is, even with the bridge's own block final
		// (open decision (b) of #1986: the assertion follows the current behaviour)
		require.False(t, item.isFinalized(), "a claim in a non-final block must not be final: %s", item)
		require.EqualValues(t, networkID, item.ClaimNetworkID)
		require.NotEqual(t, ready.Revision, claimed.Revision, "a claim_status change must change the revision")

		reorgDropping(ctx, t, l2, testEnv.L2.Client, claimTx.Hash())
		requireReorgDetected(ctx, t, bs, networkID, baseline)

		back, item := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)
		require.Nil(t, item.Claim, "the claim record must be gone with the claim: %s", item)
		require.Equal(t, 1, back.Count)
		require.NotEqual(t, claimed.Revision, back.Revision, "going back to readyToClaim must change the revision")
		require.Equal(t, ready.Revision, back.Revision, "same content as before the claim, same revision")

		// claimed again, from a key of the pool (a different transaction), and this time the claim becomes final
		claimer2, key2, err := testEnv.L2.Keys.Checkout()
		require.NoError(t, err)
		t.Cleanup(func() { testEnv.L2.Keys.Return(key2) })
		claimTx2 := bridge.sendClaim(ctx, t, claimer2)
		claimReceipt2 := mineTx(ctx, t, l2, testEnv.L2.Client, claimTx2)
		_, err = l2.MineUntilFinalized(ctx, claimReceipt2.BlockNumber.Uint64())
		require.NoError(t, err)
		bridge.requireClaimServed(ctx, t, claimReceipt2, true)

		final, item := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimedFinalized,
			func(i *proxyActivityItem) bool { return claimIs(i, claimReceipt2) })
		require.True(t, item.isFinalized())
		require.Equal(t, 1, final.Count)
	})

	t.Run("B2 reorg removes the claim and the GER injection", func(t *testing.T) {
		// paused BEFORE the bridge is sent, so that its GER injection is mined by the test
		l2.PauseMiningForTest(ctx, t)
		bridge := startL1ToL2Bridge(ctx, t, claimer.From)
		bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusPending, nil)
		baseline := reorgBaseline(ctx, t, bs, networkID)

		// The injection goes in block X and the claim in X+1, the two blocks that are not final. The
		// claim is built against the injected leaf without waiting for aggkit to serve the injection,
		// which it only does with a block on top of X.
		injection := captureGERInjection(ctx, t, l2, bridge)
		claimTx := bridge.sendClaimAtLeaf(ctx, t, &claimer, injection.leafIndex)
		claimReceipt := mineTx(ctx, t, l2, testEnv.L2.Client, claimTx)
		require.Equal(t, injection.block+1, claimReceipt.BlockNumber.Uint64())
		requireNonFinal(ctx, t, l2, injection.block)
		bridge.waitInjected(ctx, t) // aggkit indexed (and tracks) the block of the injection
		// the claim is in the head block: the proxy reads it from the chain (isClaimed), whether or not
		// the bridge service has indexed its record yet
		claimed, _ := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimed, nil)

		// aggkit must have tracked the head (the claim's block) before the reorg removes it, or the
		// detection races the claim syncer: see requireClaimSyncedToHead.
		requireClaimSyncedToHead(ctx, t, l2, bs)
		reorgDropping(ctx, t, l2, testEnv.L2.Client, append([]common.Hash{claimTx.Hash()}, injection.txs...)...)
		requireReorgDetected(ctx, t, bs, networkID, baseline)

		pending, item := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusPending, nil)
		require.Nil(t, item.Claim, "the claim record must be gone with the claim: %s", item)
		require.NotEqual(t, claimed.Revision, pending.Revision, "going back to pending must change the revision")

		// a later GER is injected (the aggoracle does not inject the removed one again, agglayer/aggkit#1992: see forceNewGER):
		// the bridge recovers
		forceNewGER(ctx, t)
		captureGERInjection(ctx, t, l2, bridge)
		require.NoError(t, l2.Mine(ctx, 1))
		bridge.waitInjected(ctx, t)
		bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)
		claimTx2 := bridge.sendClaim(ctx, t, &claimer)
		claimReceipt2 := mineTx(ctx, t, l2, testEnv.L2.Client, claimTx2)
		_, err := l2.MineUntilFinalized(ctx, claimReceipt2.BlockNumber.Uint64())
		require.NoError(t, err)
		_, item = bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimedFinalized,
			func(i *proxyActivityItem) bool { return claimIs(i, claimReceipt2) })
		require.True(t, item.isFinalized())
	})

	t.Run("B3 finalized claim", func(t *testing.T) {
		// two bridges of the same sender: the first one is claimed and becomes final, the second one stays
		// unclaimed, so that it is refreshed forever and tells when the proxy refreshed the address
		bridge := startL1ToL2Bridge(ctx, t, claimer.From)
		opts := bridge.sender.opts(t, testEnv.L1.ChainID)
		opts.Context = ctx
		secondTx := sendL1Bridge(t, opts, networkID, claimer.From, proxyReorgBridgeAmount)
		secondDeposit := l1DepositCount(t, requireMined(ctx, t, testEnv.Clients.L1, secondTx, "second L1 bridge"))
		bridge.waitInjected(ctx, t)
		// both ready to claim before anything is compared: the second bridge must not change state later
		_, err := waitForActivity(ctx, proxyTrackerBaseURL, bridge.sender.addr, "both bridges ready to claim",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
				first, second := a.find(0, bridge.depositCount), a.find(0, secondDeposit)
				return first != nil && second != nil && first.ClaimStatus == claimStatusReadyToClaim &&
					second.ClaimStatus == claimStatusReadyToClaim
			})
		require.NoError(t, err)

		claimTx := bridge.sendClaim(ctx, t, &claimer)
		claimReceipt := requireMined(ctx, t, testEnv.L2.Client, claimTx, "claim")
		final, settled := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimedFinalized,
			func(i *proxyActivityItem) bool { return claimIs(i, claimReceipt) })
		require.True(t, settled.isFinalized())
		require.Equal(t, 2, final.Count)
		// the tracker closes the bridge for good too
		txHash := bridge.receipt.TxHash
		tracking := waitForTracking(ctx, t, proxyTrackerBaseURL, 0, txHash, "finished",
			func(d *trackerTrackingData) bool { return d.TrackingStatus == "finished" })
		require.Equal(t, claimStatusClaimedFinalized, tracking.ClaimStatus)

		// a reorg of newer, non-final blocks of the claim's chain: a bridge of another sender is dropped
		other := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		require.Greater(t, other.receipt.BlockNumber.Uint64(), claimReceipt.BlockNumber.Uint64()+proxyReorgDepth)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		reorgDropping(ctx, t, l2, testEnv.L2.Client, other.receipt.TxHash)
		requireReorgDetected(ctx, t, bs, networkID, baseline)
		require.NoError(t, WaitForBridgeAbsent(ctx, bs, networkID, other.depositCount, proxyReorgServeTimeout))
		// taken once aggkit has resolved the reorg: a refresh after it read the bridge service as it is now
		resolvedAt := time.Now()

		// the proxy refreshed the address after the reorg (the unclaimed bridge says so) and the settled
		// entry was not touched: last_updated_timestamp stops advancing once an item is claimed_finalized,
		// which is how the API shows that it is not checked again (no isClaimed() call)
		after := requireActivityRefreshedAfter(ctx, t, proxyTrackerBaseURL, bridge.sender.addr, 0, secondDeposit, resolvedAt)
		requireActivityWellFormed(t, after)
		item := after.find(0, bridge.depositCount)
		require.NotNil(t, item)
		require.Equal(t, claimStatusClaimedFinalized, item.ClaimStatus, "claimed_finalized is for good")
		require.True(t, claimIs(item, claimReceipt))
		require.Equal(t, settled.LastUpdatedTimestamp, item.LastUpdatedTimestamp,
			"a settled entry must not be rechecked: last_updated_timestamp must not advance")
		require.Equal(t, final.Revision, after.Revision)
		tracking, err = fetchTrackingDataFrom(ctx, proxyTrackerBaseURL, 0, txHash)
		require.NoError(t, err)
		require.Equal(t, "finished", tracking.TrackingStatus)
		require.Equal(t, claimStatusClaimedFinalized, tracking.ClaimStatus)
	})

	t.Run("B4 claim re-included in another block", func(t *testing.T) {
		bridge := startL1ToL2Bridge(ctx, t, claimer.From)
		bridge.waitInjected(ctx, t)
		bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)

		l2.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		claimTx := bridge.sendClaim(ctx, t, &claimer)
		claimReceipt := mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, claimTx)
		bridge.requireClaimServed(ctx, t, claimReceipt, false)
		claimed, _ := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimed,
			func(i *proxyActivityItem) bool { return claimIs(i, claimReceipt) })

		moved := reorgMoving(ctx, t, l2, testEnv.L2.Client, claimTx.Hash(), 1)
		require.Equal(t, claimReceipt.BlockNumber.Uint64()+1, moved.BlockNumber.Uint64())
		requireReorgDetected(ctx, t, bs, networkID, baseline)
		bridge.requireClaimServed(ctx, t, moved, false)

		after, item := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimed,
			func(i *proxyActivityItem) bool { return claimIs(i, moved) })
		require.Equal(t, 1, after.Count)
		require.False(t, item.isFinalized())
		// the revision hashes the claim's tx hash, not its block: the same claim tx in another block gives
		// the same revision (docs/bridgetracker/API.md, "What goes into the hash")
		log.Infof("B4: revision with the claim at block %d: %s, at block %d: %s",
			claimReceipt.BlockNumber.Uint64(), claimed.Revision, moved.BlockNumber.Uint64(), after.Revision)
		require.Equal(t, claimed.Revision, after.Revision)

		_, err := l2.MineUntilFinalized(ctx, moved.BlockNumber.Uint64())
		require.NoError(t, err)
		_, item = bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimedFinalized,
			func(i *proxyActivityItem) bool { return claimIs(i, moved) })
		require.True(t, item.isFinalized())
	})

	t.Run("C1 reorg removes only the GER injection", func(t *testing.T) {
		l2.PauseMiningForTest(ctx, t)
		bridge := startL1ToL2Bridge(ctx, t, claimer.From)
		bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusPending, nil)
		baseline := reorgBaseline(ctx, t, bs, networkID)

		injection := captureGERInjection(ctx, t, l2, bridge)
		require.NoError(t, l2.Mine(ctx, 1)) // the injection is head-1, and aggkit serves it
		requireNonFinal(ctx, t, l2, injection.block)
		bridge.waitInjected(ctx, t)
		ready, _ := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)

		// no bridge and no claim tracks the injection's block here: only the claim syncer does, so
		// wait for it before reorging the block away (see requireClaimSyncedToHead).
		requireClaimSyncedToHead(ctx, t, l2, bs)
		reorgDropping(ctx, t, l2, testEnv.L2.Client, injection.txs...)
		requireReorgDetected(ctx, t, bs, networkID, baseline)
		injected, err := waitForInjectionGone(ctx, bridge)
		require.NoError(t, err, "aggkit must stop serving the injection the reorg removed (still injected: %t)", injected)

		pending, _ := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusPending, nil)
		require.NotEqual(t, ready.Revision, pending.Revision, "going back to pending must change the revision")

		// a later GER is injected (the aggoracle does not inject the removed one again, agglayer/aggkit#1992: see forceNewGER)
		forceNewGER(ctx, t)
		captureGERInjection(ctx, t, l2, bridge)
		require.NoError(t, l2.Mine(ctx, 1))
		bridge.waitInjected(ctx, t)
		again, _ := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)
		require.Equal(t, ready.Revision, again.Revision, "same content as before the reorg, same revision")
		require.Equal(t, 1, again.Count)
	})
}

// requireClaimSyncedToHead waits until the L2 claim syncer of aggkit-001 (claim_l2_info) has processed
// the current head of l2 (the chain is paused, so the head is stable). The reorg detector only reports a
// reorg of blocks a syncer has already tracked (see "What aggkit detects" in the e2e README), and of the
// syncers merged into the L2 network's last-reorg-event only the claim syncer processes every L2 block:
// without this wait, reorging the head can race the claim syncer and the reorg is never reported.
//
// waitInjected is not enough on its own: it reads l2gersync's injected-GER answer, and l2gersync does not
// feed this network's reorg event. waitForStatus is not enough either: the proxy answers claimStatusClaimed
// from the chain (isClaimed), whether or not the bridge service has indexed the block yet.
func requireClaimSyncedToHead(ctx context.Context, t *testing.T, l2 *AnvilChain, bs *client.Client) {
	t.Helper()
	head, err := l2.Head(ctx)
	require.NoError(t, err)
	require.NoError(t, WaitForClaimL2Processed(ctx, bs, head.Number, proxyReorgServeTimeout),
		"the L2 claim syncer must have processed head block %d before the reorg", head.Number)
}

// waitForInjectionGone waits until aggkit no longer serves a GER injection covering the bridge.
func waitForInjectionGone(ctx context.Context, b *l1ToL2Bridge) (stillInjected bool, err error) {
	err = waitUntil(ctx, proxyReorgServeTimeout, "the GER injection to be gone", func() (bool, error) {
		ok, err := b.injected(ctx)
		if err != nil {
			return false, nil //nolint:nilerr // transient: the bridge service may be resolving the reorg
		}
		stillInjected = ok
		return !ok, nil
	}, nil)
	return stillInjected, err
}
