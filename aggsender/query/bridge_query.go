package query

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
)

var ErrNoBridgeExits = fmt.Errorf("no bridge exits consumed")

var _ types.BridgeQuerier = (*bridgeDataQuerier)(nil)

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

// GetBridgesAndClaims retrieves bridges and optionally claims within a specified block range.
//
// Parameters:
//   - ctx: The context for managing request deadlines and cancellations.
//   - fromBlock: The starting block number for the query range.
//   - toBlock: The ending block number for the query range.
//   - allowEmptyCert: A flag indicating whether to retrieve claims even if certificates are empty.
//
// Returns:
//   - []bridgesync.Bridge: A slice of Bridge objects retrieved within the specified block range.
//   - []bridgesync.Claim: A slice of Claim objects retrieved within the specified block range (if allowEmptyCert).
//   - error: An error if any occurs during the retrieval of bridges or claims.
//
// Errors:
//   - Returns an error if there is an issue retrieving bridges or claims from the bridgeSyncer.
func (b *bridgeDataQuerier) GetBridgesAndClaims(
	ctx context.Context,
	fromBlock, toBlock uint64,
	allowEmptyCert bool,
) ([]bridgesync.Bridge, []bridgesync.Claim, error) {
	bridges, err := b.bridgeSyncer.GetBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bridges: %w", err)
	}

	if !allowEmptyCert && len(bridges) == 0 {
		return nil, nil, fmt.Errorf("%w, no need to send a certificate from block: %d to block: %d",
			ErrNoBridgeExits, fromBlock, toBlock)
	}

	// If allowEmptyCert is true or if there are bridges, retrieve claims
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
