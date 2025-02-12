package aggsender

import (
	"context"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// L2GERManager is an interface to interact with the GlobalExitRootManager contract
type L2GERManager interface {
	BridgeAddress(*bind.CallOpts) (common.Address, error)
	FilterInsertGlobalExitRoot(opts *bind.FilterOpts, newGlobalExitRoot [][32]byte, newHashChainValue [][32]byte) (
		*globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchainInsertGlobalExitRootIterator, error)
}

// L2Etherman is a component used to interact with L2 contracts
type L2Etherman struct {
	l2GERManager L2GERManager
}

// NewL2Etherman creates a new L2Etherman
func NewL2Etherman(l2GERManagerAddr common.Address, l2Client types.EthClient) (*L2Etherman, error) {
	l2GERManager, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
		l2GERManagerAddr, l2Client)
	if err != nil {
		return nil, err
	}
	return newL2Etherman(l2GERManager, l2GERManagerAddr)
}

func newL2Etherman(l2GERManager L2GERManager, l2GERManagerAddr common.Address) (*L2Etherman, error) {
	if err := checkGlobalExitRootManagerContract(l2GERManager, l2GERManagerAddr); err != nil {
		return nil, err
	}

	return &L2Etherman{l2GERManager: l2GERManager}, nil
}

// checkGlobalExitRootManagerContract checks if the GlobalExitRootManager contract is valid on given address
func checkGlobalExitRootManagerContract(l2GERManager L2GERManager, contractAddr common.Address) error {
	bridgeAddr, err := l2GERManager.BridgeAddress(nil)
	if err != nil {
		return fmt.Errorf("fail sanity check GlobalExitRootManagerL2(%s) Contract. Err: %w", contractAddr.String(), err)
	}
	log.Infof("sanity check GlobalExitRootManagerL2(%s) OK. bridgeAddress: %s", contractAddr.String(), bridgeAddr.String())
	return nil
}

// GetInjectedGERsForRange returns the injected GlobalExitRoots for the given block range
func (e *L2Etherman) GetInjectedGERsForRange(ctx context.Context, fromBlock, toBlock uint64) ([]common.Hash, error) {
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
