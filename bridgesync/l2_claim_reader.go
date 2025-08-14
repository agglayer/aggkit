package bridgesync

import (
	"context"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/bridgel2sovereignchain"
	"github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

type L2ClaimReader struct {
	bridgeSovereignChain *bridgel2sovereignchain.Bridgel2sovereignchain
}

func NewL2ClaimReader(bridgeAddr common.Address, l2Client aggkittypes.BaseEthereumClienter) (*L2ClaimReader, error) {
	bridgeSovereignChainContract, err := bridgel2sovereignchain.NewBridgel2sovereignchain(bridgeAddr, l2Client)
	if err != nil {
		return nil, err
	}
	return &L2ClaimReader{bridgeSovereignChain: bridgeSovereignChainContract}, nil
}

func (r *L2ClaimReader) GetUnclaimBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]types.Unclaim, error) {
	unclaimIterator, err := r.bridgeSovereignChain.FilterUpdatedUnsetGlobalIndexHashChain(
		&bind.FilterOpts{Start: fromBlock, End: &toBlock})
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := unclaimIterator.Close(); err != nil {
			log.Errorf("failed to close UpdatedUnsetGlobalIndexHashChain iterator: %v", err)
		}
	}()

	unclaims := make([]types.Unclaim, 0)
	for unclaimIterator.Next() {
		globalIndex := unclaimIterator.Event.UnsetGlobalIndex
		unclaims = append(unclaims, types.Unclaim{
			GlobalIndex: globalIndex,
			BlockNumber: unclaimIterator.Event.Raw.BlockNumber,
			BlockIndex:  uint(unclaimIterator.Event.Raw.Index),
		})
	}

	return unclaims, nil
}
