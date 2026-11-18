package e2e

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// TestProxyReorgActivity covers the bridge scenarios of agglayer/aggkit#1972 (A1-A6): real reorgs on the
// anvil chains that remove, move or replace a bridge that is not final yet, and what
// GET /tracker/v1/activity/from/{address} of the aggkit-proxy reports before and after.
//
// aggkit-001 syncs its L2 (network 1) and L1 at LatestBlock, so its bridge service serves bridges of
// blocks that are not final and the proxy lists them with finalized=false. aggkit-002 syncs its L2
// (network 2) at FinalizedBlock: a bridge of a non-final block of network 2 reaches the proxy only through
// its RPC fallback (A5).
//
// Every scenario uses a sender created for it, so the activity starts empty (count 0 and the documented
// empty revision) and every count and revision is exact. See proxy_reorg_common_test.go for the order of
// the assertions (aggkit first, then the proxy).
func TestProxyReorgActivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()

	l1 := NewAnvilChain("L1", testEnv.Clients.L1)
	l2 := NewAnvilChain("L2", testEnv.L2.Client)
	requireProxyReorgEnv(ctx, t, l1, l2)
	l2b := NewAnvilChain("L2B", testEnv.L2B.Client)
	requireAnvilChains(ctx, t, l2b)

	t.Run("A1 bridge from network 1 reorged out before it is final", func(t *testing.T) {
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		reorgDropping(ctx, t, l2, testEnv.L2.Client, s.receipt.TxHash)
		s.requireReorgDetected(ctx, t)
		require.NoError(t, WaitForBridgeAbsent(ctx, s.bs, s.networkID, s.depositCount, proxyReorgServeTimeout))

		after := s.waitForActivity(ctx, t, "the reorged-out bridge is gone",
			func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.Empty(t, after.Bridges)
		require.NotEqual(t, s.before.Revision, after.Revision, "removing a bridge must change the revision")
		require.Equal(t, emptyActivityRevision, after.Revision, "the address has no bridges again")
	})

	t.Run("A2 same bridge re-included in another block", func(t *testing.T) {
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		moved := reorgMoving(ctx, t, l2, testEnv.L2.Client, s.receipt.TxHash, 1)
		require.Equal(t, s.receipt.BlockNumber.Uint64()+1, moved.BlockNumber.Uint64())
		require.Equal(t, s.depositCount, s.bridger.depositCount(t, moved), "the re-included bridge keeps its deposit count")
		s.requireReorgDetected(ctx, t)
		requireBridgeServed(ctx, t, s.bs, s.networkID, s.depositCount, moved, false)

		after := s.waitForActivity(ctx, t, "the bridge is listed at its new block",
			func(a *proxyActivityResponse) bool {
				return itemMatchesReceipt(a.find(s.networkID, s.depositCount), moved)
			})
		require.Equal(t, 1, after.Count, "a corrected bridge must stay a single entry: %s", after)
		item := after.find(s.networkID, s.depositCount)
		require.Equal(t, s.item.Bridge.GlobalIndex, item.Bridge.GlobalIndex)
		require.False(t, item.isFinalized(), "the new block is not final either")
		require.NotEqual(t, s.before.Revision, after.Revision, "a bridge moved to another block must change the revision")
	})

	t.Run("A3 bridge replaced by a different one with the same deposit count", func(t *testing.T) {
		s := startL2ActivityScenario(ctx, t, l2, &testEnv.L2)
		// a different bridge of the same sender, signed with the nonce of the first one and never sent: the
		// reorg puts it where the first one was, so it takes its deposit count
		const replacementAmount = 3 * proxyReorgBridgeAmount
		replacement := s.bridger.sign(t, replacementAmount, s.tx.Nonce(), s.tx)
		require.NotEqual(t, s.tx.Hash(), replacement.Hash())
		replaced := reorgReplacing(ctx, t, l2, testEnv.L2.Client, s.tx.Hash(), replacement)
		require.Equal(t, s.depositCount, s.bridger.depositCount(t, replaced), "the replacement takes the deposit count")
		s.requireReorgDetected(ctx, t)
		requireBridgeServed(ctx, t, s.bs, s.networkID, s.depositCount, replaced, false)

		after := s.waitForActivity(ctx, t, "the entry holds the replacement bridge",
			func(a *proxyActivityResponse) bool {
				return itemMatchesReceipt(a.find(s.networkID, s.depositCount), replaced)
			})
		require.Equal(t, 1, after.Count, "the replaced bridge must not leave a second entry: %s", after)
		item := after.find(s.networkID, s.depositCount)
		require.Equal(t, s.item.Bridge.GlobalIndex, item.Bridge.GlobalIndex, "same deposit count, same global index")
		require.Equal(t, strconv.FormatInt(replacementAmount, 10), item.Bridge.Amount.ToBigInt().String(), "the entry must hold the new amount")
		require.NotEqual(t, s.item.Bridge.BridgeHash, item.Bridge.BridgeHash)
		require.NotEqual(t, s.before.Revision, after.Revision, "a replaced bridge must change the revision")
	})

	t.Run("A4 L1 to L2 bridge reorged out of L1", func(t *testing.T) {
		sender := newProxyReorgSender(t)
		funder := *testEnv.L1.Transactor
		sender.fundNative(ctx, t, testEnv.Clients.L1, &funder)
		opts := sender.opts(t, testEnv.L1.ChainID)
		opts.Context = ctx
		bs := testEnv.L2.BridgeService // aggkit-001 also serves L1 (network 0)
		const l1NetworkID = 0

		empty, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "empty before the bridge",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.NoError(t, err)
		require.Equal(t, emptyActivityRevision, empty.Revision)

		l1.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, l1NetworkID)
		tx := sendL1Bridge(t, opts, testEnv.L2.NetworkID, sender.addr, proxyReorgBridgeAmount)
		receipt := mineTxPlusOne(ctx, t, l1, testEnv.Clients.L1, tx)
		depositCount := l1DepositCount(t, receipt)
		requireNonFinal(ctx, t, l1, receipt.BlockNumber.Uint64())
		requireBridgeServed(ctx, t, bs, l1NetworkID, depositCount, receipt, false)

		before, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "the L1 bridge is listed by the bridge service",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool {
				item := a.find(l1NetworkID, depositCount)
				return itemMatchesReceipt(item, receipt) && item.Source == activitySourceBridge
			})
		require.NoError(t, err)
		requireActivityWellFormed(t, before)
		require.Equal(t, 1, before.Count)
		require.False(t, before.find(l1NetworkID, depositCount).isFinalized(), "a bridge of a non-final L1 block is not final")

		reorgDropping(ctx, t, l1, testEnv.Clients.L1, tx.Hash())
		requireReorgDetected(ctx, t, bs, l1NetworkID, baseline)
		require.NoError(t, WaitForBridgeAbsent(ctx, bs, l1NetworkID, depositCount, proxyReorgServeTimeout))

		after, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "the reorged-out L1 bridge is gone",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.NoError(t, err)
		require.NotEqual(t, before.Revision, after.Revision, "removing a bridge must change the revision")
		require.Equal(t, emptyActivityRevision, after.Revision)
	})

	t.Run("A5 bridge from network 2 seen only through the RPC fallback, reorged out", func(t *testing.T) {
		l2bEnv := testEnv.L2B
		sender := newProxyReorgSender(t)
		bridger := newL2Bridger(ctx, t, l2bEnv, sender)
		bs := l2bEnv.BridgeService // aggkit-002: syncs network 2 at FinalizedBlock
		networkID := l2bEnv.NetworkID

		empty, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "empty before the bridge",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.NoError(t, err)
		require.Equal(t, emptyActivityRevision, empty.Revision)

		l2b.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, networkID)
		tx := bridger.send(t, proxyReorgBridgeAmount)
		receipt := mineTxPlusOne(ctx, t, l2b, l2bEnv.Client, tx)
		depositCount := bridger.depositCount(t, receipt)
		requireNonFinal(ctx, t, l2b, receipt.BlockNumber.Uint64())

		before, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "the bridge is listed through the RPC fallback",
			proxyReorgConvergeTimeout,
			func(a *proxyActivityResponse) bool {
				return itemMatchesReceipt(a.find(networkID, depositCount), receipt)
			})
		require.NoError(t, err)
		requireActivityWellFormed(t, before)
		require.Equal(t, 1, before.Count)
		item := before.find(networkID, depositCount)
		require.Equal(t, activitySourceRPC, item.Source, "aggkit-002 does not index non-final blocks: only the RPC fallback sees it")
		require.False(t, item.isFinalized(), "a bridge only found through the RPC fallback is never final")
		// the control: the bridge service of network 2 does not serve the bridge (the chain is paused and
		// the block is not final, so it cannot have indexed it)
		_, err = bs.GetBridgeByDepositCount(ctx, networkID, depositCount)
		require.ErrorIs(t, err, client.ErrNotFound, "aggkit-002 syncs at FinalizedBlock: it must not serve a non-final bridge")

		reorgDropping(ctx, t, l2b, l2bEnv.Client, tx.Hash())

		after, err := waitForActivity(ctx, proxyTrackerBaseURL, sender.addr, "the reorged-out bridge is forgotten",
			proxyReorgConvergeTimeout, func(a *proxyActivityResponse) bool { return a.Count == 0 })
		require.NoError(t, err)
		require.NotEqual(t, before.Revision, after.Revision, "removing a bridge must change the revision")
		require.Equal(t, emptyActivityRevision, after.Revision)
		// the one scenario where aggkit has nothing to detect: aggkit-002 never tracked the reorged blocks,
		// so it reports no reorg and never served the bridge. The proxy forgot it on its own (RPC window).
		event, err := LastReorgEvent(ctx, bs, networkID)
		require.NoError(t, err)
		require.Equal(t, baseline, event, "aggkit-002 does not track non-final blocks, it cannot report this reorg")
		_, err = bs.GetBridgeByDepositCount(ctx, networkID, depositCount)
		require.ErrorIs(t, err, client.ErrNotFound)
	})

	t.Run("A6 finalized bridge, shallower reorg", func(t *testing.T) {
		// An L1 -> L2 bridge that nobody claims, left to settle in readyToClaim with its L1 block final:
		// from then on nothing about it changes on its own, so whatever changes after the reorg is the
		// proxy's doing. (A bridge from the L2 would not do: its claim_status moves to readyToClaim
		// whenever the certificate that carries it settles, which changes the revision legitimately.)
		const l1NetworkID = 0
		bs := testEnv.L2.BridgeService // aggkit-001 also serves L1
		bridge := startL1ToL2Bridge(ctx, t, testEnv.L2.Transactor.From)
		bridge.waitInjected(ctx, t)
		requireBridgeServed(ctx, t, bs, l1NetworkID, bridge.depositCount, bridge.receipt, true)
		// NOTE: an unclaimed bridge in a final block reads finalized=true (open decision (b) of #1986: the
		// assertion follows the current behaviour)
		final, finalItem := bridge.waitForStatus(ctx, t, proxyTrackerBaseURL, claimStatusReadyToClaim,
			func(i *proxyActivityItem) bool { return i.isFinalized() })
		require.Equal(t, 1, final.Count)
		require.True(t, itemMatchesReceipt(finalItem, bridge.receipt))

		// another sender provides the non-final L1 bridge that the reorg removes, above the final one
		other := newProxyReorgSender(t)
		funder := *testEnv.L1.Transactor
		other.fundNative(ctx, t, testEnv.Clients.L1, &funder)
		otherOpts := other.opts(t, testEnv.L1.ChainID)
		otherOpts.Context = ctx
		l1.PauseMiningForTest(ctx, t)
		baseline := reorgBaseline(ctx, t, bs, l1NetworkID)
		otherTx := sendL1Bridge(t, otherOpts, testEnv.L2.NetworkID, other.addr, proxyReorgBridgeAmount)
		otherReceipt := mineTxPlusOne(ctx, t, l1, testEnv.Clients.L1, otherTx)
		otherDeposit := l1DepositCount(t, otherReceipt)
		require.Greater(t, otherReceipt.BlockNumber.Uint64(), bridge.receipt.BlockNumber.Uint64()+proxyReorgDepth)
		requireBridgeServed(ctx, t, bs, l1NetworkID, otherDeposit, otherReceipt, false)

		reorgDropping(ctx, t, l1, testEnv.Clients.L1, otherTx.Hash())
		reorgedAt := time.Now()
		requireReorgDetected(ctx, t, bs, l1NetworkID, baseline)
		require.NoError(t, WaitForBridgeAbsent(ctx, bs, l1NetworkID, otherDeposit, proxyReorgServeTimeout))

		// the proxy refreshed the address of the final bridge after the reorg, and nothing changed
		after := requireActivityRefreshedAfter(ctx, t, proxyTrackerBaseURL, bridge.sender.addr, l1NetworkID,
			bridge.depositCount, reorgedAt)
		requireActivityWellFormed(t, after)
		require.Equal(t, 1, after.Count)
		item := after.find(l1NetworkID, bridge.depositCount)
		require.True(t, itemMatchesReceipt(item, bridge.receipt), "the final bridge must be unchanged: %s", item)
		require.True(t, item.isFinalized())
		require.Equal(t, claimStatusReadyToClaim, item.ClaimStatus)
		require.Equal(t, finalItem.CreationTimestamp, item.CreationTimestamp)
		require.Equal(t, final.Revision, after.Revision, "a reorg above a final bridge must not change the revision")
		log.Infof("A6: final bridge unchanged after a shallower reorg (revision %s)", after.Revision)
	})
}

