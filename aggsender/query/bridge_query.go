package query

import (
	"context"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var _ types.BridgeQuerier = (*bridgeDataQuerier)(nil)

// bridgeDataQuerier is a struct that holds the logic to query the bridge data
type bridgeDataQuerier struct {
	log                     types.Logger
	bridgeSyncer            types.L2BridgeSyncer
	delayBetweenRetries     time.Duration
	bridgeL2SovereignReader types.BridgeL2SovereignReader

	originNetwork uint32
}

// NewBridgeDataQuerier returns a new instance of the BridgeDataQuerier
func NewBridgeDataQuerier(
	log types.Logger,
	bridgeSyncer types.L2BridgeSyncer,
	delayBetweenRetries time.Duration,
	bridgeL2SovereignReader types.BridgeL2SovereignReader,
) *bridgeDataQuerier {
	return &bridgeDataQuerier{
		log:                     log,
		bridgeSyncer:            bridgeSyncer,
		delayBetweenRetries:     delayBetweenRetries,
		originNetwork:           bridgeSyncer.OriginNetwork(),
		bridgeL2SovereignReader: bridgeL2SovereignReader,
	}
}

// GetBridgesAndClaims retrieves bridges and optionally claims within a specified block range.
//
// Parameters:
//   - ctx: The context for managing request deadlines and cancellations.
//   - fromBlock: The starting block number for the query range.
//   - toBlock: The ending block number for the query range.
//
// Returns:
//   - []bridgesync.Bridge: A slice of Bridge objects retrieved within the specified block range.
//   - []bridgesync.Claim: A slice of Claim objects retrieved within the specified block range.
//   - error: An error if any occurs during the retrieval of bridges or claims.
//
// Errors:
//   - Returns an error if there is an issue retrieving bridges or claims from the bridgeSyncer.
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

// WaitForSyncerToCatchUp waits for the bridge syncer to catch up to a specified block.
func (b *bridgeDataQuerier) WaitForSyncerToCatchUp(ctx context.Context, block uint64) error {
	b.log.Infof("bridgeDataQuerier - waiting for L2 syncer to catch up to block: %d", block)
	defer b.log.Infof("bridgeDataQuerier - finished waiting for L2 syncer to catch up to block: %d", block)

	if b.delayBetweenRetries <= 0 {
		b.log.Warnf("bridgeDataQuerier - invalid delayBetweenRetries: %v, falling back to default value of 1s",
			b.delayBetweenRetries)
		b.delayBetweenRetries = time.Second
	}

	ticker := time.NewTicker(b.delayBetweenRetries)
	defer ticker.Stop()

	for {
		lastProcessedBlock, err := b.bridgeSyncer.GetLastProcessedBlock(ctx)
		if err != nil {
			return fmt.Errorf("bridgeDataQuerier - error getting last processed block: %w", err)
		}

		if lastProcessedBlock >= block {
			b.log.Infof("bridgeDataQuerier - L2 syncer caught up to block: %d", block)
			return nil
		}

		b.log.Infof("bridgeDataQuerier - waiting for L2 syncer to catch up to block: %d, current last processed block: %d",
			block, lastProcessedBlock)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue // Keep checking until the condition is met
		}
	}
}

func (b *bridgeDataQuerier) GetUnsetClaimsForBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]*agglayertypes.Unclaim, error) {
	unclaims, err := b.bridgeL2SovereignReader.GetUnsetClaimsForBlockRange(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, fmt.Errorf("failed to get unclaim block range: %w", err)
	}

	unclaimsConverted := make([]*agglayertypes.Unclaim, len(unclaims))

	// Add the unclaim hash to the unclaim
	for i, unclaim := range unclaims {
		claim, err := b.bridgeSyncer.GetClaimByGlobalIndex(
			ctx, unclaim.BlockNumber, uint32(unclaim.BlockIndex), unclaim.GlobalIndex.String(),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to get claim by global index: %w, blockNumber: %d, blockIndex: %d, globalIndex: %s",
				err, unclaim.BlockNumber, unclaim.BlockIndex, unclaim.GlobalIndex,
			)
		}
		ibe, err := converters.ConvertToImportedBridgeExitWithoutClaimData(claim)
		if err != nil {
			return nil, fmt.Errorf("failed to convert claim to imported bridge exit: %w", err)
		}

		// Create a custom hash for ImportedBridgeExit without ClaimData
		// This avoids the panic that would occur when calling Hash() on an ImportedBridgeExit with nil ClaimData
		var unclaimHash common.Hash
		if ibe.ClaimData == nil {
			// Custom hash calculation using only BridgeExit and GlobalIndex
			unclaimHash = crypto.Keccak256Hash(
				ibe.BridgeExit.Hash().Bytes(),
				ibe.GlobalIndex.Hash().Bytes(),
			)
		} else {
			// Use the standard Hash method if ClaimData is available
			unclaimHash = ibe.Hash()
		}

		unclaimsConverted[i] = &agglayertypes.Unclaim{
			UnclaimHash: unclaimHash,
			BlockNumber: unclaim.BlockNumber,
			BlockIndex:  unclaim.BlockIndex,
		}
	}

	return unclaimsConverted, nil
}
