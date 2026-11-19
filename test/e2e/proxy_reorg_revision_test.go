package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

// aggkitProxy002Service is the compose service of the second aggkit-proxy instance.
const aggkitProxy002Service = "aggkit-proxy-002"

// TestProxyReorgRevision covers the `revision` scenarios of agglayer/aggkit#1972 (E1-E4): the opaque value
// GET /tracker/v1/activity/from/{address} returns so that a client knows when the pages it loaded are
// stale. It must change when a reorg corrects any entry of the address, on whatever page, and must not
// change on a refresh that changes nothing nor when an entry only becomes final; and two instances that
// hold the same data must report the same value, also after a restart.
func TestProxyReorgRevision(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	l1 := NewAnvilChain("L1", testEnv.Clients.L1)
	l2 := NewAnvilChain("L2", testEnv.L2.Client)
	requireProxyReorgEnv(ctx, t, l1, l2)
	holdAggsender(ctx, t, l2)
	bs := testEnv.L2.BridgeService
	networkID := testEnv.L2.NetworkID
	const l1NetworkID = 0

	t.Run("E1 refreshes with no changes and a not-final to final transition", func(t *testing.T) {
		// An L1 -> L2 bridge with BOTH chains paused: L1 so that the test decides when the bridge becomes
		// final, the L2 so that no GER injection can be mined, which would move the bridge to readyToClaim
		// and change the revision for a reason that is not the one under test.
		sender := newProxyReorgSender(t)
		funder := *testEnv.L1.Transactor
		sender.fundNative(ctx, t, testEnv.Clients.L1, &funder)
		opts := sender.opts(t, testEnv.L1.ChainID)
		opts.Context = ctx
		l2.PauseMiningForTest(ctx, t)
		l1.PauseMiningForTest(ctx, t)
		tx := sendL1Bridge(t, opts, networkID, sender.addr, proxyReorgBridgeAmount)
		receipt := mineTxPlusOne(ctx, t, l1, testEnv.Clients.L1, tx)
		depositCount := l1DepositCount(t, receipt)
		requireNonFinal(ctx, t, l1, receipt.BlockNumber.Uint64())
		requireBridgeServed(ctx, t, bs, l1NetworkID, depositCount, receipt, false)

		listed := func(finalized bool) func(*proxyActivityResponse) bool {
			return func(a *proxyActivityResponse) bool {
				item := a.find(l1NetworkID, depositCount)
				return itemMatchesReceipt(item, receipt) && item.Source == activitySourceBridge &&
					item.isFinalized() == finalized
			}
		}
		before, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "the bridge is listed, not final",
			proxyReorgConvergeTimeout, listed(false))
		require.NoError(t, err)
		requireActivityWellFormed(t, before)
		require.NotEqual(t, emptyActivityRevision, before.Revision)

		// several refreshes that change nothing
		const refreshes = 3
		for i := 0; i < refreshes; i++ {
			refreshed := requireActivityRefreshedAfter(ctx, t, proxyTrackerBaseURL, sender.addr, l1NetworkID,
				depositCount, time.Now())
			require.Equal(t, before.Revision, refreshed.Revision, "a refresh that changes nothing must keep the revision")
			require.False(t, refreshed.find(l1NetworkID, depositCount).isFinalized())
		}

		// the bridge becomes final, and nothing else changes
		_, err = l1.MineUntilFinalized(ctx, receipt.BlockNumber.Uint64())
		require.NoError(t, err)
		requireBridgeServed(ctx, t, bs, l1NetworkID, depositCount, receipt, true)
		final, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "the bridge is final",
			proxyReorgConvergeTimeout, listed(true))
		require.NoError(t, err)
		require.Equal(t, claimStatusPending, final.find(l1NetworkID, depositCount).ClaimStatus,
			"with the L2 paused the bridge cannot become claimable")
		require.Equal(t, before.Revision, final.Revision, "going from not final to final must not change the revision")
	})

	t.Run("E2 E3 claim of an old bridge reorged out while a client paginates", func(t *testing.T) {
		// two bridges of the same sender; with one item per page the newer one is page 1 and the older one,
		// whose claim the reorg removes, is page 2
		claimer := *testEnv.L2.Transactor
		older := startL1ToL2Bridge(ctx, t, claimer.From)
		newer := startL1ToL2BridgeFrom(ctx, t, older.sender, claimer.From)
		newer.waitInjected(ctx, t)
		addr := older.sender.addr
		_, err := waitForActivity(ctx, proxyTrackerBaseURL, addr, "both bridges ready to claim", proxyReorgConvergeTimeout,
			func(a *proxyActivityResponse) bool {
				first, second := a.find(l1NetworkID, older.depositCount), a.find(l1NetworkID, newer.depositCount)
				return a.Count == 2 && first != nil && second != nil &&
					first.ClaimStatus == claimStatusReadyToClaim && second.ClaimStatus == claimStatusReadyToClaim
			})
		require.NoError(t, err)

		l2.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		claimTx := older.sendClaim(ctx, t, &claimer)
		claimReceipt := mineTxPlusOne(ctx, t, l2, testEnv.L2.Client, claimTx)
		older.requireClaimServed(ctx, t, claimReceipt, false)
		older.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusClaimed,
			func(i *proxyActivityItem) bool { return claimIs(i, claimReceipt) })

		// the client loads page 1 ...
		const pageSize = 1
		page1, err := fetchActivityPage(ctx, proxyTrackerBaseURL, addr, 1, pageSize)
		require.NoError(t, err)
		require.Equal(t, 2, page1.Count)
		require.Len(t, page1.Bridges, pageSize)
		require.Equal(t, newer.depositCount, page1.Bridges[0].Bridge.DepositCount, "the newest bridge comes first")

		// ... the claim of the bridge on page 2 is reorged out ...
		reorgDropping(ctx, t, l2, testEnv.L2.Client, claimTx.Hash())
		requireReorgDetected(ctx, t, bs, networkID, baseline)
		older.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim, nil)

		// ... and the client loads page 2: the revision of the last page differs from the one it started
		// with, which is how it knows that what it loaded is stale (E3)
		page2, err := fetchActivityPage(ctx, proxyTrackerBaseURL, addr, 2, pageSize)
		require.NoError(t, err)
		require.Len(t, page2.Bridges, pageSize)
		require.Equal(t, older.depositCount, page2.Bridges[0].Bridge.DepositCount)
		require.Equal(t, claimStatusReadyToClaim, page2.Bridges[0].ClaimStatus)
		require.NotEqual(t, page1.Revision, page2.Revision,
			"a reorg during the traversal must show as a different revision on the last page")

		// page 1 itself holds the same item as before: only the revision tells (E2)
		page1Again, err := fetchActivityPage(ctx, proxyTrackerBaseURL, addr, 1, pageSize)
		require.NoError(t, err)
		require.Equal(t, page1.Count, page1Again.Count, "count does not change: no bridge was added or removed")
		require.Len(t, page1Again.Bridges, pageSize)
		was, is := page1.Bridges[0], page1Again.Bridges[0]
		was.LastUpdatedTimestamp, is.LastUpdatedTimestamp = 0, 0 // advances on every refresh
		require.Equal(t, was, is, "page 1 must not have changed")
		require.Equal(t, page2.Revision, page1Again.Revision, "the revision is the same on every page")
		require.NotEqual(t, page1.Revision, page1Again.Revision,
			"the revision must change although page 1 did not: the change is on page 2")
	})

	t.Run("E4 two proxies on the same data and one of them restarted", func(t *testing.T) {
		require.NoError(t, waitForProxyReady(ctx, proxyReorgProxy2URL))
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		addr := s.sender.addr
		at := func(block uint64) func(*proxyActivityResponse) bool {
			return func(a *proxyActivityResponse) bool {
				item := a.find(s.networkID, s.depositCount)
				return a.Count == 1 && item != nil && item.Bridge.BlockNum == block && item.Source == activitySourceBridge
			}
		}
		// both instances agree before the reorg
		second, err := waitForActivity(ctx, proxyReorgProxy2URL, addr, "the bridge is listed by the second proxy",
			proxyReorgConvergeTimeout, at(s.receipt.BlockNumber.Uint64()))
		require.NoError(t, err)
		require.Equal(t, s.before.Revision, second.Revision, "same data, same revision on both instances")

		// a reorg moves the bridge: both converge to the same new revision
		moved := reorgMoving(ctx, t, l2, testEnv.L2.Client, s.receipt.TxHash, 1)
		s.requireReorgDetected(ctx, t)
		requireBridgeServed(ctx, t, s.bs, s.networkID, s.depositCount, moved, false)
		first := s.waitForActivity(ctx, t, "the first proxy lists the bridge at its new block", at(moved.BlockNumber.Uint64()))
		second, err = waitForActivity(ctx, proxyReorgProxy2URL, addr, "the second proxy lists the bridge at its new block",
			proxyReorgConvergeTimeout, at(moved.BlockNumber.Uint64()))
		require.NoError(t, err)
		require.NotEqual(t, s.before.Revision, first.Revision)
		require.Equal(t, first.Revision, second.Revision, "once converged both instances report the same revision")

		// the second proxy is restarted: it rebuilds the address from scratch and reports the same value
		require.NoError(t, testEnv.RestartService(ctx, aggkitProxy002Service))
		require.NoError(t, waitForProxyReady(ctx, proxyReorgProxy2URL))
		restarted, err := waitForActivity(ctx, proxyReorgProxy2URL, addr, "the restarted proxy lists the bridge",
			proxyReorgConvergeTimeout, at(moved.BlockNumber.Uint64()))
		require.NoError(t, err)
		requireActivityWellFormed(t, restarted)
		require.Equal(t, second.Revision, restarted.Revision, "the revision must be the same after a restart")
		log.Infof("E4: revision %s on both proxies and after the restart", restarted.Revision)
	})
}
