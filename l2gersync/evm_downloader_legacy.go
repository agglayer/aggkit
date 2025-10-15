package l2gersync

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmglobalexitrootv2"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// GEREventType represents the type of Global Exit Root event.
// It can be either an insertion or a removal of a Global Exit Root.
type GEREventType int

const (
	GEREventTypeInsert GEREventType = iota
	GEREventTypeRemove
)

// Event is the combination of the events that are emitted by the L2 GER manager
type Event struct {
	GERInfo   *GlobalExitRootInfo
	EventType GEREventType
}

// newEvent creates a new Event instance with the provided block number, GER info, and event type.
func newEvent(gerInfo *GlobalExitRootInfo, eventType GEREventType) *Event {
	return &Event{
		GERInfo:   gerInfo,
		EventType: eventType,
	}
}

type downloaderLegacy struct {
	*sync.EVMDownloaderImplementation
	l2GERManager   *polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2
	l2GERAddr      common.Address
	l1InfoTreeSync L1InfoTreeQuerier
	processor      *processor
	rh             *sync.RetryHandler
}

func newDownloaderLegacy(
	l2Client aggkittypes.BaseEthereumClienter,
	l2GERAddr common.Address,
	l1InfoTreeSync L1InfoTreeQuerier,
	processor *processor,
	rh *sync.RetryHandler,
	blockFinality aggkittypes.BlockNumberFinality,
	waitForNewBlocksPeriod time.Duration,
) (*downloaderLegacy, error) {
	l2GERManager, err := polygonzkevmglobalexitrootv2.NewPolygonzkevmglobalexitrootv2(
		l2GERAddr, l2Client)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize L2 GER manager contract: %w", err)
	}

	evmDownloader := sync.NewEVMDownloaderImplementation(
		"l2GERSync", l2Client, blockFinality,
		waitForNewBlocksPeriod, nil, nil,
		rh, nil, nil, "l2GERSync")

	return &downloaderLegacy{
		EVMDownloaderImplementation: evmDownloader,
		l2GERManager:                l2GERManager,
		l2GERAddr:                   l2GERAddr,
		l1InfoTreeSync:              l1InfoTreeSync,
		processor:                   processor,
		rh:                          rh,
	}, nil
}

// RuntimeData returns the runtime data: chainID + addresses to query
func (d *downloaderLegacy) RuntimeData(ctx context.Context) (sync.RuntimeData, error) {
	chainID, err := d.ChainID(ctx)
	if err != nil {
		return sync.RuntimeData{}, err
	}
	return sync.RuntimeData{
		ChainID:   chainID,
		Addresses: []common.Address{d.l2GERAddr},
	}, nil
}

func (d *downloaderLegacy) Download(ctx context.Context, fromBlock uint64, downloadedCh chan sync.EVMBlock) {
	var (
		attempts            int
		nextL1InfoTreeIndex uint32
		err                 error
	)

	// Determine the next index to start fetching GERs
	for {
		latestL1InfoTreeIndex, err := d.processor.getLatestL1InfoTreeIndex()
		if errors.Is(err, db.ErrNotFound) {
			nextL1InfoTreeIndex = 0
		} else if err != nil {
			log.Errorf("error getting latest l1 info tree index: %v", err)
			attempts++
			d.rh.Handle(ctx, "getLatestL1InfoTreeIndex", attempts)

			continue
		}
		if latestL1InfoTreeIndex > 0 {
			nextL1InfoTreeIndex = latestL1InfoTreeIndex + 1
		}
		break
	}

	for {
		select {
		case <-ctx.Done():
			log.Debug("aborting the l2GERSync downloader...")
			close(downloadedCh)

			return
		default:
		}

		// Wait for new blocks before processing
		fromBlock = d.WaitForNewBlocks(ctx, fromBlock)

		// Fetch GERs from the determined index
		var (
			attempts = 0
			gers     []*GlobalExitRootInfo
		)
		for {
			gers, err = d.getGERsFromIndex(ctx, fromBlock, nextL1InfoTreeIndex)
			if err != nil {
				log.Errorf("error getting GERs: %v", err)
				attempts++
				d.rh.Handle(ctx, "getGERsFromIndex", attempts)

				continue
			}

			break
		}

		// Find the latest GER injected from retrieved GERs
		gerInfo := d.findLatestInjectedGER(ctx, gers)

		header, isCanceled := d.GetBlockHeader(ctx, fromBlock)
		if isCanceled {
			return
		}
		if header == (sync.EVMBlockHeader{}) {
			log.Warnf("no block header found for block number %d", fromBlock)
			continue
		}

		block := &sync.EVMBlock{
			EVMBlockHeader: sync.EVMBlockHeader{
				Num:        header.Num,
				Hash:       header.Hash,
				ParentHash: header.ParentHash,
				Timestamp:  header.Timestamp,
			},
		}

		if gerInfo != nil {
			block.Events = []any{newEvent(gerInfo, GEREventTypeInsert)}

			// Update nextIndex based on the last injected GER info
			nextL1InfoTreeIndex = gerInfo.L1InfoTreeIndex + 1
		}

		// Report the block to the processor
		downloadedCh <- *block
	}
}

func (d *downloaderLegacy) getGERsFromIndex(
	ctx context.Context, blockNum uint64, fromL1InfoTreeIndex uint32) ([]*GlobalExitRootInfo, error) {
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
			return nil, fmt.Errorf("failed to retrieve l1 info tree leaf for index=%d: %w", i, err)
		}

		// For the legacy downloader, we cannot determine the block position,
		// because we are querying the global exit root map
		gers = append(gers, newLegacyGlobalExitRootInfo(info.GlobalExitRoot, i, blockNum))
	}

	return gers, nil
}

// findLatestInjectedGER finds the latest injected Global Exit Root from the provided GER infos.
// It is asumed that the GERs are ordered by their L1 info tree index in ascending order.
func (d *downloaderLegacy) findLatestInjectedGER(
	ctx context.Context, gerInfos []*GlobalExitRootInfo) *GlobalExitRootInfo {
	for i := len(gerInfos) - 1; i >= 0; i-- {
		gerInfo := gerInfos[i]
		attempts := 0
		for {
			blockHash, err := d.l2GERManager.GlobalExitRootMap(&bind.CallOpts{Pending: false}, gerInfo.GlobalExitRoot)
			if err != nil {
				attempts++
				log.Errorf("failed to check if global exit root %s is injected on L2: %s", gerInfo.GlobalExitRoot.Hex(), err)
				d.rh.Handle(ctx, "GlobalExitRootMap", attempts)
				continue
			}

			// Check if the GER is injected on L2
			if common.BigToHash(blockHash) != aggkitcommon.ZeroHash {
				return gerInfo
			}

			break
		}
	}

	return nil
}
