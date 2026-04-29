package l2gersync

import (
	"context"
	"fmt"
	"maps"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// L2EVMGERReader is a component used to read GlobalExitRootManager L2 contract
type L2EVMGERReader struct {
	l2GERManager   types.L2GERManagerContract
	l1InfoTreeSync L1InfoTreeQuerier
}

// NewL2EVMGERReader creates a new instance of L2 EVM global exit root reader
func NewL2EVMGERReader(
	l2GERManagerAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter,
	l1InfoTreeSync L1InfoTreeQuerier,
) (*L2EVMGERReader, error) {
	l2GERManager, err := agglayergerl2.NewAgglayergerl2(
		l2GERManagerAddr, l2Client)
	if err != nil {
		return nil, err
	}

	if err := validateL2GERContract(l2GERManager, l2GERManagerAddr); err != nil {
		return nil, err
	}

	return &L2EVMGERReader{
		l2GERManager:   l2GERManager,
		l1InfoTreeSync: l1InfoTreeSync,
	}, nil
}

// validateL2GERContract checks if the GlobalExitRootManager contract is valid on given address
func validateL2GERContract(l2GERManager types.L2GERManagerContract, l2GERManagerAddr common.Address) error {
	gerUpdater, err := l2GERManager.GlobalExitRootUpdater(nil)
	if err != nil {
		return fmt.Errorf("L2 GER manager contract sanity check failed (SC address=%s): %w",
			l2GERManagerAddr, err)
	}
	log.Infof("sanity check for L2 GER manager contract (SC address=%s) successful. GlobalExitRootUpdater: %s",
		l2GERManagerAddr.String(), gerUpdater.String())
	return nil
}

// GetInjectedGERsForRange returns the injected GlobalExitRoots for the given block range
// If the block range is too large, it automatically splits the request into smaller chunks.
func (e *L2EVMGERReader) GetInjectedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) (map[common.Hash]GlobalExitRootInfo, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	injectedGERs, err := e.fetchInjectedGERs(ctx, fromBlock, toBlock)
	if err != nil {
		// Check if error is due to block range being too large
		maxRange, isMaxRangeErr := aggkitcommon.ParseMaxRangeFromError(err.Error())
		if isMaxRangeErr {
			log.Debugf("block range too large, splitting into chunks of max %d blocks", maxRange)
			return aggkitcommon.ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange,
				e.fetchInjectedGERs,
				func(all map[common.Hash]GlobalExitRootInfo,
					chunk map[common.Hash]GlobalExitRootInfo,
				) map[common.Hash]GlobalExitRootInfo {
					maps.Copy(all, chunk)
					return all
				},
				make(map[common.Hash]GlobalExitRootInfo),
			)
		}
		log.Errorf("failed to create InsertGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	return injectedGERs, nil
}

// fetchInjectedGERs performs the actual event filtering for injected GERs
func (e *L2EVMGERReader) fetchInjectedGERs(ctx context.Context,
	fromBlock, toBlock uint64) (map[common.Hash]GlobalExitRootInfo, error) {
	// first get all inserted GERs in the block range
	insertIterator, err := e.l2GERManager.FilterUpdateHashChainValue(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := insertIterator.Close(); err != nil {
			log.Errorf("failed to close InsertGlobalExitRoot iterator: %v", err)
		}
	}()

	removedGERs, err := e.GetRemovedGERsForRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting removed GERs block numbers: %w", err)
	}

	removedGERMap := make(map[common.Hash]struct{}, len(removedGERs))
	for _, removedGER := range removedGERs {
		removedGERMap[removedGER.GlobalExitRoot] = struct{}{}
	}

	injectedGERs := make(map[common.Hash]GlobalExitRootInfo, 0)

	for insertIterator.Next() {
		ger := common.Hash(insertIterator.Event.NewGlobalExitRoot)

		var (
			l1InfoTreeIndex uint32
			removed         bool
		)
		if _, removed = removedGERMap[ger]; removed {
			log.Infof("inserted GER %s at block %d, index %d was removed", ger.String(),
				insertIterator.Event.Raw.BlockNumber, insertIterator.Event.Raw.Index)
		} else {
			log.Infof("inserted GER: %s at block %d, index %d", ger.String(),
				insertIterator.Event.Raw.BlockNumber, insertIterator.Event.Raw.Index)
			l1InfoLeaf, err := e.l1InfoTreeSync.GetInfoByGlobalExitRoot(ger)
			if err != nil {
				return nil, fmt.Errorf("failed to get L1 info tree index for global exit root %s: %w",
					ger.String(), err)
			}

			l1InfoTreeIndex = l1InfoLeaf.L1InfoTreeIndex
		}

		gerInfo := newGlobalExitRootInfo(ger, l1InfoTreeIndex,
			insertIterator.Event.Raw.BlockNumber, uint64(insertIterator.Event.Raw.Index))
		gerInfo.Removed = removed
		injectedGERs[ger] = *gerInfo
	}

	if insertIterator.Error() != nil {
		return nil, insertIterator.Error()
	}

	return injectedGERs, nil
}

// GetRemovedGERsForRange returns the removed GlobalExitRoots for the given block range
// If the block range is too large, it automatically splits the request into smaller chunks.
func (e *L2EVMGERReader) GetRemovedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]*agglayertypes.RemovedGER, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	removedGERs, err := e.fetchRemovedGERs(ctx, fromBlock, toBlock)
	if err != nil {
		// Check if error is due to block range being too large
		maxRange, isMaxRangeErr := aggkitcommon.ParseMaxRangeFromError(err.Error())
		if isMaxRangeErr {
			log.Debugf("block range too large, splitting into chunks of max %d blocks", maxRange)
			return aggkitcommon.ChunkedRangeQuery(ctx, fromBlock, toBlock, maxRange,
				e.fetchRemovedGERs,
				func(all, chunk []*agglayertypes.RemovedGER) []*agglayertypes.RemovedGER {
					return append(all, chunk...)
				},
				[]*agglayertypes.RemovedGER{},
			)
		}
		log.Errorf("failed to create RemoveGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	return removedGERs, nil
}

// fetchRemovedGERs performs the actual event filtering for removed GERs
func (e *L2EVMGERReader) fetchRemovedGERs(ctx context.Context,
	fromBlock, toBlock uint64) ([]*agglayertypes.RemovedGER, error) {
	removalIterator, err := e.l2GERManager.FilterUpdateRemovalHashChainValue(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := removalIterator.Close(); err != nil {
			log.Errorf("failed to close RemoveGlobalExitRoot iterator: %v", err)
		}
	}()

	removedGERs := make([]*agglayertypes.RemovedGER, 0)

	for removalIterator.Next() {
		ger := removalIterator.Event.RemovedGlobalExitRoot
		log.Infof("removed GER: %s at block %d, index %d", common.Hash(ger).String(),
			removalIterator.Event.Raw.BlockNumber, removalIterator.Event.Raw.Index)
		removedGERs = append(removedGERs, &agglayertypes.RemovedGER{
			GlobalExitRoot: common.Hash(ger),
			BlockNumber:    removalIterator.Event.Raw.BlockNumber,
			LogIndex:       uint64(removalIterator.Event.Raw.Index),
		})
	}

	if removalIterator.Error() != nil {
		return nil, removalIterator.Error()
	}

	return removedGERs, nil
}
