package e2e

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

const (
	// reorgSmokeDepth is the depth of every reorg of the test: it replaces the block with the bridge
	// event and the block mined after it (see runReorgScenario for why both).
	reorgSmokeDepth uint64 = 2
	// reorgSmokeServeTimeout bounds how long aggkit takes to serve (or stop serving) a bridge after the
	// block was mined or reorged: the syncers poll for new blocks every 3s.
	reorgSmokeServeTimeout = 90 * time.Second
	// reorgSmokeDetectTimeout bounds how long aggkit takes to report a reorg: one reorg-detector tick (2s)
	// plus the time the bridge-service needs to answer. Far above the ~2s measured on the snapshot env.
	reorgSmokeDetectTimeout = 60 * time.Second
	// reorgSmokeStableFor is how long a bridge that survived a reorg must be served without interruption
	// before it is considered re-indexed: longer than the syncers' 3s poll period.
	reorgSmokeStableFor = 6 * time.Second
	// reorgSmokeAbsentFor is how long a bridge dropped by a reorg must stay unserved.
	reorgSmokeAbsentFor = 5 * time.Second
	// reorgSmokeBridgeAmount is the amount of every bridge sent by the test (0.0001 of the native/ERC20 unit).
	reorgSmokeBridgeAmount = 1e14
	// reorgSmokeBlockSpacing is the timestamp gap, in seconds, between the block with the bridge and its
	// parent: >= 2 guarantees that a replacement block (timestamped like the common ancestor) differs.
	reorgSmokeBlockSpacing = 2
	// reorgSmokeMinFinalityLag is the minimum finality lag the scenario needs: the bridge block is mined
	// with one more block on top, and must still be above the finalized one.
	reorgSmokeMinFinalityLag = 2
)

// reorgScenario is one chain (L1 or an L2) under test and how to bridge from it.
type reorgScenario struct {
	chain *AnvilChain
	// bs is the DIRECT bridge-service of aggkit-001 (not the aggkit-proxy), so a failure points at the
	// syncers and not at the proxy.
	bs *client.Client
	// networkID is the id of the chain in the bridge-service API: 0 for L1, the L2 network id.
	networkID uint32
	// nativeMD is true when the bridge syncer of this chain runs on the multidownloader (the L1
	// bridge and claim syncers do, see bridgesync_multidownloader.go): its reported reorg range is
	// the range of headers it had stored, not exactly the reorged blocks, so requireReorgEventWithin
	// only requires it to cover the reorged blocks instead of lying within them. It is false for the
	// legacy L2 syncer, which reports the exact reorged range.
	nativeMD bool
	// client is the chain's RPC client.
	client interface {
		TransactionReceipt(ctx context.Context, txHash common.Hash) (*ethtypes.Receipt, error)
	}
	// sendBridge sends a bridge transaction from the chain and returns it WITHOUT waiting for it to be
	// mined: the chain is paused, the test mines it.
	sendBridge func(ctx context.Context) (*ethtypes.Transaction, error)
	// depositCount returns the deposit count of the bridge created by a mined bridge transaction.
	depositCount func(receipt *ethtypes.Receipt) (uint32, error)
}

