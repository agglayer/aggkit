package query

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainbase"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var (
	_ types.MultisigQuerier  = (*BaseMultisigCommitteeQuery)(nil)
	_ types.MultisigContract = (*aggchainbase.Aggchainbase)(nil)
)

type BaseMultisigCommitteeQuery struct {
	multisigCommitteeSC   types.MultisigContract
	multisigCommitteeAddr common.Address
}

// NewBaseMultisigCommitteeQuery creates a new instance of BaseMultisigCommitteeQuery
func NewBaseMultisigCommitteeQuery(multisigCommitteeAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter) (*BaseMultisigCommitteeQuery, error) {
	multisigCommitteeSC, err := aggchainbase.NewAggchainbaseCaller(
		multisigCommitteeAddr, l1Client)
	if err != nil {
		return nil, err
	}

	return &BaseMultisigCommitteeQuery{
		multisigCommitteeSC:   multisigCommitteeSC,
		multisigCommitteeAddr: multisigCommitteeAddr,
	}, nil
}

// GetMultisigCommittee reads the multisig committee from the smart contract for a certain block
func (m *BaseMultisigCommitteeQuery) GetMultisigCommittee(
	ctx context.Context, blockNum *big.Int) (*types.MultisigCommittee, error) {
	callOpts := &bind.CallOpts{Pending: false, BlockNumber: blockNum}
	threshold, err := m.multisigCommitteeSC.Threshold(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the signatures threshold for block %d: %w", blockNum, err)
	}

	aggChainSigners, err := m.multisigCommitteeSC.GetAggchainSignerInfos(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the committee signers for block %d: %w", blockNum, err)
	}

	signerInfos := make([]*types.SignerInfo, 0, len(aggChainSigners))
	for _, aggChainSigner := range aggChainSigners {
		signerInfos = append(signerInfos, types.NewSignerInfo(aggChainSigner.Url, aggChainSigner.Addr))
	}

	return types.NewMultisigCommittee(signerInfos, threshold)
}
