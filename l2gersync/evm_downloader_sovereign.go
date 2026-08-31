package l2gersync

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	// event UpdateHashChainValue(bytes32 indexed newGlobalExitRoot, bytes32 indexed newHashChainValue);
	insertGEREventSignature = crypto.Keccak256Hash([]byte("UpdateHashChainValue(bytes32,bytes32)"))

	// event UpdateRemovalHashChainValue(bytes32 indexed removedGlobalExitRoot,	bytes32 indexed newRemovalHashChainValue)
	removeGEREventSignature = crypto.Keccak256Hash([]byte("UpdateRemovalHashChainValue(bytes32,bytes32)"))
)

// maxRemovalScanRange caps isGERRemovedFromL2's lookback window so its eth_getLogs call never
// exceeds the L2 RPC's own block-range limit (observed as 100000 blocks on this deployment).
const maxRemovalScanRange = 99_999

type downloaderSovereign struct {
	*sync.EVMDownloaderImplementation
	l2GERManager       *agglayergerl2.Agglayergerl2
	l2GERAddr          common.Address
	l1InfoTreeSync     L1InfoTreeQuerier
	l1GERManager       *agglayerger.Agglayerger
	rh                 *sync.RetryHandler
	syncBlockChunkSize uint64
}

func newDownloaderSovereign(
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERAddr common.Address,
	l1InfoTreeSync L1InfoTreeQuerier,
	l1Client aggkittypes.BaseEthereumClienter,
	l1GERAddr common.Address,
	rh *sync.RetryHandler,
	blockFinality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	syncBlockChunkSize uint64) (*downloaderSovereign, error) {
	l2GERManager, err := agglayergerl2.NewAgglayergerl2(
		l2GERAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L2 GER manager contract: %w", err)
	}

	l1GERManager, err := agglayerger.NewAgglayerger(
		l1GERAddr, l1Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L1 GER manager contract: %w", err)
	}

	d := &downloaderSovereign{
		l2GERManager:       l2GERManager,
		l2GERAddr:          l2GERAddr,
		l1InfoTreeSync:     l1InfoTreeSync,
		l1GERManager:       l1GERManager,
		rh:                 rh,
		syncBlockChunkSize: syncBlockChunkSize,
	}

	appender := d.buildAppender(l2GERManager)

	evmDownloader := sync.NewEVMDownloaderImplementation(
		"l2GERSync", sync.NewAdapterEthClientToMultidownloader(l2Client), blockFinality,
		waitForNewBlocksPeriod, appender, []common.Address{l2GERAddr},
		rh, nil, nil, "l2GERSync")

	d.EVMDownloaderImplementation = evmDownloader

	return d, nil
}

// RuntimeData returns the runtime data: chainID + addresses to query
func (d *downloaderSovereign) RuntimeData(ctx context.Context) (sync.RuntimeData, error) {
	chainID, err := d.ChainID(ctx)
	if err != nil {
		return sync.RuntimeData{}, err
	}
	return sync.RuntimeData{
		ChainID:   chainID,
		Addresses: []common.Address{d.l2GERAddr},
	}, nil
}

func (d *downloaderSovereign) Download(
	ctx context.Context, fromBlock uint64, downloadedCh chan sync.EVMBlock, _ *uint64, _ bool,
) {
	for {
		select {
		case <-ctx.Done():
			log.Debug("aborting the l2GERSync downloader...")
			close(downloadedCh)
			return
		default:
		}

		// Wait for new blocks and get current head
		latestBlock := d.WaitForNewBlocks(ctx, fromBlock)
		toBlock := min(fromBlock+d.syncBlockChunkSize-1, latestBlock)
		log.Debugf("processing chunk [%d to %d] (chunk size: %d)", fromBlock, toBlock, d.syncBlockChunkSize)

		blocks := d.GetEventsByBlockRange(ctx, fromBlock, toBlock)
		for _, block := range blocks {
			downloadedCh <- *block
		}

		fromBlock = toBlock + 1
	}
}

