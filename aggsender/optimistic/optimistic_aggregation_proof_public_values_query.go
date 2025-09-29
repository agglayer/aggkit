package optimistic

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainfep"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/opnode"
	"github.com/ethereum/go-ethereum/common"
)

// This is just to check in build time that the expected objects fulfill the interfaces
var (
	_ OpNodeClienter                                = (*opnode.OpNodeClient)(nil)
	_ FEPContractQuerier                            = (*aggchainfep.Aggchainfep)(nil)
	_ OptimisticAggregationProofPublicValuesQuerier = (*OptimisticAggregationProofPublicValuesQuery)(nil)
)

// OptimisticAggregationProofPublicValuesQuery implements OptimisticAggregationProofPublicValuesQuerier
type OptimisticAggregationProofPublicValuesQuery struct {
	aggchainFEPContract FEPContractQuerier
	aggchainFEPAddr     common.Address
	opNodeClient        OpNodeClienter
	proverAddress       common.Address
}

// NewOptimisticAggregationProofPublicValuesQuery creates a new instance of OptimisticAggregationProofPublicValuesQuery
func NewOptimisticAggregationProofPublicValuesQuery(
	aggchainFEPContract FEPContractQuerier,
	aggchainFEPAddr common.Address,
	opNodeClient OpNodeClienter,
	proverAddress common.Address,
) *OptimisticAggregationProofPublicValuesQuery {
	return &OptimisticAggregationProofPublicValuesQuery{
		aggchainFEPContract: aggchainFEPContract,
		aggchainFEPAddr:     aggchainFEPAddr,
		opNodeClient:        opNodeClient,
		proverAddress:       proverAddress,
	}
}

// GetAggregationProofPublicValuesData retrieves the AggregationProofPublicValue required for
// the optimistic aggregation proof
func (o *OptimisticAggregationProofPublicValuesQuery) GetAggregationProofPublicValuesData(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*types.AggregationProofPublicValues, error) {
	l2PreRoot, err := o.opNodeClient.OutputAtBlockRoot(lastProvenBlock)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. l2PreRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w",
			lastProvenBlock, err)
	}
	claimRoot, err := o.opNodeClient.OutputAtBlockRoot(requestedEndBlock)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. claimRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w",
			requestedEndBlock, err)
	}
	configName, err := o.aggchainFEPContract.SelectedOpSuccinctConfigName(nil)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. contract.SelectedOpSuccinctConfigName from contract %s. Err: %w",
			o.aggchainFEPAddr, err)
	}
	opConfig, err := o.aggchainFEPContract.OpSuccinctConfigs(nil, configName)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. contract.OpSuccinctConfigs from contract %s. Err: %w",
			o.aggchainFEPAddr, err)
	}

	trustedSequencerAddr := o.proverAddress
	if trustedSequencerAddr == aggkitcommon.ZeroAddress {
		// if proverAddress is zero, get the trusted sequencer from the contract
		trustedSequencerAddr, err = o.trustedSequencerAddr()
		if err != nil {
			return nil, fmt.Errorf("opAggProofPublicValuesQuery. trustedSequencerAddr from contract %s. Err: %w",
				o.aggchainFEPAddr, err)
		}
	}

	return &types.AggregationProofPublicValues{
		L1Head:           l1InfoTreeLeafHash,
		L2PreRoot:        l2PreRoot,
		ClaimRoot:        claimRoot,
		L2BlockNumber:    requestedEndBlock,
		RollupConfigHash: opConfig.RollupConfigHash,
		MultiBlockVKey:   opConfig.RangeVkeyCommitment,
		ProverAddress:    o.proverAddress,
	}, nil
}

func (o *OptimisticAggregationProofPublicValuesQuery) trustedSequencerAddr() (common.Address, error) {
	signers, err := o.aggchainFEPContract.GetAggchainSigners(nil)
	if err != nil {
		return aggkitcommon.ZeroAddress, fmt.Errorf("failed to get aggchain signers from contract %s. Err: %w",
			o.aggchainFEPAddr.String(), err)
	}

	if len(signers) < 1 {
		return aggkitcommon.ZeroAddress, fmt.Errorf("no signers found in the aggchainFEP contract %s. Required at least one",
			o.aggchainFEPAddr.String())
	}

	return signers[0], nil
}
