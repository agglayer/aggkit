package l2gersync

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/aggoracle/types"
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
func (e *L2EVMGERReader) GetInjectedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) (map[common.Hash]GlobalExitRootInfo, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	// first get all inserted GERs in the block range
	insertIterator, err := e.l2GERManager.FilterUpdateHashChainValue(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		log.Errorf("failed to create InsertGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	defer func() {
		if err := insertIterator.Close(); err != nil {
			log.Errorf("failed to close InsertGlobalExitRoot iterator: %v", err)
		}
	}()

	injectedGERs := make(map[common.Hash]GlobalExitRootInfo, 0)

	for insertIterator.Next() {
		ger := insertIterator.Event.NewGlobalExitRoot
		l1InfoLeaf, err := e.l1InfoTreeSync.GetInfoByGlobalExitRoot(ger)
		if err != nil {
			return nil, fmt.Errorf("failed to get L1 info tree index for global exit root %s: %w", common.Hash(ger), err)
		}

		gerInfo := newGlobalExitRootInfo(ger, l1InfoLeaf.L1InfoTreeIndex,
			insertIterator.Event.Raw.BlockNumber, uint64(insertIterator.Event.Raw.Index))
		injectedGERs[ger] = *gerInfo
	}

	if insertIterator.Error() != nil {
		return nil, insertIterator.Error()
	}

	// then get all removed GERs in the block range
	// and remove them from the injectedGERs map
	removalIterator, err := e.l2GERManager.FilterUpdateRemovalHashChainValue(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		log.Errorf("failed to create RemoveGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	defer func() {
		if err := removalIterator.Close(); err != nil {
			log.Errorf("failed to close RemoveGlobalExitRoot iterator: %v", err)
		}
	}()

	for removalIterator.Next() {
		ger := removalIterator.Event.RemovedGlobalExitRoot
		delete(injectedGERs, ger)
	}

	if removalIterator.Error() != nil {
		return nil, removalIterator.Error()
	}

	return injectedGERs, nil
}
