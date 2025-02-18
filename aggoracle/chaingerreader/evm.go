package chaingerreader

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	"github.com/agglayer/aggkit/aggoracle/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// EVMChainGERReader is a component used to read GlobalExitRootManager L2 contract
type EVMChainGERReader struct {
	l2GERManager types.L2GERManagerContract
}

// NewEVMChainGERReader creates a new L2Etherman
func NewEVMChainGERReader(l2GERManagerAddr common.Address, l2Client types.EthClienter) (*EVMChainGERReader, error) {
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
	fromBlock, toBlock uint64) ([]common.Hash, error) {
	iter, err := e.l2GERManager.FilterInsertGlobalExitRoot(
		&bind.FilterOpts{
			Context: ctx,
			Start:   fromBlock,
			End:     &toBlock,
		}, nil, nil)
	if err != nil {
		log.Errorf("failed to create InsertGlobalExitRoot event iterator: %v", err)
		return nil, err
	}

	var gerHashes []common.Hash

	for iter.Next() {
		if iter.Error() != nil {
			return nil, iter.Error()
		}

		gerHashes = append(gerHashes, iter.Event.NewGlobalExitRoot)
	}

	if err = iter.Close(); err != nil {
		log.Errorf("failed to close InsertGlobalExitRoot event iterator: %v", err)
	}

	return gerHashes, nil
}
