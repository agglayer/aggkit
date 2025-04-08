package lastgersync

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

var (
	// event UpdateRemovalHashChainValue(bytes32 indexed removedGlobalExitRoot,	bytes32 indexed newRemovalHashChainValue)
	removeGEREventSignature = crypto.Keccak256Hash([]byte("UpdateRemovalHashChainValue(bytes32,bytes32)"))
)

// Event is the combination of the events that are emitted by the L2 GER manager
type Event struct {
	GERInfo        *GlobalExitRootInfo
	RemoveGEREvent *RemoveGEREvent
}

type downloader struct {
	*sync.EVMDownloaderImplementation
	l2GERManager       *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain
	l2GERAddr          common.Address
	syncBlockChunkSize uint64
	l1InfoTreeSync     L1InfoTreeQuerier
	processor          *processor
	rh                 *sync.RetryHandler
}

func newDownloader(
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERAddr common.Address,
	syncBlockChunkSize uint64,
	l1InfoTreeSync L1InfoTreeQuerier,
	processor *processor,
	rh *sync.RetryHandler,
	blockFinality *big.Int,
	waitForNewBlocksPeriod time.Duration,
) (*downloader, error) {
	gerContract, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
		l2GERAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L2 GER manager contract: %w", err)
	}

	appender := buildAppender(gerContract)
	evmDownloader := sync.NewEVMDownloaderImplementation(
		"lastgersync", l2Client, blockFinality,
		waitForNewBlocksPeriod, appender, []common.Address{l2GERAddr},
		rh, nil)

	return &downloader{
		EVMDownloaderImplementation: evmDownloader,
		l2GERManager:                gerContract,
		l2GERAddr:                   l2GERAddr,
		syncBlockChunkSize:          syncBlockChunkSize,
		l1InfoTreeSync:              l1InfoTreeSync,
		processor:                   processor,
		rh:                          rh,
	}, nil
}

// RuntimeData returns the runtime data: chainID + addresses to query
func (d *downloader) RuntimeData(ctx context.Context) (sync.RuntimeData, error) {
	chainID, err := d.ChainID(ctx)
	if err != nil {
		return sync.RuntimeData{}, err
	}
	return sync.RuntimeData{
		ChainID:   chainID,
		Addresses: []common.Address{d.l2GERAddr},
	}, nil
}

func (d *downloader) Download(ctx context.Context, fromBlock uint64, downloadedCh chan sync.EVMBlock) {
	var (
		attempts  int
		nextIndex uint32
		lastBlock uint64
		err       error
	)

	// Determine the next index to start fetching GERs
	for {
		lastIndex, err := d.processor.getLastIndex()
		if errors.Is(err, db.ErrNotFound) {
			nextIndex = 0
		} else if err != nil {
			log.Errorf("error getting last index: %v", err)
			attempts++
			d.rh.Handle("getLastIndex", attempts)

			continue
		}
		if lastIndex > 0 {
			nextIndex = lastIndex + 1
		}
		break
	}

	for {
		select {
		case <-ctx.Done():
			log.Debug("aborting the lastgersync downloader...")
			close(downloadedCh)

			return
		default:
		}

		// Wait for new blocks before processing
		lastBlock = d.WaitForNewBlocks(ctx, fromBlock)

		// Fetch GERs from the determined index
		attempts = 0
		var gers []*GlobalExitRootInfo
		for {
			gers, err = d.getGERsFromIndex(ctx, nextIndex)
			if err != nil {
				log.Errorf("error getting GERs: %v", err)
				attempts++
				d.rh.Handle("getGERsFromIndex", attempts)

				continue
			}

			break
		}

		header, isCanceled := d.GetBlockHeader(ctx, lastBlock)
		if isCanceled {
			return
		}

		block := &sync.EVMBlock{
			EVMBlockHeader: sync.EVMBlockHeader{
				Num:        header.Num,
				Hash:       header.Hash,
				ParentHash: header.ParentHash,
				Timestamp:  header.Timestamp,
			},
		}
		// Set the greatest GER injected from retrieved GERs
		d.populateGreatestInjectedGER(block, gers)

		downloadedCh <- *block
		// Update nextIndex based on the last injected GER info
		if len(block.Events) > 0 {
			if e, ok := block.Events[0].(*GlobalExitRootInfo); ok {
				nextIndex = e.L1InfoTreeIndex + 1
			}
		} else {
			toBlock := min(fromBlock+d.syncBlockChunkSize, lastBlock)
			for _, b := range d.GetEventsByBlockRange(ctx, lastBlock, toBlock) {
				downloadedCh <- *b
			}
		}
	}
}

func (d *downloader) getGERsFromIndex(ctx context.Context, fromL1InfoTreeIndex uint32) ([]*GlobalExitRootInfo, error) {
	lastRoot, err := d.l1InfoTreeSync.GetLastL1InfoTreeRoot(ctx)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("error calling GetLastL1InfoTreeRoot: %w", err)
	}

	gers := make([]*GlobalExitRootInfo, 0, lastRoot.Index-fromL1InfoTreeIndex+1)
	for i := fromL1InfoTreeIndex; i <= lastRoot.Index; i++ {
		info, err := d.l1InfoTreeSync.GetInfoByIndex(ctx, i)
		if err != nil {
			return nil, fmt.Errorf("error calling GetInfoByIndex: %w", err)
		}
		gers = append(gers, &GlobalExitRootInfo{
			L1InfoTreeIndex: i,
			GlobalExitRoot:  info.GlobalExitRoot,
		})
	}

	return gers, nil
}

func (d *downloader) populateGreatestInjectedGER(b *sync.EVMBlock, gerInfos []*GlobalExitRootInfo) {
	for _, gerInfo := range gerInfos {
		attempts := 0
		for {
			blockHashOrTimestamp, err := d.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, gerInfo.GlobalExitRoot)
			if err != nil {
				attempts++
				log.Errorf("failed to check if global exit root %s is injected on L2: %s", gerInfo.GlobalExitRoot.Hex(), err)
				d.rh.Handle("GlobalExitRootMap", attempts)

				continue
			}

			// Check if the GER is injected on L2
			if common.BigToHash(blockHashOrTimestamp) != aggkitcommon.ZeroHash ||
				common.Big0.Cmp(blockHashOrTimestamp) != 0 {
				// for GlobalExitRootManagerL2 contract, we are storing the block timestamp
				// instead of the block hash
				b.Events = []any{&Event{GERInfo: gerInfo}}
			}

			break
		}
	}
}

// buildAppender creates a log appender for the downloader
// It parses the logs emitted by the L2 GER manager and populates the block events
// with the corresponding events.
func buildAppender(
	l2GERManager *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain) sync.LogAppenderMap {
	appender := make(sync.LogAppenderMap)

	appender[removeGEREventSignature] = func(block *sync.EVMBlock, log types.Log) error {
		removeGEREvent, err := l2GERManager.ParseUpdateRemovalHashChainValue(log)
		if err != nil {
			return fmt.Errorf("error parsing UpdateRemovalHashChainValue event log %+v: %w", log, err)
		}

		block.Events = []any{
			&Event{
				RemoveGEREvent: &RemoveGEREvent{GlobalExitRoot: removeGEREvent.RemovedGlobalExitRoot},
			}}
		return nil
	}

	return appender
}
