package query

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.BridgeDataQuerier = (*bridgeDataQuerier)(nil)

// bridgeDataQuerier is a struct that holds the logic to query the bridge data
type bridgeDataQuerier struct {
	bridgeSyncer types.L2BridgeSyncer

	originNetwork uint32
}

// NewBridgeDataQuerier returns a new instance of the BridgeDataQuerier
func NewBridgeDataQuerier(bridgeSyncer types.L2BridgeSyncer) *bridgeDataQuerier {
	return &bridgeDataQuerier{
		bridgeSyncer:  bridgeSyncer,
		originNetwork: bridgeSyncer.OriginNetwork(),
	}
}

// GetBridgesAndClaims retrieves bridges and claims within a specified block range.
// Returns:
//   - A slice of bridgesync.Bridge representing the bridges found in the specified range.
//   - A slice of bridgesync.Claim representing the claims found in the specified range.
//   - An error if there is an issue retrieving the bridges or claims.
func (b *bridgeDataQuerier) GetBridgesAndClaims(
	ctx context.Context,
	fromBlock, toBlock uint64,
) ([]bridgesync.Bridge, []bridgesync.Claim, error) {
	bridges, err := b.bridgeSyncer.GetBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bridges: %w", err)
	}

	claims, err := b.bridgeSyncer.GetClaims(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting claims: %w", err)
	}

	return bridges, claims, nil
}

// GetExitRootByIndex retrieves the local exit root hash for a given index from the bridge syncer.
// Returns:
//   - common.Hash: The hash of the exit root corresponding to the given index.
//   - error: An error if the operation fails, including details about the failure.
func (b *bridgeDataQuerier) GetExitRootByIndex(ctx context.Context, index uint32) (common.Hash, error) {
	exitRoot, err := b.bridgeSyncer.GetExitRootByIndex(ctx, index)
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting exit root by index: %d. Error: %w", index, err)
	}

	return exitRoot.Hash, nil
}

// GetLastProcessedBlock retrieves the last processed block number from the bridge syncer.
// Returns:
//   - uint64: The last processed block number.
//   - error: An error if there is an issue retrieving the block number.
func (b *bridgeDataQuerier) GetLastProcessedBlock(ctx context.Context) (uint64, error) {
	lastProcessedBlock, err := b.bridgeSyncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return 0, fmt.Errorf("error getting last processed block: %w", err)
	}

	return lastProcessedBlock, nil
}

// OriginNetwork returns the origin network id related to given bridge syncer.
func (b *bridgeDataQuerier) OriginNetwork() uint32 {
	return b.originNetwork
}
