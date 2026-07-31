package l1infotreesync_test

import (
	"context"
	"errors"
	"math/big"
	"path"
	gosync "sync"
	"testing"
	"time"

	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// tipReorgEthClient decorates an aggkittypes.BaseEthereumClienter, simulating an RPC endpoint
// that serves data across an L1 tip-reorg boundary (e.g. load-balanced RPC nodes sitting on
// different forks). While armed, requests for the block header at poisonHeight are answered
// with the header of the losing (orphaned) fork, and log queries by that orphan block hash are
// answered with the poisoned log set. Everything else is passed through to the real client.
type tipReorgEthClient struct {
	aggkittypes.BaseEthereumClienter

	mu           gosync.Mutex
	armed        bool
	poisonHeight uint64
	orphanHeader *aggkittypes.BlockHeader
	poisonedLogs []types.Log
}

var _ aggkittypes.BaseEthereumClienter = (*tipReorgEthClient)(nil)

// Arm activates the poisoned view for the given height.
func (c *tipReorgEthClient) Arm(poisonHeight uint64, orphanHeader *aggkittypes.BlockHeader, poisonedLogs []types.Log) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = true
	c.poisonHeight = poisonHeight
	c.orphanHeader = orphanHeader
	c.poisonedLogs = poisonedLogs
}

// Disarm restores a fully consistent (canonical) RPC view.
func (c *tipReorgEthClient) Disarm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed = false
}

