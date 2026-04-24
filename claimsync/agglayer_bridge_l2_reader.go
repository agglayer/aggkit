package claimsync

import (
	"context"
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// AgglayerBridgeL2Reader provides functionality to read and interact with the AggLayer Bridge L2 contract.
// It encapsulates the contract instance and provides methods to query bridge-related data from the L2 chain.
type AgglayerBridgeL2Reader struct {
	agglayerBridgeL2 *agglayerbridgel2.Agglayerbridgel2
	maxLogBlockRange uint64
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
	return NewAgglayerBridgeL2ReaderWithMaxLogBlockRange(bridgeAddr, l2Client, 0)
}

// NewAgglayerBridgeL2ReaderWithMaxLogBlockRange creates a new instance of AgglayerBridgeL2Reader
// with an optional proactive max block range for eth_getLogs queries.
func NewAgglayerBridgeL2ReaderWithMaxLogBlockRange(
	bridgeAddr common.Address,
	l2Client aggkittypes.BaseEthereumClienter,
	maxLogBlockRange uint64,
) (*AgglayerBridgeL2Reader, error) {
	agglayerBridgeL2Contract, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, l2Client)
	if err != nil {
		return nil, err
	}

	return &AgglayerBridgeL2Reader{
		agglayerBridgeL2: agglayerBridgeL2Contract,
		maxLogBlockRange: maxLogBlockRange,
	}, nil
}

// GetUnsetClaimsForBlockRange retrieves all unset claims (unclaims) within a specified block range.
// It filters the UpdatedUnsetGlobalIndexHashChain events from the bridge contract and converts them
// into Unclaim objects for further processing.
// If the block range is too large, it automatically splits the request into smaller chunks.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control
//   - fromBlock: The starting block number for the search range (inclusive)
//   - toBlock: The ending block number for the search range (inclusive)
//
// Returns:
//   - []types.Unclaim: A slice of Unclaim objects containing global index, block number, and block index
//   - error: Any error that occurred during the event filtering or iteration
func (r *AgglayerBridgeL2Reader) GetUnsetClaimsForBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]claimsynctypes.Unclaim, error) {
	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid block range: fromBlock(%d) > toBlock(%d)", fromBlock, toBlock)
	}

	if r.maxLogBlockRange > 0 && toBlock-fromBlock >= r.maxLogBlockRange {
		return r.getUnsetClaimsInChunks(ctx, fromBlock, toBlock, r.maxLogBlockRange)
	}

	return r.fetchUnsetClaimsWithFallbackChunking(ctx, fromBlock, toBlock)
}

func (r *AgglayerBridgeL2Reader) fetchUnsetClaimsWithFallbackChunking(ctx context.Context,
	fromBlock, toBlock uint64) ([]claimsynctypes.Unclaim, error) {
	unclaims, err := r.fetchUnsetClaims(ctx, fromBlock, toBlock)
	if err != nil {
		// Check if error is due to block range being too large
		maxRange, isMaxRangeErr := aggkitcommon.ParseMaxRangeFromError(err.Error())
		if isMaxRangeErr {
			return r.getUnsetClaimsInChunks(ctx, fromBlock, toBlock, maxRange)
		}

		return nil, err
	}

	return unclaims, nil
}

func (r *AgglayerBridgeL2Reader) getUnsetClaimsInChunks(ctx context.Context,
	fromBlock, toBlock, maxRange uint64) ([]claimsynctypes.Unclaim, error) {
	log.Debugf("block range too large, splitting into chunks of max %d blocks", maxRange)
	return aggkitcommon.ChunkedRangeQuery(
		ctx, fromBlock, toBlock, maxRange,
		r.fetchUnsetClaimsWithFallbackChunking,
		func(all, chunk []claimsynctypes.Unclaim) []claimsynctypes.Unclaim {
			return append(all, chunk...)
		},
		make([]claimsynctypes.Unclaim, 0),
	)
}

// fetchUnsetClaims performs the actual event filtering for a given block range
func (r *AgglayerBridgeL2Reader) fetchUnsetClaims(ctx context.Context,
	fromBlock, toBlock uint64) ([]claimsynctypes.Unclaim, error) {
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

	unclaims := make([]claimsynctypes.Unclaim, 0)
	for unclaimIterator.Next() {
		globalIndex := unclaimIterator.Event.UnsetGlobalIndex
		log.Infof("unset claim: %s at block %d, index %d", new(big.Int).SetBytes(globalIndex[:]),
			unclaimIterator.Event.Raw.BlockNumber, unclaimIterator.Event.Raw.Index)
		unclaims = append(unclaims, claimsynctypes.Unclaim{
			GlobalIndex: new(big.Int).SetBytes(globalIndex[:]),
			BlockNumber: unclaimIterator.Event.Raw.BlockNumber,
			LogIndex:    uint64(unclaimIterator.Event.Raw.Index),
		})
	}

	if unclaimIterator.Error() != nil {
		return nil, unclaimIterator.Error()
	}

	return unclaims, nil
}
