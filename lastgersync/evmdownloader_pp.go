package lastgersync

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
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

type downloaderPP struct {
	*sync.EVMDownloaderImplementation
	l2GERManager   *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain
	l2GERAddr      common.Address
	l1InfoTreeSync L1InfoTreeQuerier
	processor      *processor
	rh             *sync.RetryHandler
}

func newDownloaderPP(
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERAddr common.Address,
	l1InfoTreeSync L1InfoTreeQuerier,
	processor *processor,
	rh *sync.RetryHandler,
	blockFinality *big.Int,
	waitForNewBlocksPeriod time.Duration) (*downloaderPP, error) {
	l2GERManager, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
		l2GERAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L2 GER manager contract: %w", err)
	}

	d := &downloaderPP{
		l2GERManager:   l2GERManager,
		l2GERAddr:      l2GERAddr,
		l1InfoTreeSync: l1InfoTreeSync,
		processor:      processor,
		rh:             rh,
	}

	appender := d.buildAppender(l2GERManager)

	evmDownloader := sync.NewEVMDownloaderImplementation(
		"lastgersync", l2Client, blockFinality,
		waitForNewBlocksPeriod, appender, []common.Address{l2GERAddr},
		rh, nil)

	d.EVMDownloaderImplementation = evmDownloader

	return d, nil
}

// RuntimeData returns the runtime data: chainID + addresses to query
func (d *downloaderPP) RuntimeData(ctx context.Context) (sync.RuntimeData, error) {
	chainID, err := d.ChainID(ctx)
	if err != nil {
		return sync.RuntimeData{}, err
	}
	return sync.RuntimeData{
		ChainID:   chainID,
		Addresses: []common.Address{d.l2GERAddr},
	}, nil
}

func (d *downloaderPP) Download(ctx context.Context, fromBlock uint64, downloadedCh chan sync.EVMBlock) {
	for {
		select {
		case <-ctx.Done():
			log.Debug("aborting the lastgersync downloader...")
			close(downloadedCh)

			return
		default:
		}

		// Wait for new blocks before processing
		fromBlock = d.WaitForNewBlocks(ctx, fromBlock)
		for _, block := range d.GetEventsByBlockRange(ctx, fromBlock, fromBlock) {
			downloadedCh <- *block
		}
	}
}

// buildAppender creates a log appender for the downloader
// It parses the logs emitted by the L2 GER manager and populates the block events
// with the corresponding events.
func (d *downloaderPP) buildAppender(
	l2GERManager *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain) sync.LogAppenderMap {
	appender := make(sync.LogAppenderMap)

	appender[removeGEREventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		removeGEREvent, err := l2GERManager.ParseUpdateRemovalHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateRemovalHashChainValue event log %+v: %w", l, err)
		}

		b.Events = []any{
			&Event{
				GEREvent: &GEREvent{
					BlockNum:       b.Num,
					GlobalExitRoot: removeGEREvent.RemovedGlobalExitRoot,
					IsRemove:       true,
				},
			}}
		return nil
	}

	appender[insertGEREventSignature] = func(b *sync.EVMBlock, l types.Log) error {
		insertGEREvent, err := l2GERManager.ParseUpdateHashChainValue(l)
		if err != nil {
			return fmt.Errorf("error parsing UpdateHashChainValue event log %+v: %w", l, err)
		}

		l1InfoTreeLeaf, err := d.l1InfoTreeSync.GetInfoByGlobalExitRoot(insertGEREvent.NewGlobalExitRoot)
		if err != nil {
			return fmt.Errorf("failed to fetch l1 info tree for global exit root %s: %w",
				insertGEREvent.NewGlobalExitRoot, err)
		}

		b.Events = []any{
			&Event{
				GEREvent: &GEREvent{
					BlockNum:        b.Num,
					GlobalExitRoot:  insertGEREvent.NewGlobalExitRoot,
					L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
					IsRemove:        false,
				},
			}}
		return nil
	}

	return appender
}