// TestReorgDetectionSmoke provokes reorgs on L1 and on an L2 of the anvil env and asserts that aggkit-001's
// bridge-service reports them (GET /bridge/v1/last-reorg-event) and keeps serving consistent data.
//
// aggkit-001 syncs its L2 (and the L1 bridge syncer) at LatestBlock, so it serves data of blocks that are
// not final yet; it is the instance that sees reorgs. For each chain, with mining paused so that the
// non-final window is under the test's control instead of racing the 1s block time:
//
//  1. a bridge mined in a non-final block is served with block_finalized=false;
//  2. a reorg that keeps the transaction (new block hashes, same transactions) is reported and the bridge
//     is served unchanged afterwards;
//  3. a reorg that drops the transaction is reported and the bridge stops being served, while the bridges
//     served before are unchanged and the syncer gets back in sync;
//  4. a new bridge is served with block_finalized=false, and with block_finalized=true once enough blocks
//     are mined for its block to be finalized.
//
// Reorgs are always provoked on a block that holds a bridge event that aggkit already served: syncers only
// track the blocks with events and the head they saw at each poll, so a reorg of untracked head blocks on a
// chain that keeps mining is detected only by chance.
func TestReorgDetectionSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	l1 := NewAnvilChain("L1", testEnv.Clients.L1)
	l2 := NewAnvilChain("L2", testEnv.L2.Client)
	for _, chain := range []*AnvilChain{l1, l2} {
		supported, err := chain.Supported(ctx)
		require.NoError(t, err, "probe the anvil_* RPCs of %s", chain.Name())
		if !supported {
			t.Skipf("%s is not an anvil node: the reorg helpers need the anvil_* RPCs (env anvil-2chains)", chain.Name())
		}
	}

	t.Run("L2 (aggkit-001 syncs at LatestBlock)", func(t *testing.T) {
		runReorgScenario(ctx, t, l2BridgeScenario(ctx, t, l2))
	})
	t.Run("L1", func(t *testing.T) {
		runReorgScenario(ctx, t, l1BridgeScenario(ctx, t, l1))
	})
}

// l2BridgeScenario bridges a MintableERC20 from the L2 to L1 (L2-native tokens bypass the Local Balance Tree
// underflow check, see testmain_test.go). The mint and the approve are mined here, with the chain still
// producing blocks, because the scenario pauses it afterwards.
func l2BridgeScenario(ctx context.Context, t *testing.T, chain *AnvilChain) reorgScenario {
	t.Helper()
	l2 := testEnv.L2
	opts, key, err := l2.Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { l2.Keys.Return(key) })

	// Enough balance and allowance for the bridges of the scenario, with slack.
	amount := new(big.Int).Mul(big.NewInt(reorgSmokeBridgeAmount), big.NewInt(3))
	mintTx, err := l2.Contracts.MintableERC20.Mint(opts, opts.From, amount)
	require.NoError(t, err)
	_, err = bind.WaitMined(ctx, l2.Client, mintTx)
	require.NoError(t, err, "wait for the ERC20 mint")
	approveTx, err := l2.Contracts.MintableERC20.Approve(opts, l2.Contracts.L2BridgeAddress, amount)
	require.NoError(t, err)
	_, err = bind.WaitMined(ctx, l2.Client, approveTx)
	require.NoError(t, err, "wait for the ERC20 approve")

	return reorgScenario{
		chain:     chain,
		bs:        l2.BridgeService,
		networkID: l2.NetworkID,
		client:    l2.Client,
		sendBridge: func(ctx context.Context) (*ethtypes.Transaction, error) {
			return l2.Contracts.L2Bridge.BridgeAsset(
				opts, 0, opts.From, big.NewInt(reorgSmokeBridgeAmount), l2.Contracts.MintableERC20Address, true, nil)
		},
		depositCount: func(receipt *ethtypes.Receipt) (uint32, error) {
			// An ERC20 bridge also emits a Transfer log: look for the BridgeEvent among all of them.
			for _, lg := range receipt.Logs {
				if event, err := l2.Contracts.L2Bridge.ParseBridgeEvent(*lg); err == nil {
					return event.DepositCount, nil
				}
			}
			return 0, fmt.Errorf("no BridgeEvent in the logs of %s", receipt.TxHash.Hex())
		},
	}
}

