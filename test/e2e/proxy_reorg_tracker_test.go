package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	trackingStatusRunning  = "running"
	trackingStatusFinished = "finished"
	trackingStatusError    = "error"

	trackerStepWaitingClaim = "WaitingClaim"
	trackerStepClaimed      = "Claimed"
)

// TestProxyReorgTracker covers the tracker scenarios of agglayer/aggkit#1972 (D1, D2): reorgs while the
// aggkit-proxy tracks a bridge through GET /tracker/v1/network/{id}/tx/{hash} and its WebSocket.
//
// D3 (L2 -> L1, L1 reorg of the settlement transaction) is optional in the issue and is not covered.
func TestProxyReorgTracker(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	l2 := NewAnvilChain("L2", testEnv.L2.Client)
	requireProxyReorgEnv(ctx, t, l2)
	bs := testEnv.L2.BridgeService
	networkID := testEnv.L2.NetworkID

	t.Run("D1 origin transaction moves and then disappears while tracking", func(t *testing.T) {
		sender := newProxyReorgSender(t)
		bridger := newL2Bridger(ctx, t, &testEnv.L2, sender)
		l2.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		tx := bridger.send(t, proxyReorgBridgeAmount)
		receipt := mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, tx)
		depositCount := bridger.depositCount(t, receipt)
		requireNonFinal(ctx, t, l2, receipt.BlockNumber.Uint64())
		requireBridgeServed(ctx, t, bs, networkID, depositCount, receipt, false)

		resolvedAt := func(block uint64) func(*trackerTrackingData) bool {
			return func(d *trackerTrackingData) bool {
				return d.BridgeStatus != nil && d.BridgeStatus.BlockNumber == block
			}
		}
		tracking := waitForTracking(ctx, t, proxyTrackerBaseURL, networkID, tx.Hash(), "resolved at the bridge block",
			resolvedAt(receipt.BlockNumber.Uint64()))
		require.Equal(t, trackingStatusRunning, tracking.TrackingStatus)
		require.EqualValues(t, depositCount, tracking.BridgeStatus.Event.DepositCount)
		require.NotNil(t, tracking.BridgeStatus.BlockFinalized, "bridge_status.block_finalized must be present")
		require.False(t, *tracking.BridgeStatus.BlockFinalized, "the origin block is not final")

		// the origin transaction is re-included in another block: the tracking is resolved again there
		moved := reorgMoving(ctx, t, l2, testEnv.L2.Client, tx.Hash(), 1)
		baseline = requireReorgDetected(ctx, t, bs, networkID, baseline)
		tracking = waitForTracking(ctx, t, proxyTrackerBaseURL, networkID, tx.Hash(), "resolved again at the new block",
			resolvedAt(moved.BlockNumber.Uint64()))
		require.Equal(t, trackingStatusRunning, tracking.TrackingStatus)
		require.EqualValues(t, depositCount, tracking.BridgeStatus.Event.DepositCount)
		require.False(t, *tracking.BridgeStatus.BlockFinalized)

		// the origin transaction disappears: the tracking must not stay "running" on a bridge that does
		// not exist. One more block first, so that the bridge block is head-1 again (and still not final)
		requireBridgeServed(ctx, t, bs, networkID, depositCount, moved, false)
		require.NoError(t, l2.Mine(ctx, 1))
		requireNonFinal(ctx, t, l2, moved.BlockNumber.Uint64())
		reorgDropping(ctx, t, l2, testEnv.L2.Client, tx.Hash())
		requireReorgDetected(ctx, t, bs, networkID, baseline)
		// NOTE: a reorged-out origin tx that is not re-included ends in a terminal error after
		// UnresolvedTimeout, 30s (open decision (c) of #1986: the assertion follows the current behaviour)
		tracking = waitForTracking(ctx, t, proxyTrackerBaseURL, networkID, tx.Hash(), "discarded: not running anymore",
			func(d *trackerTrackingData) bool { return d.TrackingStatus == trackingStatusError })
		require.Nil(t, tracking.BridgeStatus, "the resolved bridge must be discarded with its origin tx")
		require.Nil(t, tracking.AllSteps)
		require.NotNil(t, tracking.Error)
	})

	t.Run("D2 Claimed step goes back and WebSocket subscribers are notified", func(t *testing.T) {
		claimer := *testEnv.L2.Transactor
		bridge := startL1ToL2Bridge(ctx, t, claimer.From)
		bridge.waitInjected(ctx, t)
		const l1NetworkID = 0
		txHash := bridge.receipt.TxHash
		atStep := func(step, claimStatus string) func(*trackerTrackingData) bool {
			return func(d *trackerTrackingData) bool { return d.currentStep() == step && d.ClaimStatus == claimStatus }
		}
		waitForTracking(ctx, t, proxyTrackerBaseURL, l1NetworkID, txHash, "ready to claim",
			atStep(trackerStepWaitingClaim, claimStatusReadyToClaim))

		ws := dialTrackerWS(ctx, t, proxyTrackerBaseURL, l1NetworkID, txHash)
		ws.waitFor(t, "the current status on connect", atStep(trackerStepWaitingClaim, claimStatusReadyToClaim))

		l2.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		claimTx := bridge.sendClaim(ctx, t, &claimer)
		claimReceipt := mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, claimTx)
		requireNonFinal(ctx, t, l2, claimReceipt.BlockNumber.Uint64())
		bridge.requireClaimServed(ctx, t, claimReceipt, false)

		claimed := ws.waitFor(t, "claimed, not final", atStep(trackerStepClaimed, claimStatusClaimed))
		// NOTE: tracking_status only reads "finished" with a final claim (open decision (a) of #1986: the
		// assertion follows the current behaviour)
		require.Equal(t, trackingStatusRunning, claimed.TrackingStatus, "a claim that is not final keeps the tracking running")

		reorgDropping(ctx, t, l2, testEnv.L2.Client, claimTx.Hash())
		requireReorgDetected(ctx, t, bs, networkID, baseline)

		// the same connection, still open, receives the backward move
		back := ws.waitFor(t, "back to WaitingClaim", atStep(trackerStepWaitingClaim, claimStatusReadyToClaim))
		require.Equal(t, trackingStatusRunning, back.TrackingStatus)
		rest := waitForTracking(ctx, t, proxyTrackerBaseURL, l1NetworkID, txHash, "back to WaitingClaim over REST",
			atStep(trackerStepWaitingClaim, claimStatusReadyToClaim))
		require.Equal(t, trackingStatusRunning, rest.TrackingStatus)

		// claimed again and final: the tracking finishes and the server closes the connection
		claimTx2 := bridge.sendClaim(ctx, t, &claimer)
		claimReceipt2 := mineTx(ctx, t, l2, testEnv.L2.Client, claimTx2)
		_, err := l2.MineUntilFinalized(ctx, claimReceipt2.BlockNumber.Uint64())
		require.NoError(t, err)
		final := ws.waitFor(t, "claimed and final", func(d *trackerTrackingData) bool {
			return d.ClaimStatus == claimStatusClaimedFinalized
		})
		require.Equal(t, trackingStatusFinished, final.TrackingStatus)
		require.Equal(t, trackerStepClaimed, final.currentStep())
		ws.requireClosedNormally(t)
	})
}
