package query

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/aggchain-ecdsa-multisig/aggchainecdsamultisig"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var (
	_ types.MultisigQuerier  = (*ECDSAMultisigCommitteeQuery)(nil)
	_ types.MultisigContract = (*aggchainecdsamultisig.Aggchainecdsamultisig)(nil)
)

type ECDSAMultisigCommitteeQuery struct {
	multisigCommitteeSC   types.MultisigContract
	multisigCommitteeAddr common.Address
}

// NewECDSAMultisigCommitteeQuery creates a new instance of ECDSAMultisigCommitteeQuery
func NewECDSAMultisigCommitteeQuery(multisigCommitteeAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter) (*ECDSAMultisigCommitteeQuery, error) {
	multisigCommitteeSC, err := aggchainecdsamultisig.NewAggchainecdsamultisigCaller(
		multisigCommitteeAddr, l1Client)
	if err != nil {
		return nil, err
	}

	return &ECDSAMultisigCommitteeQuery{
		multisigCommitteeSC:   multisigCommitteeSC,
		multisigCommitteeAddr: multisigCommitteeAddr,
	}, nil
}

// GetMultisigCommittee reads the multisig committee from the smart contract for a certain block
func (m *ECDSAMultisigCommitteeQuery) GetMultisigCommittee(
	ctx context.Context, blockNum *big.Int) (*types.MultisigCommittee, error) {
	callOpts := &bind.CallOpts{Pending: false, BlockNumber: blockNum}
	threshold, err := m.multisigCommitteeSC.Threshold(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the signatures threshold for block %d: %w", blockNum, err)
	}

	signers, err := m.multisigCommitteeSC.GetSigners(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the committee signers for block %d: %w", blockNum, err)
	}

	signerInfos := make([]*types.SignerInfo, 0, len(signers))
	for _, signer := range signers {
		// TODO: Populate the URLs once they are on the smart contract
		signerInfos = append(signerInfos, types.NewSignerInfo("", signer))
	}

	return types.NewMultisigCommittee(signerInfos, threshold)
}
