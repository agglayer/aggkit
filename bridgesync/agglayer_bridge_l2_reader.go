package bridgesync

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// AgglayerBridgeL2Reader provides functionality to read and interact with the AggLayer Bridge L2 contract.
// It encapsulates the contract instance and provides methods to query bridge-related data from the L2 chain.
type AgglayerBridgeL2Reader struct {
	agglayerBridgeL2 *agglayerbridgel2.Agglayerbridgel2
}

// NewAgglayerBridgeL2Reader creates a new instance of AgglayerBridgeL2Reader.
// It initializes the contract instance using the provided bridge address and L2 client.
//
// Parameters:
//   - bridgeAddr: The Ethereum address of the AggLayer Bridge L2 contract
//   - l2Client: The Ethereum client for interacting with the L2 chain
//
// Returns:
//   - *AgglayerBridgeL2Reader: A new reader instance
//   - error: Any error that occurred during contract initialization
func NewAgglayerBridgeL2Reader(
	bridgeAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter,
) (*AgglayerBridgeL2Reader, error) {
	agglayerBridgeL2Contract, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, l2Client)
	if err != nil {
		return nil, err
	}
	return &AgglayerBridgeL2Reader{agglayerBridgeL2: agglayerBridgeL2Contract}, nil
}

// GetUnsetClaimsForBlockRange retrieves all unset claims (unclaims) within a specified block range.
// It filters the UpdatedUnsetGlobalIndexHashChain events from the bridge contract and converts them
// into Unclaim objects for further processing.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - fromBlock: The starting block number for the search range (inclusive)
//   - toBlock: The ending block number for the search range (inclusive)
//
// Returns:
//   - []*types.Unclaim: A slice of Unclaim objects containing global index, block number, and block index
//   - error: Any error that occurred during the event filtering or iteration
func (r *AgglayerBridgeL2Reader) GetUnsetClaimsForBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]*types.Unclaim, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}
	unclaimIterator, err := r.agglayerBridgeL2.FilterUpdatedUnsetGlobalIndexHashChain(
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

	if unclaimIterator.Error() != nil {
		return nil, unclaimIterator.Error()
	}

	return unclaims, nil
}
