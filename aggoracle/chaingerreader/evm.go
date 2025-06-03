package chaingerreader

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	"github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// InjectedGER is a struct that represents the injected GlobalExitRoot event
type InjectedGER struct {
	// BlockNumber is the block number of the event
	BlockNumber uint64
	// BlockIndex is the index of the event in the block
	BlockIndex uint
	// NewGlobalExitRoot is the new GlobalExitRoot injected
	GlobalExitRoot common.Hash
}

// EVMChainGERReader is a component used to read GlobalExitRootManager L2 contract
type EVMChainGERReader struct {
	l2GERManager types.L2GERManagerContract
}

// NewEVMChainGERReader creates a new L2Etherman
func NewEVMChainGERReader(
	l2GERManagerAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter) (*EVMChainGERReader, error) {
	l2GERManager, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
		l2GERManagerAddr, l2Client)
	if err != nil {
		return nil, err
	}

	return newEVMChainGERReader(l2GERManager, l2GERManagerAddr)
}

func newEVMChainGERReader(l2GERManager types.L2GERManagerContract,
	l2GERManagerAddr common.Address) (*EVMChainGERReader, error) {
	if err := checkGlobalExitRootManagerContract(l2GERManager, l2GERManagerAddr); err != nil {
		return nil, err
	}

	return &EVMChainGERReader{l2GERManager: l2GERManager}, nil
}

// checkGlobalExitRootManagerContract checks if the GlobalExitRootManager contract is valid on given address
func checkGlobalExitRootManagerContract(l2GERManager types.L2GERManagerContract, contractAddr common.Address) error {
	bridgeAddr, err := l2GERManager.BridgeAddress(nil)
	if err != nil {
		return fmt.Errorf("fail sanity check GlobalExitRootManagerL2(%s) Contract. Err: %w", contractAddr.String(), err)
	}
	log.Infof("sanity check GlobalExitRootManagerL2(%s) OK. bridgeAddress: %s", contractAddr.String(), bridgeAddr.String())
	return nil
}

// GetInjectedGERsForRange returns the injected GlobalExitRoots for the given block range
func (e *EVMChainGERReader) GetInjectedGERsForRange(ctx context.Context,
	fromBlock, toBlock uint64) (map[common.Hash]InjectedGER, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	// first get all inserted GERs in the block range
	insertIter, err := e.l2GERManager.FilterUpdateHashChainValue(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		log.Errorf("failed to create InsertGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	injectedGERs := make(map[common.Hash]InjectedGER, 0)

	for insertIter.Next() {
		if insertIter.Error() != nil {
			return nil, insertIter.Error()
		}

		ger := insertIter.Event.NewGlobalExitRoot
		injectedGERs[ger] = InjectedGER{
			BlockNumber:    insertIter.Event.Raw.BlockNumber,
			BlockIndex:     insertIter.Event.Raw.Index,
			GlobalExitRoot: ger,
		}
	}

	if err = insertIter.Close(); err != nil {
		log.Errorf("failed to close InsertGlobalExitRoot event iterator: %v", err)
	}

	// then get all removed GERs in the block range
	// and remove them from the injectedGERs map
	removalIter, err := e.l2GERManager.FilterUpdateRemovalHashChainValue(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		log.Errorf("failed to create RemoveGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	for removalIter.Next() {
		if removalIter.Error() != nil {
			return nil, removalIter.Error()
		}

		ger := removalIter.Event.RemovedGlobalExitRoot
		delete(injectedGERs, ger)
	}

	if err = removalIter.Close(); err != nil {
		log.Errorf("failed to close RemoveGlobalExitRoot event iterator: %v", err)
	}

	return injectedGERs, nil
}
