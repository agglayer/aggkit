package l1infotreesync

import (
	"context"
	"path"
	gosync "sync"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	mdrsync "github.com/agglayer/aggkit/multidownloader/sync"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// scriptedPoisonDownloader is a hand-written mdrsynctypes.DownloaderInterface that plays back a
// fixed script of blocks, simulating a flaky RPC that silently omitted the UpdateL1InfoTree/V2
// logs for one specific block exactly once (the bokuto incident, 2026-08-05). It is a deliberate
// simplification of a full multidownloader+simulated-chain e2e test (as in
// e2e_reorg_halt_test.go): driving the real multidownloader stack showed that its own
// unsafe/safe block cache replays an already-fetched (poisoned) response on retry rather than
// re-querying the RPC, which defeats a literal "RPC glitches once" simulation at the ethclient
// level. Scripting DownloaderInterface directly -- the same seam l1infotreesync.go wires the real
// downloader into -- lets the test drive the real processor + real multidownloader/sync.EVMDriver
// (including its withRetry/RetryHandler machinery) through the exact self-healing path while
// keeping the "RPC" behavior deterministic and inspectable. Every response is counted so the test
// can assert on the *sequence* of what was served instead of trying to catch transient
// halted/unhalted states via polling: since there is no real network latency here, a full
// halt -> shallow-recover -> halt-again -> escalate -> heal cycle can complete in well under a
// millisecond, faster than any polling interval could reliably observe.
type scriptedPoisonDownloader struct {
	mu             gosync.Mutex
	poisonConsumed bool

	checkpointServed int
	poisonedServed   int
	healedServed     int
	failingServed    int

	checkpointBlock aggkitsync.Block // block 1: leaf0 + a matching checkpoint
	poisonedBlock   aggkitsync.Block // block 2 as first fetched: 0 events (the dropped logs)
	healedBlock     aggkitsync.Block // block 2 once re-fetched: leaf1 + a matching checkpoint
	failingBlock    aggkitsync.Block // block 3: leaf2 + a checkpoint against the REAL (3-leaf) root
}

func (d *scriptedPoisonDownloader) ChainID(ctx context.Context) (uint64, error) {
	return 1, nil
}

func (d *scriptedPoisonDownloader) DownloadNextBlocks(
	ctx context.Context, fromBlockHeader *aggkittypes.BlockHeader, maxBlocks uint64,
	syncerConfig aggkittypes.SyncerConfig,
) (*mdrsynctypes.DownloadResult, error) {
	var lastProcessed uint64
	if fromBlockHeader != nil {
		lastProcessed = fromBlockHeader.Number
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	var block aggkitsync.Block
	switch lastProcessed {
	case 0:
		d.checkpointServed++
		block = d.checkpointBlock
	case 1:
		if !d.poisonConsumed {
			d.poisonConsumed = true
			d.poisonedServed++
			block = d.poisonedBlock
		} else {
			d.healedServed++
			block = d.healedBlock
		}
	case 2:
		d.failingServed++
		block = d.failingBlock
	default:
		// Caught up: nothing more to serve.
		return &mdrsynctypes.DownloadResult{}, nil
	}

	return &mdrsynctypes.DownloadResult{
		Data: aggkitsync.EVMBlocks{
			&aggkitsync.EVMBlock{
				EVMBlockHeader: aggkitsync.EVMBlockHeader{Num: block.Num, Hash: block.Hash},
				Events:         block.Events,
			},
		},
		CompletionPercentage: 100,
	}, nil
}

func (d *scriptedPoisonDownloader) counts() (checkpoint, poisoned, healed, failing int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.checkpointServed, d.poisonedServed, d.healedServed, d.failingServed
}

// TestE2E_CommittedStateSelfHeal is a regression test for the bokuto outage (aggkit v0.11.0-rc2,
// 2026-08-05): a flaky RPC silently drops the UpdateL1InfoTree/V2 logs for one block, so that
// block commits with a leaf missing and no immediate error -- the divergence is now permanently
// in already-committed data. Only much later, when a subsequent legitimate UpdateL1InfoTreeV2
// checkpoint no longer matches the (now short) local tree, does the processor halt, at a block
// that is *not* where the real divergence lives. Recovering by purging just the failing batch
// (the pre-fix rc2 behavior, PR #1738) can never reach the divergence and loops forever (observed
// on bokuto: ~90s per attempt, 13+ hours straight). This test asserts the desired self-healing
// behavior: the second consecutive halt at the same block escalates the purge back to the last
// verified checkpoint, which reaches (and re-downloads, now correctly) the block with the missing
// leaf, and the syncer fully recovers.
func TestE2E_CommittedStateSelfHeal(t *testing.T) {
	ctx := context.Background()

	leaf0 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("0xaa"), RollupExitRoot: common.Hash{},
		ParentHash: common.HexToHash("0x01"), Timestamp: 1,
	}
	leaf1 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("0xbb"), RollupExitRoot: common.Hash{},
		ParentHash: common.HexToHash("0x02"), Timestamp: 2,
	}
	leaf2 := &UpdateL1InfoTree{
		MainnetExitRoot: common.HexToHash("0xcc"), RollupExitRoot: common.Hash{},
		ParentHash: common.HexToHash("0x03"), Timestamp: 3,
	}

	// Ground truth: what the real L1 chain's tree looks like after each leaf, computed with a
	// throwaway processor so the scripted UpdateL1InfoTreeV2 checkpoints stay internally
	// consistent -- exactly what real events derived from the real chain would contain.
	realProcessor, err := newProcessor(path.Join(t.TempDir(), "real-chain.sqlite"))
	require.NoError(t, err)
	rootAfter := func(blockNum uint64, leaf *UpdateL1InfoTree) (common.Hash, uint32) {
		require.NoError(t, realProcessor.ProcessBlock(ctx, aggkitsync.Block{
			Num:    blockNum,
			Events: []any{Event{UpdateL1InfoTree: leaf}},
		}))
		root, err := realProcessor.l1InfoTree.GetLastRoot(realProcessor.db)
		require.NoError(t, err)
		return root.Hash, root.Index + 1
	}
	root0, leafCount0 := rootAfter(1, leaf0)
	root1, leafCount1 := rootAfter(2, leaf1)
	root2, leafCount2 := rootAfter(3, leaf2)

	downloader := &scriptedPoisonDownloader{
		checkpointBlock: aggkitsync.Block{
			Num: 1,
			Events: []any{
				Event{UpdateL1InfoTree: leaf0},
				Event{UpdateL1InfoTreeV2: &UpdateL1InfoTreeV2{CurrentL1InfoRoot: root0, LeafCount: leafCount0}},
			},
		},
		poisonedBlock: aggkitsync.Block{Num: 2}, // 0 events: the dropped-log block
		healedBlock: aggkitsync.Block{
			Num: 2,
			Events: []any{
				Event{UpdateL1InfoTree: leaf1},
				Event{UpdateL1InfoTreeV2: &UpdateL1InfoTreeV2{CurrentL1InfoRoot: root1, LeafCount: leafCount1}},
			},
		},
		failingBlock: aggkitsync.Block{
			Num: 3,
			Events: []any{
				Event{UpdateL1InfoTree: leaf2},
				Event{UpdateL1InfoTreeV2: &UpdateL1InfoTreeV2{CurrentL1InfoRoot: root2, LeafCount: leafCount2}},
			},
		},
	}

	p, err := newProcessor(path.Join(t.TempDir(), "syncer.sqlite"))
	require.NoError(t, err)

	rh := &aggkitsync.RetryHandler{RetryAfterErrorPeriod: 5 * time.Millisecond, MaxRetryAttemptsAfterError: -1}
	syncerConfig := aggkittypes.SyncerConfig{FromBlock: 0}
	driver := mdrsync.NewEVMDriver(log.WithFields("test", "committed-poison"), p, downloader, syncerConfig, 1, rh, nil)

	driverCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go driver.Sync(driverCtx, &syncerConfig.FromBlock)

	// --- Recovery (the red/green assertion). Desired post-fix behavior: the syncer processes the
	// checkpoint block, then silently commits the poisoned block with a leaf missing (no error);
	// the failing checkpoint at block 3 then halts the processor. The first Reorg only purges
	// block 3, which can never reach the real, earlier, committed divergence at block 2, so the
	// processor halts again at the exact same block. That second consecutive halt must escalate
	// the purge back to the last verified checkpoint (block 1), re-downloading block 2 -- now
	// correctly, since the one-shot poison was already consumed -- and the syncer must fully
	// recover. Pre-fix, this never happens: the shallow purge repeats forever and the processor
	// stays halted (the bokuto incident behavior) -- observable here as failingServed growing
	// without bound and lastProcessed never reaching 3.
	require.Eventually(t, func() bool {
		lastProcessed, found, lastErr := p.GetLastProcessedBlock(ctx)
		return lastErr == nil && found && !p.isHalted() && lastProcessed >= 3
	}, 10*time.Second, 2*time.Millisecond,
		"syncer did not self-heal from the committed-state divergence: the driver keeps retrying the same "+
			"shallow purge and the processor stays halted")

	// The sequence of what the (fake) RPC actually served proves the desired recovery path was
	// taken, not just that the final state happens to look right:
	//  - the poisoned block-2 response was served (and consumed) exactly once: a real flaky-RPC
	//    glitch is transient, not a permanent malfunction.
	//  - the failing block-3 checkpoint was served (and failed) at least twice: once triggering
	//    the first, shallow (no-progress) recovery, and once more triggering the escalation.
	//  - the checkpoint block was re-served after the escalation purged back to it.
	//  - the healed block-2 response (with the real, previously-missing leaf) was served during
	//    the escalated recovery.
	checkpointServed, poisonedServed, healedServed, failingServed := downloader.counts()
	require.Equal(t, 1, poisonedServed, "the poisoned response must be consumed exactly once (one-shot glitch)")
	require.GreaterOrEqual(t, failingServed, 2,
		"the failing checkpoint must have been retried at least once before escalating")
	require.GreaterOrEqual(t, checkpointServed, 2,
		"the checkpoint block must have been re-downloaded after the escalated purge reached it")
	require.GreaterOrEqual(t, healedServed, 1,
		"the healed (previously poisoned) block must have been re-downloaded during the escalated recovery")

	// Strengthening: the previously missing leaf must now be present with its real content (the
	// range was re-downloaded, this time without the RPC glitch), the later leaf must be at its
	// correct index, and the checkpoint must have advanced past the whole recovered range.
	info1, err := p.GetInfoByIndex(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), info1.BlockNumber)
	require.Equal(t, common.HexToHash("0xbb"), info1.MainnetExitRoot)

	info2, err := p.GetInfoByIndex(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(3), info2.BlockNumber)
	require.Equal(t, common.HexToHash("0xcc"), info2.MainnetExitRoot)

	checkpointBlockNum, found, err := getCheckpointBlockWithTx(p.db)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(3), checkpointBlockNum, "the checkpoint must have advanced past the recovered range")
}
