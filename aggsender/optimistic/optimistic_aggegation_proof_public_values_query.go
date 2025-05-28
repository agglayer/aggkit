package optimistic

import (
	"fmt"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/opnode"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// OpNodeClienter is an interface that defines the methods for interacting with the OpNode client.
type OpNodeClienter interface {
	OutputAtBlockRoot(blockNum uint64) (common.Hash, error)
}

// The real object that implements OpNodeClienter is opnode.OpNodeClient
var _ OpNodeClienter = (*opnode.OpNodeClient)(nil)

// TrustedSequencerQuerier it's the object that returns the trusted sequencer address that
// usually it's obtanied from rollup contract
type TrustedSequencerQuerier interface {
	TrustedSequencer() (common.Address, error)
}

// The real object that implements TrustedSequencerQuerier is etherman.Client
var _ TrustedSequencerQuerier = (*etherman.Client)(nil)

// FEPContractQuerier is an interface that defines the methods for interacting with the FEP contract.
type FEPContractQuerier interface {
	RollupConfigHash(opts *bind.CallOpts) ([32]byte, error)
	AggregationVkey(opts *bind.CallOpts) ([32]byte, error)
}

var _ FEPContractQuerier = (*aggchainfep.Aggchainfep)(nil)

// OptimisticAggregationProofPublicValuesQuerier defines an interface for querying aggregation proof public values in optimistic mode.
type OptimisticAggregationProofPublicValuesQuerier interface {
	GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock uint64,
		l1InfoTreeLeafHash common.Hash) (*AggregationProofPublicValues, error)
}

var _OptimisticAggregationProofPublicValuesQuerier = (*OptimisticAggregationProofPublicValuesQuery)(nil)

type OptimisticAggregationProofPublicValuesQuery struct {
	aggchainFEPContract  FEPContractQuerier
	aggchainFEPAddr      common.Address
	opNodeClient         OpNodeClienter
	proverAddressQuerier TrustedSequencerQuerier
}

func NewOptimisticAggregationProofPublicValuesQuery(
	aggchainFEPContract FEPContractQuerier,
	aggchainFEPAddr common.Address,
	opNodeClient OpNodeClienter,
	proverAddressQuerier TrustedSequencerQuerier,
) *OptimisticAggregationProofPublicValuesQuery {
	return &OptimisticAggregationProofPublicValuesQuery{
		aggchainFEPContract:  aggchainFEPContract,
		aggchainFEPAddr:      aggchainFEPAddr,
		opNodeClient:         opNodeClient,
		proverAddressQuerier: proverAddressQuerier,
	}
}

// The parametameters are contained in the AggchainProofRequest struct
// LastProvenBlock =  req.LastProvenBlock
// RequestedEndBlock = req.RequestedEndBlock
// L1InfoTreeLeafHash = req.L1InfoTreeLeaf.Hash
func (o *OptimisticAggregationProofPublicValuesQuery) GetAggregationProofPublicValuesData(lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeLeafHash common.Hash) (*AggregationProofPublicValues, error) {
	l2PreRoot, err := o.opNodeClient.OutputAtBlockRoot(lastProvenBlock)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get l2PreRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w", lastProvenBlock, err)
	}
	claimRoot, err := o.opNodeClient.OutputAtBlockRoot(requestedEndBlock)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get claimRoot opNodeClient.OutputAtBlockRoot(%d). Err: %w", requestedEndBlock, err)
	}
	rollupConfigHash, err := o.aggchainFEPContract.RollupConfigHash(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get rollupConfigHash from contract %s. Err: %w", o.aggchainFEPAddr, err)
	}
	multiBlockVKey, err := o.aggchainFEPContract.AggregationVkey(nil)
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get multiBlockVKey(AggregationVkey) from contract %s. Err: %w", o.aggchainFEPAddr, err)
	}
	proverAddress, err := o.proverAddressQuerier.TrustedSequencer()
	if err != nil {
		return nil, fmt.Errorf("optimisticModeSignQuery. Fails to get proverAddress. Err: %w", err)
	}
	return &AggregationProofPublicValues{
		l1Head:           l1InfoTreeLeafHash,
		l2PreRoot:        l2PreRoot,
		claimRoot:        claimRoot,
		l2BlockNumber:    requestedEndBlock,
		rollupConfigHash: rollupConfigHash,
		multiBlockVKey:   multiBlockVKey,
		proverAddress:    proverAddress,
	}, nil
}