// buildAppender creates a log appender for the downloader
// It parses the logs emitted by the L2 GER manager and populates the block events
// with the corresponding events.
func (d *downloaderSovereign) buildAppender(
	l2GERManager *agglayergerl2.Agglayergerl2) sync.LogAppenderMap {
	appender := make(sync.LogAppenderMap)

	appender[removeGEREventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		removeGEREvent, err := l2GERManager.ParseUpdateRemovalHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateRemovalHashChainValue event log %+v: %w", l, err)
		}

		b.Events = append(b.Events, newEvent(
			newGlobalExitRootInfo(
				removeGEREvent.RemovedGlobalExitRoot,
				0,
				b.Num,
				uint64(l.Index)),
			GEREventTypeRemove))
		return nil
	}

	appender[insertGEREventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		insertGEREvent, err := l2GERManager.ParseUpdateHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateHashChainValue event log %+v: %w", l, err)
		}

		l1InfoTreeLeaf, err := d.l1InfoTreeSync.GetInfoByGlobalExitRoot(insertGEREvent.NewGlobalExitRoot)
		if err != nil {
			gerHash := common.Hash(insertGEREvent.NewGlobalExitRoot)
			log.Errorf("failed to fetch l1 info tree for global exit root %s (block: %d): %v",
				gerHash.Hex(), b.Num, err)
			ctx := context.Background()

			// The L1 contract read is informational only: it is redundant with the
			// invalid-GER condition ("on L2, not in L1 info tree") and must not gate the skip
			// decision; the recovery signal is entirely L2-side (see §1 below).
			timestamp, contractErr := d.l1GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, gerHash)
			if contractErr != nil {
				log.Errorf("GER lookup for %s failed in L1 contract: %v", gerHash.Hex(), contractErr)
			} else if timestamp == nil || timestamp.Cmp(common.Big0) == 0 {
				log.Infof("GER %s not found in L1 contract globalExitRootMap", gerHash.Hex())
			} else {
				log.Infof("GER %s exists in L1 contract", gerHash.Hex())
			}

			if d.isGERRemovedFromL2(ctx, b.Num, gerHash) {
				log.Infof("GER %s got removed from L2, skipping stale insert event", gerHash.Hex())
				return nil
			}

			return fmt.Errorf("failed to fetch l1 info tree for global exit root %s: %w",
				gerHash.Hex(), err)
		}

		b.Events = append(b.Events, newEvent(
			newGlobalExitRootInfo(
				insertGEREvent.NewGlobalExitRoot,
				l1InfoTreeLeaf.L1InfoTreeIndex,
				b.Num,
				uint64(l.Index)),
			GEREventTypeInsert))
		return nil
	}

	return appender
}

// isGERRemovedFromL2 reports whether ger has genuinely been removed on-chain. Two corroborating signals,
// both pinned to the current L2 head, are required (AND):
//
//   - (S-log) a durable UpdateRemovalHashChainValue event for ger exists anywhere from fromBlock (the
//     insert block, so an unrelated older removal can never be mistaken for this one) up to
//     fromBlock+maxRemovalScanRange (capped rather than scanning to latest, to stay within the L2 RPC's
//     own eth_getLogs block-range limit — a removal further out than that window is not detected here);
//   - (S-map) the L2 globalExitRootMap entry for ger currently reads 0.
//
// Requiring both defends against the two false-unstick vectors: a transient/stale zero map read with no
// actual removal (S-log would be empty), and a removal later reversed by re-injection (S-map would be
// non-zero). Any read/scan error is treated as "not removed" (never dereferences a nil value), so the
// caller keeps retrying rather than skipping a stale insert incorrectly.
func (d *downloaderSovereign) isGERRemovedFromL2(ctx context.Context, fromBlock uint64, ger common.Hash) bool {
	toBlock := fromBlock + maxRemovalScanRange
	removedEvents, err := filterRemovedGERs(ctx, d.l2GERManager, fromBlock, &toBlock, [][common.HashLength]byte{ger})
	if err != nil {
		log.Errorf("failed to scan for GER %s removal events from block %d: %v", ger.Hex(), fromBlock, err)
		return false
	}
	if len(removedEvents) == 0 {
		return false
	}

	timestampL2, err := d.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, ger)
	if err != nil || timestampL2 == nil {
		log.Errorf("failed to read L2 globalExitRootMap for GER %s: %v", ger.Hex(), err)
		return false
	}

	return timestampL2.Cmp(common.Big0) == 0
}
