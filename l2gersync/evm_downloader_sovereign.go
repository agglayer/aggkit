package l2gersync

import (
	"context"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
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

type downloaderSovereign struct {
	*sync.EVMDownloaderImplementation
	l2GERManager       *agglayergerl2.Agglayergerl2
	l2GERAddr          common.Address
	l1InfoTreeSync     L1InfoTreeQuerier
	l1Client           aggkittypes.BaseEthereumClienter
	rh                 *sync.RetryHandler
	syncBlockChunkSize uint64
}

func newDownloaderSovereign(
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERAddr common.Address,
	l1InfoTreeSync L1InfoTreeQuerier,
	l1Client aggkittypes.BaseEthereumClienter,
	rh *sync.RetryHandler,
	blockFinality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
	syncBlockChunkSize uint64) (*downloaderSovereign, error) {
	l2GERManager, err := agglayergerl2.NewAgglayergerl2(
		l2GERAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L2 GER manager contract: %w", err)
	}

	d := &downloaderSovereign{
		l2GERManager:       l2GERManager,
		l2GERAddr:          l2GERAddr,
		l1InfoTreeSync:     l1InfoTreeSync,
		l1Client:           l1Client,
		rh:                 rh,
		syncBlockChunkSize: syncBlockChunkSize,
	}

	appender := d.buildAppender(l2GERManager)

	evmDownloader := sync.NewEVMDownloaderImplementation(
		"l2GERSync", l2Client, blockFinality,
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

func (d *downloaderSovereign) Download(ctx context.Context, fromBlock uint64, downloadedCh chan sync.EVMBlock) {
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

		b.Events = []any{
			newEvent(
				newGlobalExitRootInfo(
					removeGEREvent.RemovedGlobalExitRoot,
					0,
					b.Num,
					uint64(l.Index)),
				GEREventTypeRemove)}
		return nil
	}

	appender[insertGEREventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		insertGEREvent, err := l2GERManager.ParseUpdateHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateHashChainValue event log %+v: %w", l, err)
		}

		l1InfoTreeLeaf, err := d.l1InfoTreeSync.GetInfoByGlobalExitRoot(insertGEREvent.NewGlobalExitRoot)
		if err != nil {
			ctx := context.Background()
			isUpToDate, err := d.l1InfoTreeSync.IsUpToDate(ctx, d.l1Client)
			if err != nil {
				log.Warnf("Failed to check if L1InfoTreeSync is up to date: %v", err)
			}
			if isUpToDate {
				log.Fatal("L1InfoTreeSync is to date, GER lookup for %s failed: %v",
					common.Hash(insertGEREvent.NewGlobalExitRoot).Hex(), err)
			}

			return fmt.Errorf("failed to fetch l1 info tree for global exit root %s: %w",
				common.Hash(insertGEREvent.NewGlobalExitRoot).Hex(), err)
		}

		b.Events = []any{
			newEvent(
				newGlobalExitRootInfo(
					insertGEREvent.NewGlobalExitRoot,
					l1InfoTreeLeaf.L1InfoTreeIndex,
					b.Num,
					uint64(l.Index)),
				GEREventTypeInsert),
		}
		return nil
	}

	return appender
}