// l2ActivityScenario is the common start of the scenarios on an L2 served at LatestBlock: a fresh sender
// whose only bridge sits in a non-final block (head-1), served by aggkit and listed by the proxy.
type l2ActivityScenario struct {
	chain     *AnvilChain
	bs        *client.Client
	networkID uint32
	sender    *proxyReorgSender
	bridger   *l2Bridger
	// tx and receipt are the bridge, depositCount its deposit count
	tx           *ethtypes.Transaction
	receipt      *ethtypes.Receipt
	depositCount uint32
	// baseline is aggkit's last reorg event before the scenario provokes one
	baseline bridgesync.LastReorg
	// before is the proxy's activity with the bridge listed (not final), item the bridge's entry in it
	before *proxyActivityResponse
	item   *proxyActivityItem
}

// startL2ActivityScenario creates the sender, checks its activity is empty, pauses the chain (restored
// when the test ends), mines one bridge of the sender with a block on top and waits until aggkit serves it
// and the proxy lists it, from the bridge service, as not final.
func startL2ActivityScenario(ctx context.Context, t *testing.T, chain *AnvilChain, l2 *envs.L2Config) *l2ActivityScenario {
	t.Helper()
	// a previous scenario of the same test may have left the chain paused: the setup needs it mining
	require.NoError(t, chain.SetMiningMode(ctx, MiningMode{IntervalSeconds: 1}))
	sender := newProxyReorgSender(t)
	s := &l2ActivityScenario{
		chain: chain, bs: l2.BridgeService, networkID: l2.NetworkID, sender: sender,
		bridger: newL2Bridger(ctx, t, l2, sender),
	}
	empty := s.waitForActivity(ctx, t, "empty before the bridge", func(a *proxyActivityResponse) bool { return a.Count == 0 })
	require.Equal(t, emptyActivityRevision, empty.Revision, "an address with no bridges has the empty revision")

	chain.PauseMiningForTest(ctx, t)
	s.baseline = reorgBaseline(ctx, t, s.bs, s.networkID)
	s.tx = s.bridger.send(t, proxyReorgBridgeAmount)
	s.receipt = mineTxPlusOne(ctx, t, chain, l2.Client, s.tx)
	s.depositCount = s.bridger.depositCount(t, s.receipt)
	requireNonFinal(ctx, t, chain, s.receipt.BlockNumber.Uint64())
	requireBridgeServed(ctx, t, s.bs, s.networkID, s.depositCount, s.receipt, false)

	s.before = s.waitForActivity(ctx, t, "the bridge is listed by the bridge service", func(a *proxyActivityResponse) bool {
		item := a.find(s.networkID, s.depositCount)
		return itemMatchesReceipt(item, s.receipt) && item.Source == activitySourceBridge
	})
	require.Equal(t, 1, s.before.Count)
	s.item = s.before.find(s.networkID, s.depositCount)
	require.False(t, s.item.isFinalized(), "a bridge of a non-final block must be listed as not final: %s", s.item)
	require.Equal(t, claimStatusPending, s.item.ClaimStatus)
	require.NotNil(t, s.item.Bridge.FromAddress)
	require.Equal(t, sender.addr, common.HexToAddress(string(*s.item.Bridge.FromAddress)))
	require.NotEqual(t, emptyActivityRevision, s.before.Revision, "adding a bridge must change the revision")
	return s
}

// waitForActivity waits for the sender's activity on aggkit-proxy-001 to match, and checks it is well formed.
func (s *l2ActivityScenario) waitForActivity(
	ctx context.Context, t *testing.T, what string, match func(*proxyActivityResponse) bool,
) *proxyActivityResponse {
	t.Helper()
	activity, err := waitForActivity(ctx, proxyTrackerBaseURL, s.sender.addr, what, proxyReorgConvergeTimeout, match)
	require.NoError(t, err)
	requireActivityWellFormed(t, activity)
	return activity
}

// requireReorgDetected requires aggkit to report a reorg newer than the scenario's baseline.
func (s *l2ActivityScenario) requireReorgDetected(ctx context.Context, t *testing.T) {
	t.Helper()
	s.baseline = requireReorgDetected(ctx, t, s.bs, s.networkID, s.baseline)
}
