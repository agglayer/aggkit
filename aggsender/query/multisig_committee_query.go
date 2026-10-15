package query

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

var (
	_                     types.MultisigQuerier  = (*BaseMultisigCommitteeQuery)(nil)
	_                     types.MultisigContract = (*aggchainbase.Aggchainbase)(nil)
	aggchainECDSAMultisig                        = [2]byte{0, 0}
	aggchainFEP                                  = [2]byte{0, 1}
	aggchainQBFT                                 = [2]byte{0, 2}
)

const consensusTypeMultiECDSAAndSP1 = uint32(1)

type BaseMultisigCommitteeQuery struct {
	sovereignRollupSC   types.MultisigContract
	sovereignRollupAddr common.Address
	overrideURL         *CommitteeOverride
}

// CommitteeOverride is used to override the URLs of the committee members
type CommitteeOverride struct {
	// oldURL -> newURL
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
		sovereignRollupSC:   sovereignRollupAddrSC,
		sovereignRollupAddr: sovereignRollupAddr,
		overrideURL:         overrideURL,
	}, nil
}

// GetMultisigCommittee reads the multisig committee from the smart contract for a certain block
func (m *BaseMultisigCommitteeQuery) GetMultisigCommittee(
	ctx context.Context, blockNum *big.Int) (*types.MultisigCommittee, error) {
	callOpts := &bind.CallOpts{Pending: false, BlockNumber: blockNum}
	threshold, err := m.sovereignRollupSC.Threshold(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the signatures threshold for block %d (rollupAddr %s): %w",
			blockNum, m.sovereignRollupAddr.String(), err)
	}

	aggChainSigners, err := m.sovereignRollupSC.GetAggchainSignerInfos(callOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to query the committee signers for block %d (rollupAddr %s): %w",
			blockNum, m.sovereignRollupAddr.String(), err)
	}
	if m.overrideURL != nil {
		aggChainSigners = m.overrideURL.ReplaceURL(aggChainSigners)
	}

	signerInfos := make([]*types.SignerInfo, 0, len(aggChainSigners))
	for _, aggChainSigner := range aggChainSigners {
		signerInfos = append(signerInfos, types.NewSignerInfo(aggChainSigner.Url, aggChainSigner.Addr))
	}
	if !threshold.IsUint64() {
		return nil, fmt.Errorf("threshold is not uint64: %s", threshold.String())
	}
	return types.NewMultisigCommittee(signerInfos, threshold.Uint64())
}

// ContractMode returns the mode of the multisig contract (PP or FEP)
func (m *BaseMultisigCommitteeQuery) ContractMode() (types.AggsenderMode, error) {
	var none types.AggsenderMode
	if m == nil {
		return none, fmt.Errorf("object is nil")
	}
	consensusType, err := m.sovereignRollupSC.CONSENSUSTYPE(&bind.CallOpts{})
	if err != nil {
		return none, fmt.Errorf("failed to get consensus type from contract: %w", err)
	}
	if consensusType != consensusTypeMultiECDSAAndSP1 {
		return none, fmt.Errorf("consensus type must be 1 always: %d", consensusType)
	}
	aggchainType, err := m.sovereignRollupSC.AGGCHAINTYPE(&bind.CallOpts{})
	if err != nil {
		return none, fmt.Errorf("failed to get aggchain type from contract: %w", err)
	}
	switch aggchainType {
	case aggchainECDSAMultisig:
		return types.PessimisticProofMode, nil
	case aggchainFEP, aggchainQBFT:
		// QBFT uses the same aggchain-proof flow as FEP (certificate + aggkit-prover proof)
		return types.AggchainProofMode, nil
	default:
		return none, fmt.Errorf("unsupported aggchain type: %v", aggchainType)
	}
}

// AggchainType returns the aggchain type (AGGCHAIN_TYPE) of the rollup contract
func (m *BaseMultisigCommitteeQuery) AggchainType() (types.AggchainType, error) {
	if m == nil {
		return "", fmt.Errorf("object is nil")
	}
	aggchainType, err := m.sovereignRollupSC.AGGCHAINTYPE(&bind.CallOpts{})
	if err != nil {
		return "", fmt.Errorf("failed to get aggchain type from contract: %w", err)
	}
	return aggchainTypeFromBytes(aggchainType)
}

// aggchainTypeFromBytes maps the bytes2 AGGCHAIN_TYPE value to the AggchainType enum
func aggchainTypeFromBytes(aggchainType [2]byte) (types.AggchainType, error) {
	switch aggchainType {
	case aggchainECDSAMultisig:
		return types.AggchainTypeECDSAMultisig, nil
	case aggchainFEP:
		return types.AggchainTypeFEP, nil
	case aggchainQBFT:
		return types.AggchainTypeQBFT, nil
	default:
		return "", fmt.Errorf("unsupported aggchain type: %v", aggchainType)
	}
}

func (m *BaseMultisigCommitteeQuery) ResolveAutoMode(cfgMode types.AggsenderMode) (types.AggsenderMode, error) {
	switch cfgMode {
	case types.PessimisticProofMode, types.AggchainProofMode:
		return cfgMode, nil
	case types.AutoMode:
		mode, err := m.ContractMode()
		if err != nil {
			return mode, fmt.Errorf("aggsender mode is AUTO, but can't get contract mode from rollup contract: %w", err)
		}
		return mode, nil
	default:
		return "", fmt.Errorf("unknown aggsender mode: %s", cfgMode)
	}
}
