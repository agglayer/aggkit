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
	sovereignRollupAddrSC types.MultisigContract
	sovereignRollupAddr   common.Address
	overrideURL           *CommitteeOverride
}

// CommitteeOverride is used to override the URLs of the committee members
type CommitteeOverride struct {
	// URL[oldURL] = newURL
	URLMapping map[string]string
}

func (c *CommitteeOverride) String() string {
	if c == nil {
		return "CommitteeOverride{nil}"
	}
	return fmt.Sprintf("CommitteeOverride{URL: %v}", c.URLMapping)
}

// ReplaceURL replaces the URLs of the committee members with the ones in the override map
func (m *CommitteeOverride) ReplaceURL(
	committee []aggchainbase.IAggchainSignersSignerInfo) []aggchainbase.IAggchainSignersSignerInfo {
	if m == nil || len(m.URLMapping) == 0 {
		return committee
	}
	newCommittee := make([]aggchainbase.IAggchainSignersSignerInfo, 0, len(committee))
	for _, member := range committee {
		newMember := member
		if url, ok := m.URLMapping[member.Url]; ok {
			newMember.Url = url
		}
		newCommittee = append(newCommittee, newMember)
	}
	return newCommittee
}

// NewBaseMultisigCommitteeQuery creates a new instance of BaseMultisigCommitteeQuery
func NewBaseMultisigCommitteeQuery(sovereignRollupAddr common.Address,
	l1Client aggkittypes.BaseEthereumClienter,
	overrideURL *CommitteeOverride) (*BaseMultisigCommitteeQuery, error) {
	sovereignRollupAddrSC, err := aggchainbase.NewAggchainbaseCaller(
		sovereignRollupAddr, l1Client)
	if err != nil {
		return nil, err
	}

	return &BaseMultisigCommitteeQuery{
		sovereignRollupAddrSC: sovereignRollupAddrSC,
		sovereignRollupAddr:   sovereignRollupAddr,
		overrideURL:           overrideURL,
	}, nil
}

// GetMultisigCommittee reads the multisig committee from the smart contract for a certain block
func (m *BaseMultisigCommitteeQuery) GetMultisigCommittee(
	ctx context.Context, blockNum *big.Int) (*types.MultisigCommittee, error) {
	callOpts := &bind.CallOpts{Pending: false, BlockNumber: blockNum}
	threshold, err := m.sovereignRollupAddrSC.Threshold(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the signatures threshold from for block %d to rollupAddr %s : %w",
			blockNum, m.sovereignRollupAddr.String(), err)
	}

	aggChainSigners, err := m.sovereignRollupAddrSC.GetAggchainSignerInfos(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the committee signers for block %d to rollupAddr %s: %w",
			blockNum, m.sovereignRollupAddr.String(), err)
	}
	if m.overrideURL != nil {
		aggChainSigners = m.overrideURL.ReplaceURL(aggChainSigners)
	}

	signerInfos := make([]*types.SignerInfo, 0, len(aggChainSigners))
	for _, aggChainSigner := range aggChainSigners {
		signerInfos = append(signerInfos, types.NewSignerInfo(aggChainSigner.Url, aggChainSigner.Addr))
	}

	return types.NewMultisigCommittee(signerInfos, threshold)
}
