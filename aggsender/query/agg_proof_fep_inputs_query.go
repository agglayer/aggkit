package query

import (
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.FEPInputsQuerier = (*FEPInputsQuery)(nil)

type FEPInputsQuery struct {
	publicValuesQuery types.AggProofPublicValuesQuerier

	opNodeClient        types.OpNodeClienter
	aggchainFEPContract types.FEPContractQuerier
}

// NewFEPInputsQuery creates a new instance of FEPInputsQuery
func NewFEPInputsQuery(
	aggchainFEPContract types.FEPContractQuerier,
	aggchainFEPAddr common.Address,
	opNodeClient types.OpNodeClienter,
) types.FEPInputsQuerier {
	return &FEPInputsQuery{
		publicValuesQuery: NewAggProofPublicValuesQuery(
			aggchainFEPContract,
			aggchainFEPAddr,
			opNodeClient,
			aggkitcommon.ZeroAddress, // prover address is not needed for this query, we get it from the contract
		),
		opNodeClient:        opNodeClient,
		aggchainFEPContract: aggchainFEPContract,
	}
}

// GetPublicInputs retrieves the public inputs required for the verification of aggchain proof
func (f *FEPInputsQuery) GetPublicInputs(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*types.AggregationProofPublicValues, error) {
	return f.publicValuesQuery.GetAggregationProofPublicValuesData(
		lastProvenBlock,
		requestedEndBlock,
		l1InfoTreeLeafHash)
}

func (f *FEPInputsQuery) GetAggchainParams(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*types.AggchainParams, error) {
	publicValues, err := f.GetPublicInputs(lastProvenBlock, requestedEndBlock, l1InfoTreeLeafHash)
	if err != nil {
		return nil, fmt.Errorf("failed to get FEP public inputs: %w", err)
	}

	isOptimisticModeOn, err := f.aggchainFEPContract.OptimisticMode(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to check if optimistic mode is turned on: %w", err)
	}

	aggchainParams := &types.AggchainParams{
		AggregationProofPublicValues: *publicValues,
		OptimisticMode:               isOptimisticModeOn,
	}

	return aggchainParams, nil
}
