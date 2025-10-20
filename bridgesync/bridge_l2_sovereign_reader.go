package bridgesync

import (
	"context"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

type BridgeL2SovereignReader struct {
	bridgeSovereignChain *agglayerbridge.Agglayerbridge
}

func NewBridgeL2SovereignReader(
	bridgeAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter,
) (*BridgeL2SovereignReader, error) {
	bridgeSovereignChainContract, err := agglayerbridge.NewBridgel2sovereignchain(bridgeAddr, l2Client)
	if err != nil {
		return nil, err
	}
	return &BridgeL2SovereignReader{bridgeSovereignChain: bridgeSovereignChainContract}, nil
}

func (r *BridgeL2SovereignReader) GetUnsetClaimsForBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]*types.Unclaim, error) {
	unclaimIterator, err := r.bridgeSovereignChain.FilterUpdatedUnsetGlobalIndexHashChain(
		&bind.FilterOpts{Context: ctx, Start: fromBlock, End: &toBlock})
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := unclaimIterator.Close(); err != nil {
			log.Errorf("failed to close UpdatedUnsetGlobalIndexHashChain iterator: %v", err)
		}
	}()

	unclaims := make([]*types.Unclaim, 0)
	for unclaimIterator.Next() {
		globalIndex := unclaimIterator.Event.UnsetGlobalIndex
		log.Infof("unset claim: %s at block %d, index %d", new(big.Int).SetBytes(globalIndex[:]),
			unclaimIterator.Event.Raw.BlockNumber, unclaimIterator.Event.Raw.Index)
		unclaims = append(unclaims, &types.Unclaim{
			GlobalIndex: new(big.Int).SetBytes(globalIndex[:]),
			BlockNumber: unclaimIterator.Event.Raw.BlockNumber,
			BlockIndex:  unclaimIterator.Event.Raw.Index,
		})
	}

	return unclaims, nil
}