// l1BridgeScenario bridges native ETH from L1 to the L2 (nobody claims it: the scenario is about what the
// bridge-service serves, not about claims).
func l1BridgeScenario(ctx context.Context, t *testing.T, chain *AnvilChain) reorgScenario {
	t.Helper()
	opts, key, err := testEnv.Keys.L1Keys.Checkout()
	require.NoError(t, err)
	t.Cleanup(func() { testEnv.Keys.L1Keys.Return(key) })

	return reorgScenario{
		chain:     chain,
		bs:        testEnv.L2.BridgeService,
		networkID: 0,
		// the L1 bridge syncer is registered in the multidownloader (bridgesync_multidownloader.go),
		// so its reorg range can be wider than the reorged blocks: see nativeMD.
		nativeMD: true,
		client:   testEnv.Clients.L1,
		sendBridge: func(ctx context.Context) (*ethtypes.Transaction, error) {
			amount := big.NewInt(reorgSmokeBridgeAmount)
			opts.Value = amount
			opts.GasLimit = l1BridgeGasLimit
			defer func() {
				opts.Value = nil
				opts.GasLimit = 0
			}()
			return testEnv.L1.Contracts.Bridge.BridgeAsset(
				opts, testEnv.L2.NetworkID, opts.From, amount, common.Address{}, true, nil)
		},
		depositCount: func(receipt *ethtypes.Receipt) (uint32, error) {
			for _, lg := range receipt.Logs {
				if event, err := testEnv.L1.Contracts.Bridge.ParseBridgeEvent(*lg); err == nil {
					return event.DepositCount, nil
				}
			}
			return 0, fmt.Errorf("no BridgeEvent in the logs of %s", receipt.TxHash.Hex())
		},
	}
}

// minedBridge is a bridge transaction mined by the scenario.
type minedBridge struct {
	tx           *ethtypes.Transaction
	block        uint64
	depositCount uint32
}

// mineBridge sends a bridge transaction, mines it in a block of its own and mines one more block on top.
// The extra block matters for the reorg: the syncer always tracks the head block it polled, so a reorg
// that replaced the bridge's block alone would race the syncer re-tracking that block under its new hash
// (and then see no mismatch). With the bridge in head-1, its tracked hash is only ever compared by the
// reorg detector, which makes the detection deterministic.
func (s reorgScenario) mineBridge(ctx context.Context, t *testing.T) minedBridge {
	t.Helper()
	tx, err := s.sendBridge(ctx)
	require.NoError(t, err, "send the bridge transaction")
	// Spaced from the previous block so that the replacement of this block built by the reorg (it gets the
	// timestamp of the common ancestor) is never identical to it, which would make the reorg a no-op.
	require.NoError(t, s.chain.MineSpaced(ctx, 1, reorgSmokeBlockSpacing))
	receipt, err := s.client.TransactionReceipt(ctx, tx.Hash())
	require.NoError(t, err, "bridge transaction not mined by the manual block")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "bridge transaction reverted")
	depositCount, err := s.depositCount(receipt)
	require.NoError(t, err)
	require.NoError(t, s.chain.Mine(ctx, 1))

	bridge := minedBridge{tx: tx, block: receipt.BlockNumber.Uint64(), depositCount: depositCount}
	log.Infof("[%s] bridge tx %s mined at block %d (deposit_count=%d), head now %d",
		s.chain.Name(), tx.Hash().Hex(), bridge.block, depositCount, bridge.block+1)
	return bridge
}

// served matches the bridge-service answer for b: same deposit count, block and transaction.
func (b minedBridge) served(bridge *types.BridgeResponse, finalized bool) bool {
	return bridge.DepositCount == b.depositCount &&
		bridge.BlockNum == b.block &&
		common.HexToHash(string(bridge.TxHash)) == b.tx.Hash() &&
		bridge.BlockFinalized != nil && *bridge.BlockFinalized == finalized
}

// requireServedBeforeUnchanged fails if a bridge that was served before the scenario changed or vanished.
func (s reorgScenario) requireServedBeforeUnchanged(
	ctx context.Context, t *testing.T, before map[uint32]ServedBridge,
) {
	t.Helper()
	after, err := ListServedBridges(ctx, s.bs, s.networkID)
	require.NoError(t, err)
	for depositCount, bridge := range before {
		require.Equal(t, bridge, after[depositCount],
			"bridge deposit_count=%d served before the reorg changed or vanished", depositCount)
	}
}

