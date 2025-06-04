package optimistic

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	"github.com/agglayer/aggkit/aggoracle/types"
	"github.com/ethereum/go-ethereum/common"
)

type OptimisticModeQuerierFromContract struct {
	aggchainFEPContract FEPContractQuerier
	aggchainFEPAddr     common.Address
	// op-node client
}

func NewOptimisticModeQuerierFromContract(aggchainFEPAddr common.Address,
	backend types.EthClienter) (*OptimisticModeQuerierFromContract, error) {
	contract, err := aggchainfep.NewAggchainfep(aggchainFEPAddr, backend)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeQuerierFromContract: error creating aggchainfep contract %s: %w",
			aggchainFEPAddr, err)
	}
	return &OptimisticModeQuerierFromContract{
		aggchainFEPContract: contract,
	}, nil
}

func (q *OptimisticModeQuerierFromContract) IsOptimisticModeOn() (bool, error) {
	optimisticMode, err := q.aggchainFEPContract.OptimisticMode(nil)
	if err != nil {
		return false, fmt.Errorf("optimisticModeQuerierFromContract: error checking optimisticMode in contract %s. Err: %w",
			q.aggchainFEPAddr, err)
	}
	return optimisticMode, nil
}