// RetrieveBlockHeaders delegates to the real client and, while armed, replaces the header at
// poisonHeight with the orphan fork header. This keeps the multidownloader's own reorg checks
// (detectReorgs) seeing a self-consistent orphan view, so its storage cannot self-repair until
// Disarm is called — exactly the window the incident's RPC produced.
func (c *tipReorgEthClient) RetrieveBlockHeaders(
	ctx context.Context, blockNumbers []uint64, maxConcurrency int,
) (*aggkittypes.BlockHeadersResult, error) {
	result, err := c.BaseEthereumClienter.RetrieveBlockHeaders(ctx, blockNumbers, maxConcurrency)
	if err != nil || result == nil {
		return result, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.armed {
		if _, requested := result.Headers[c.poisonHeight]; requested {
			result.Headers[c.poisonHeight] = c.orphanHeader
		}
	}
	return result, nil
}

// FilterLogs delegates to the real client, except that while armed the per-block-hash query for
// the orphan block hash is answered with the poisoned logs (orphan UpdateL1InfoTree + canonical
// UpdateL1InfoTreeV2, all carrying the orphan block hash so they pass the multidownloader's
// checkIntegrityNewLogsBlockHeaders).
func (c *tipReorgEthClient) FilterLogs(ctx context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	c.mu.Lock()
	if c.armed && q.BlockHash != nil && *q.BlockHash == c.orphanHeader.Hash {
		logs := make([]types.Log, len(c.poisonedLogs))
		copy(logs, c.poisonedLogs)
		c.mu.Unlock()
		return logs, nil
	}
	c.mu.Unlock()
	return c.BaseEthereumClienter.FilterLogs(ctx, q)
}

// TestE2E_TipReorgHaltRecovery is an e2e regression test for the cardona-67-op settlement
// outage of 2026-07-23/24 (aggkit v0.10.0-rc8, L1 = Sepolia, halt on Sepolia block 11333738).
//
// Incident mechanism reproduced here:
//  1. A GER update lands on L1 at the chain tip, but its block is orphaned by a same-parent
//     depth-1 reorg; the canonical block at the same height carries a different GER update.
//  2. An RPC serving across the reorg boundary hands the multidownloader a mixed view: the
//     orphan block header and the orphan UpdateL1InfoTree event, together with the canonical
//     UpdateL1InfoTreeV2 checkpoint.
//  3. The l1infotreesync processor builds the leaf from the orphan event and validates it
//     against the canonical checkpoint: computed root != event root with equal leaf counts, so
//     the V2 sanity check halts the processor (in-memory flag) and the batch tx rolls back
//     (nothing is persisted).
//  4. Because orphan and canonical block share the same parent, download-time reorg detection
//     (parent-of-committed-head check) never fires, and because the poisoned batch was never
//     committed the periodic reorg check has nothing to find. The multidownloader sync driver
//     (multidownloader/sync/evmdriver.go, withRetry) then retries the same in-memory batch
//     forever (~90,000 attempts over ~26h in the incident), and processor.Reorg only unhalts
//     when rowsAffected > 0, so the halt is permanent until a process restart.
//
// The test asserts the DESIRED behavior: once the RPC view becomes consistent again (the
// decorator is disarmed) and the chain advances, the syncer must self-recover — catch up to the
// tip and converge its L1 info tree root with the contract's latest root. On pre-fix code this
// test fails by timeout on the recovery assertion: the halt is observed, but the driver spins
// on the poisoned in-memory batch and every getter keeps returning sync.ErrInconsistentState.
func TestE2E_TipReorgHaltRecovery(t *testing.T) {
	ctx := t.Context()
	dbPath := path.Join(t.TempDir(), "l1infotreesyncTipReorgHaltRecovery.sqlite")

	client, auth, gerAddr, verifyAddr, gerSc, _ := newSimulatedClient(t)

	// --- Phase 1: warm-up — two canonical GER updates the syncer will process in lock-step.
	for _, root := range []common.Hash{common.HexToHash("0x01"), common.HexToHash("0x02")} {
		_, err := gerSc.UpdateExitRoot(auth, root)
		require.NoError(t, err)
		client.Commit()
	}
	// P: the block that will be the shared parent of the two competing forks.
	parentHeader, err := client.Client().HeaderByNumber(ctx, nil)
	require.NoError(t, err)
	poisonHeight := parentHeader.Number.Uint64() + 1

	// --- Phase 2: fork A (the losing fork) — one GER update at height N = poisonHeight.
	nonceA, err := client.Client().PendingNonceAt(ctx, auth.From)
	require.NoError(t, err)
	txA, err := gerSc.UpdateExitRoot(auth, common.HexToHash("0xaaaa"))
	require.NoError(t, err)
	client.Commit()

	orphanEthHeader, err := client.Client().HeaderByNumber(ctx, new(big.Int).SetUint64(poisonHeight))
	require.NoError(t, err)
	orphanHeader := aggkittypes.NewBlockHeaderFromEthHeader(orphanEthHeader)
	orphanHash := orphanEthHeader.Hash()

	logsA, err := client.Client().FilterLogs(ctx, ethereum.FilterQuery{
		BlockHash: &orphanHash,
		Addresses: []common.Address{gerAddr},
	})
	require.NoError(t, err)
	var orphanV1Log *types.Log
	for i := range logsA {
		if _, parseErr := gerSc.ParseUpdateL1InfoTree(logsA[i]); parseErr == nil {
			orphanV1Log = &logsA[i]
			break
		}
	}
	require.NotNil(t, orphanV1Log, "fork-A UpdateL1InfoTree log not found")
	orphanV1, err := gerSc.ParseUpdateL1InfoTree(*orphanV1Log)
	require.NoError(t, err)

	// --- Phase 3: abandon fork A — same-parent depth-1 reorg, the incident geometry.
	require.NoError(t, client.Fork(parentHeader.Hash()))
	blockNum, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	require.Equal(t, parentHeader.Number.Uint64(), blockNum)

	// --- Phase 4: canonical fork B at the same height N with a different GER update.
	// The fork re-injects fork-A's tx into the pending pool, so it must be replaced (same
	// nonce, bumped fees) to keep the orphan update out of the canonical block. The pool
	// resets asynchronously after Fork, hence the retry loop.
	auth.Nonce = new(big.Int).SetUint64(nonceA)
	auth.GasFeeCap = new(big.Int).Mul(txA.GasFeeCap(), big.NewInt(10))
	auth.GasTipCap = new(big.Int).Mul(txA.GasTipCap(), big.NewInt(10))
	require.Eventually(t, func() bool {
		_, sendErr := gerSc.UpdateExitRoot(auth, common.HexToHash("0xbbbb"))
		if sendErr != nil {
			t.Logf("replacing fork-A tx after reorg: %v", sendErr)
		}
		return sendErr == nil
	}, 10*time.Second, 100*time.Millisecond, "could not replace the re-injected fork-A tx in the pool")
	auth.Nonce, auth.GasFeeCap, auth.GasTipCap = nil, nil, nil
	client.Commit()

	canonicalEthHeader, err := client.Client().HeaderByNumber(ctx, new(big.Int).SetUint64(poisonHeight))
	require.NoError(t, err)
	canonicalHash := canonicalEthHeader.Hash()
	require.NotEqual(t, orphanHash, canonicalHash, "fork B must produce a different block at height N")
	require.Equal(t, parentHeader.Hash(), canonicalEthHeader.ParentHash, "forks must share the same parent")

	logsB, err := client.Client().FilterLogs(ctx, ethereum.FilterQuery{
		BlockHash: &canonicalHash,
		Addresses: []common.Address{gerAddr},
	})
	require.NoError(t, err)
	var (
		canonicalV1Log *types.Log
		canonicalV2Log *types.Log
		v1Count        int
	)
	for i := range logsB {
		if _, parseErr := gerSc.ParseUpdateL1InfoTree(logsB[i]); parseErr == nil {
			canonicalV1Log = &logsB[i]
			v1Count++
		}
		if _, parseErr := gerSc.ParseUpdateL1InfoTreeV2(logsB[i]); parseErr == nil {
			canonicalV2Log = &logsB[i]
		}
	}
	require.Equal(t, 1, v1Count, "canonical block N must contain exactly one GER update "+
		"(the re-injected fork-A tx must not have been mined)")
	require.NotNil(t, canonicalV2Log, "canonical UpdateL1InfoTreeV2 log not found")
	canonicalV1, err := gerSc.ParseUpdateL1InfoTree(*canonicalV1Log)
	require.NoError(t, err)
	require.NotEqual(t, orphanV1.MainnetExitRoot, canonicalV1.MainnetExitRoot,
		"orphan and canonical updates at height N must differ")

	// The poisoned batch: leaf-building event from the losing fork, checkpoint event from the
	// canonical fork — the load-balanced mixed view of the incident. All poisoned logs must
	// carry the ORPHAN block hash so they pass checkIntegrityNewLogsBlockHeaders.
	poisonedV2Log := *canonicalV2Log
	poisonedV2Log.BlockHash = orphanHash
	poisonedLogs := []types.Log{*orphanV1Log, poisonedV2Log}

	// --- Phase 5: advance the tip so height N stays inside the unsafe zone (finality
	// latestBlock/-5) for the whole armed phase.
	helpers.CommitBlocks(t, client, 2, time.Millisecond*100)

	// --- Phase 6: build and ARM the decorator BEFORE starting any component (arm-before-start
	// removes all races from the poisoning phase and makes the halt deterministic).
	decorated := &tipReorgEthClient{
		BaseEthereumClienter: etherman.NewDefaultEthClient(client.Client(), nil, nil),
	}
	decorated.Arm(poisonHeight, orphanHeader, poisonedLogs)

	// --- Phase 7: start multidownloader + syncer (multidownloader wiring as in TestWithReorgs).
	cfg := l1infotreesync.Config{
		DBPath:             dbPath,
		InitialBlock:       0,
		SyncBlockChunkSize: 10,
		BlockFinality:      aggkittypes.LatestBlock,
		GlobalExitRootAddr: gerAddr,
		RollupManagerAddr:  verifyAddr,
		RetryAfterErrorPeriod: cfgtypes.NewDuration(
			time.Millisecond * 100),
		// -1 mirrors the production default (config/default.go) and the incident behavior:
		// infinite retries, no RetryHandler.LogFatalf killing the process.
		MaxRetryAttemptsAfterError:         -1,
		RequireStorageContentCompatibility: true,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond),
	}
	cfgMD := multidownloader.NewConfigDefault("l1", t.TempDir())
	cfgMD.Enabled = true
	finality, err := aggkittypes.NewBlockNumberFinality("latestBlock/-5")
	require.NoError(t, err)
	cfgMD.BlockFinality = *finality
	cfgMD.WaitPeriodToCheckCatchUp = cfgtypes.NewDuration(time.Millisecond * 1)
	cfgMD.PeriodToCheckReorgs = cfgtypes.NewDuration(time.Millisecond * 10)
	evmMultidownloader, err := multidownloader.NewEVMMultidownloader(
		log.WithFields("module", "multidownloader"),
		cfgMD,
		"testMD",
		decorated,
		nil, // rpcClient
		nil, // Storage will be created internally
		nil, // blockNotifierManager will be created internally
		nil, // reorgProcessor will be created internally
	)
	require.NoError(t, err)
	syncer, err := l1infotreesync.NewMultidownloadBased(ctx, cfg, evmMultidownloader, l1infotreesync.FlagAllowWrongContractsAddrs)
	require.NoError(t, err)
	go func() {
		// Always returns an error at the end of the test, when the context is cancelled
		mdErr := evmMultidownloader.Start(ctx)
		log.Infof("Multidownloader exited with error: %v", mdErr)
	}()
	go syncer.Start(ctx)

	// --- Phase 8: the halt (the incident repro). The syncer processes the warm-up blocks and
	// then hits the poisoned batch at height N: leaf from the orphan event, checkpoint from the
	// canonical one -> computed root != event root with equal leaf counts -> processor halts.
	// This assertion passes both pre- and post-fix (post-fix the halt still happens; it just
	// recovers afterwards).
	require.Eventually(t, func() bool {
		_, lastErr := syncer.GetLastProcessedBlock(ctx)
		return errors.Is(lastErr, sync.ErrInconsistentState)
	}, 30*time.Second, 50*time.Millisecond,
		"processor never halted on the poisoned tip-reorg batch — fixture broken")

	// --- Phase 9: consolidation. The RPC view becomes consistent again (fork A fully
	// abandoned) and the chain advances with a fresh GER update, so a recovered syncer will
	// re-run the V2 sanity check on canonical data.
	decorated.Disarm()
	_, err = gerSc.UpdateExitRoot(auth, common.HexToHash("0xcccc"))
	require.NoError(t, err)
	helpers.CommitBlocks(t, client, 6, time.Millisecond*100)

	// --- Phase 10: recovery (the red/green assertion). Desired post-fix behavior: the syncer
	// discards the poisoned batch, unhalts, re-downloads the (repaired) canonical data, catches
	// up to the tip and converges with the contract's latest UpdateL1InfoTreeV2 root. Pre-fix
	// this never happens: the driver retries the same in-memory batch forever and every getter
	// keeps returning sync.ErrInconsistentState until the process is restarted.
	targetBlock, err := client.Client().BlockNumber(ctx)
	require.NoError(t, err)
	expectedRoot, err := gerSc.GetRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)

	var (
		recovered         bool
		lastObservedErr   error
		lastObservedBlock uint64
	)
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		lastProcessed, lastErr := syncer.GetLastProcessedBlock(ctx)
		lastObservedErr = lastErr
		if lastErr == nil {
			lastObservedBlock = lastProcessed
			if lastProcessed >= targetBlock {
				actualRoot, rootErr := syncer.GetLastL1InfoTreeRoot(ctx)
				if rootErr == nil && actualRoot.Hash == common.Hash(expectedRoot) {
					recovered = true
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Truef(t, recovered,
		"syncer did not recover from the tip-reorg halt within 90s: chain advanced to block %d, "+
			"but GetLastProcessedBlock=%d with err=%v — the driver keeps retrying the poisoned "+
			"in-memory batch and the processor stays halted (cardona-67-op incident behavior)",
		targetBlock, lastObservedBlock, lastObservedErr)

	// Strengthening: the leaf at the poisoned height must now hold the canonical fork-B
	// content, proving the orphan leaf was replaced rather than appended past.
	// Leaves: warm-up = 0 and 1, height N = 2.
	info, err := syncer.GetInfoByIndex(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, common.Hash(canonicalV1.MainnetExitRoot), info.MainnetExitRoot,
		"leaf at the reorged height must hold the canonical (fork-B) update content")
}