func runReorgScenario(ctx context.Context, t *testing.T, s reorgScenario) {
	t.Helper()
	chain := s.chain

	// What the bridge-service serves and reports before anything is done: the baseline every
	// "consistent data" and "new event" assertion compares with.
	servedBefore, err := ListServedBridges(ctx, s.bs, s.networkID)
	require.NoError(t, err)
	baselineEvent, err := LastReorgEvent(ctx, s.bs, s.networkID)
	require.NoError(t, err)
	log.Infof("[%s] baseline: %d bridges served, last reorg event %+v", chain.Name(), len(servedBefore), baselineEvent)

	// The non-final window is held by pausing the chain: nothing is mined unless the test mines it.
	chain.PauseMiningForTest(ctx, t)
	lag, err := chain.FinalityLag(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, lag, uint64(reorgSmokeMinFinalityLag),
		"the finality lag of %s changed: the scenario needs the bridge block to stay non-final with a block on top",
		chain.Name())

	// 1. A bridge in a non-final block is served as such.
	bridge := s.mineBridge(ctx, t)
	finalized, err := chain.FinalizedHead(ctx)
	require.NoError(t, err)
	require.Less(t, finalized.Number, bridge.block, "the bridge block must not be finalized yet")
	served, err := WaitForBridgeBlockFinalized(ctx, s.bs, s.networkID, bridge.depositCount, false, reorgSmokeServeTimeout)
	require.NoError(t, err, "aggkit-001 must serve a bridge of a non-final block (it syncs at LatestBlock)")
	require.True(t, bridge.served(served, false), "served bridge %+v is not the one mined", served)

	// 2. A reorg that keeps the transaction: the hashes of the blocks change, the bridge must survive it.
	headBefore, err := chain.Head(ctx)
	require.NoError(t, err)
	require.NoError(t, chain.ReorgKeepingTxs(ctx, reorgSmokeDepth))
	headAfter, err := chain.Head(ctx)
	require.NoError(t, err)
	require.Equal(t, headBefore.Number, headAfter.Number, "the reorg must keep the head number")
	require.NotEqual(t, headBefore.Hash, headAfter.Hash, "the reorg must change the head hash")
	receipt, err := s.client.TransactionReceipt(ctx, bridge.tx.Hash())
	require.NoError(t, err, "the reorg must re-include the bridge transaction")
	require.Equal(t, bridge.block, receipt.BlockNumber.Uint64())

	event, err := WaitForReorgEvent(ctx, s.bs, s.networkID, baselineEvent, reorgSmokeDetectTimeout)
	require.NoError(t, err, "aggkit-001 did not report the reorg of %s (a syncer problem, not a proxy one)", chain.Name())
	log.Infof("[%s] reorg #1 reported: %+v", chain.Name(), event)
	requireReorgEventWithin(t, event, bridge.block, bridge.block+reorgSmokeDepth-1, s.nativeMD)
	require.NoError(t, WaitForBridgeStable(ctx, s.bs, s.networkID, bridge.depositCount,
		func(b *types.BridgeResponse) bool { return bridge.served(b, false) },
		reorgSmokeStableFor, reorgSmokeServeTimeout),
		"the bridge of a reorg that kept its transaction must be served unchanged afterwards")
	require.NoError(t, WaitForBridgeSynced(ctx, s.bs, s.networkID, reorgSmokeServeTimeout))
	s.requireServedBeforeUnchanged(ctx, t, servedBefore)

	// 3. A reorg that drops the transaction: the bridge must stop being served.
	require.NoError(t, chain.ReorgKeepingTxs(ctx, reorgSmokeDepth, bridge.tx.Hash()))
	_, err = s.client.TransactionReceipt(ctx, bridge.tx.Hash())
	require.ErrorIs(t, err, ethereum.NotFound, "the reorg must drop the bridge transaction")

	event2, err := WaitForReorgEvent(ctx, s.bs, s.networkID, event, reorgSmokeDetectTimeout)
	require.NoError(t, err, "aggkit-001 did not report the second reorg of %s", chain.Name())
	log.Infof("[%s] reorg #2 reported: %+v", chain.Name(), event2)
	requireReorgEventWithin(t, event2, bridge.block, bridge.block+reorgSmokeDepth-1, s.nativeMD)
	require.NoError(t, WaitForBridgeAbsent(ctx, s.bs, s.networkID, bridge.depositCount, reorgSmokeServeTimeout),
		"the bridge of a dropped transaction must stop being served")
	require.NoError(t, RequireBridgeStaysAbsent(ctx, s.bs, s.networkID, bridge.depositCount, reorgSmokeAbsentFor))
	require.NoError(t, WaitForBridgeSynced(ctx, s.bs, s.networkID, reorgSmokeServeTimeout),
		"the bridge syncer must get back in sync after the reorg")
	s.requireServedBeforeUnchanged(ctx, t, servedBefore)

	// 4. block_finalized goes false -> true as blocks are mined on top.
	next := s.mineBridge(ctx, t)
	// The dropped bridge's deposit count is reused only because nobody else bridges on this chain meanwhile
	// (mining is paused and the CI group runs this test alone): a second bridging actor breaks this check.
	require.Equal(t, bridge.depositCount, next.depositCount, "the dropped bridge's deposit count is reused")
	served, err = WaitForBridgeBlockFinalized(ctx, s.bs, s.networkID, next.depositCount, false, reorgSmokeServeTimeout)
	require.NoError(t, err)
	require.True(t, next.served(served, false), "served bridge %+v is not the one mined", served)

	mined, err := chain.MineUntilFinalized(ctx, next.block)
	require.NoError(t, err)
	log.Infof("[%s] mined %d blocks to finalize block %d (lag %d)", chain.Name(), mined, next.block, lag)
	served, err = WaitForBridgeBlockFinalized(ctx, s.bs, s.networkID, next.depositCount, true, reorgSmokeServeTimeout)
	require.NoError(t, err, "block_finalized must become true once the bridge block is finalized")
	require.True(t, next.served(served, true), "served bridge %+v is not the one mined", served)
	s.requireServedBeforeUnchanged(ctx, t, servedBefore)
}

