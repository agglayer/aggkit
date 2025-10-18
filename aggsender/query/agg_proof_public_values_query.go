package query

import (
	"errors"
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainfep"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/opnode"
	"github.com/ethereum/go-ethereum/common"
)

// This is just to check in build time that the expected objects fulfill the interfaces
var (
	_ types.OpNodeClienter              = (*opnode.OpNodeClient)(nil)
	_ types.FEPContractQuerier          = (*aggchainfep.Aggchainfep)(nil)
	_ types.AggProofPublicValuesQuerier = (*AggProofPublicValuesQuery)(nil)

	errNoSigners = errors.New("no signers found in the AggchainFEP contract. There should be at least one signer")
)

// AggProofPublicValuesQuery implements AggProofPublicValuesQuerier
type AggProofPublicValuesQuery struct {
	aggchainFEPContract types.FEPContractQuerier
	aggchainFEPAddr     common.Address
	opNodeClient        types.OpNodeClienter
	proverAddress       common.Address
}

// NewAggProofPublicValuesQuery creates a new instance of AggProofPublicValuesQuery
func NewAggProofPublicValuesQuery(
	aggchainFEPContract types.FEPContractQuerier,
	aggchainFEPAddr common.Address,
	opNodeClient types.OpNodeClienter,
	proverAddress common.Address,
) *AggProofPublicValuesQuery {
	return &AggProofPublicValuesQuery{
		aggchainFEPContract: aggchainFEPContract,
		aggchainFEPAddr:     aggchainFEPAddr,
		opNodeClient:        opNodeClient,
		proverAddress:       proverAddress,
	}
}

// GetAggregationProofPublicValuesData retrieves the AggregationProofPublicValue required for
// the aggchain proof
func (a *AggProofPublicValuesQuery) GetAggregationProofPublicValuesData(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*types.AggregationProofPublicValues, error) {
	l2PreRoot, err := a.opNodeClient.OutputAtBlockRoot(lastProvenBlock)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. l2PreRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w",
			lastProvenBlock, err)
	}
	claimRoot, err := a.opNodeClient.OutputAtBlockRoot(requestedEndBlock)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. claimRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w",
			requestedEndBlock, err)
	}
	configName, err := a.aggchainFEPContract.SelectedOpSuccinctConfigName(nil)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. contract.SelectedOpSuccinctConfigName from contract %s. Err: %w",
			a.aggchainFEPAddr, err)
	}
	opConfig, err := a.aggchainFEPContract.OpSuccinctConfigs(nil, configName)
	if err != nil {
		return nil, fmt.Errorf("opAggProofPublicValuesQuery. contract.OpSuccinctConfigs from contract %s. Err: %w",
			a.aggchainFEPAddr, err)
	}

	trustedSignerAddr := a.proverAddress
	if trustedSignerAddr == aggkitcommon.ZeroAddress {
		// if proverAddress is zero, get the trusted signer from the contract
		trustedSignerAddr, err = GetTrustedSignerAddr(a.aggchainFEPContract)
		if err != nil {
			return nil, fmt.Errorf("opAggProofPublicValuesQuery. trustedSignerAddr from contract %s. Err: %w",
				a.aggchainFEPAddr, err)
		}
	}

	return &types.AggregationProofPublicValues{
		L1Head:              l1InfoTreeLeafHash,
		L2PreRoot:           l2PreRoot,
		ClaimRoot:           claimRoot,
		L2BlockNumber:       requestedEndBlock,
		RollupConfigHash:    opConfig.RollupConfigHash,
		MultiBlockVKey:      opConfig.RangeVkeyCommitment,
		TrustedSigner:       trustedSignerAddr,
		AggregationVKeyHash: opConfig.AggregationVkey,
	}, nil
}

// GetTrustedSignerAddr retrieves the trusted signer address from the AggchainFEP contract
func GetTrustedSignerAddr(aggchainFEPContract types.FEPContractQuerier) (common.Address, error) {
	signers, err := aggchainFEPContract.GetAggchainSigners(nil)
	if err != nil {
		return aggkitcommon.ZeroAddress,
			fmt.Errorf("failed to get aggchain signers from AggchainFEP contract. Err: %w", err)
	}

	if len(signers) < 1 {
		return aggkitcommon.ZeroAddress, errNoSigners
	}

	return signers[0], nil
}