// requireReorgEventWithin checks that the reported event is an acceptable report of the reorg of the blocks
// [first, last]. It does not require it to start at the bridge block: GET /bridge/v1/last-reorg-event
// returns one row of the reorg detector's reorg_event table, which holds one event per subscribed syncer
// (the bridge syncer's starts at the bridge block, the claim syncer's at the head it tracked) and orders
// them by detection second only, so which of the events of one detection the endpoint returns is arbitrary.
//
// nativeMD selects the multidownloader semantics (the L1 bridge and claim syncers): the detector walks back
// from the offending block to the first STORED header that still matches the chain, so when headers are
// sparse (only the blocks with events plus the polled head are stored) the rollback can widen FromBlock
// below the first reorged block, and ToBlock is the last stored block, at or above the last reorged block.
// The reorg is verified by the detector's own hash comparison (TrackedHash vs CurrentHash), so the range
// only has to cover the reorged blocks there, not lie within them. The legacy L2 syncer stores every header
// it tracks and reports the exact reorged range, which keeps the strict "within" check.
func requireReorgEventWithin(t *testing.T, event bridgesync.LastReorg, first, last uint64, nativeMD bool) {
	t.Helper()
	if problem := reorgEventRangeProblem(event, first, last, nativeMD); problem != "" {
		t.Fatalf("last reorg event %+v is not an acceptable report of the reorg of blocks [%d, %d]: %s",
			event, first, last, problem)
	}
}

// reorgEventRangeProblem returns "" when event is an acceptable report of the reorg of the blocks
// [first, last], or a description of why not. See requireReorgEventWithin for what nativeMD changes.
func reorgEventRangeProblem(event bridgesync.LastReorg, first, last uint64, nativeMD bool) string {
	if event.FromBlock > event.ToBlock {
		return fmt.Sprintf("from_block %d is above to_block %d", event.FromBlock, event.ToBlock)
	}
	if nativeMD {
		// Cover the reorged blocks: the sparse rollback may start lower and the stored range may end
		// higher, but the reported range must still contain every reorged block.
		if event.FromBlock > first {
			return fmt.Sprintf("from_block %d starts after the first reorged block %d", event.FromBlock, first)
		}
		if event.ToBlock < last {
			return fmt.Sprintf("to_block %d ends before the last reorged block %d", event.ToBlock, last)
		}
		return ""
	}
	// Legacy syncer: the exact reorged range.
	if event.FromBlock < first {
		return fmt.Sprintf("from_block %d starts before the first reorged block %d", event.FromBlock, first)
	}
	if event.ToBlock > last {
		return fmt.Sprintf("to_block %d ends after the last reorged block %d", event.ToBlock, last)
	}
	return ""
}
