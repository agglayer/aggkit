package query

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.BridgeQuerier = (*bridgeDataQuerier)(nil)

// bridgeDataQuerier is a struct that holds the logic to query the bridge data
type bridgeDataQuerier struct {
	log                    types.Logger
	bridgeSyncer           types.L2BridgeSyncer
	claimSyncer            claimsynctypes.ClaimSyncer
	delayBetweenRetries    time.Duration
	agglayerBridgeL2Reader types.AgglayerBridgeL2Reader

	originNetwork uint32
}

// NewBridgeDataQuerier returns a new instance of the BridgeDataQuerier
func NewBridgeDataQuerier(
	log types.Logger,
	bridgeSyncer types.L2BridgeSyncer,
	claimSyncer claimsynctypes.ClaimSyncer,
	delayBetweenRetries time.Duration,
	agglayerBridgeL2Reader types.AgglayerBridgeL2Reader,
) *bridgeDataQuerier {
	return &bridgeDataQuerier{
		log:                    log,
		bridgeSyncer:           bridgeSyncer,
		claimSyncer:            claimSyncer,
		delayBetweenRetries:    delayBetweenRetries,
		originNetwork:          bridgeSyncer.OriginNetwork(),
		agglayerBridgeL2Reader: agglayerBridgeL2Reader,
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
) ([]bridgesync.Bridge, []claimsynctypes.Claim, error) {
	bridges, err := b.bridgeSyncer.GetBridges(ctx, fromBlock, toBlock)
	if err != nil {
		return nil, nil, fmt.Errorf("error getting bridges: %w", err)
	}

	claims, err := b.claimSyncer.GetClaims(ctx, fromBlock, toBlock)
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

// GetLastProcessedBlock retrieves the last processed block number considering both the bridge syncer
// and the claim syncer. It returns the minimum of the two so that the reported block is one where
// both syncers have completed processing.
func (b *bridgeDataQuerier) GetLastProcessedBlock(ctx context.Context) (uint64, bool, error) {
	bridgeBlock, found, err := b.bridgeSyncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("error getting last processed block: %w", err)
	}
	if !found {
		return 0, false, nil
	}

	if b.claimSyncer == nil {
		return bridgeBlock, true, nil
	}

	claimBlock, claimFound, err := b.claimSyncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("error getting claim syncer last processed block: %w", err)
	}
	if !claimFound {
		return 0, false, nil
	}

	if claimBlock < bridgeBlock {
		return claimBlock, true, nil
	}
	return bridgeBlock, true, nil
}

// OriginNetwork returns the origin network id related to given bridge syncer.
func (b *bridgeDataQuerier) OriginNetwork() uint32 {
	return b.originNetwork
}

// WaitForSyncerToCatchUp waits for both the bridge syncer and the claim syncer to catch up to a specified block.
func (b *bridgeDataQuerier) WaitForSyncerToCatchUp(ctx context.Context, block uint64) error {
	b.log.Infof("bridgeDataQuerier - waiting for L2 syncers to catch up to block: %d", block)
	defer b.log.Infof("bridgeDataQuerier - finished waiting for L2 syncers to catch up to block: %d", block)

	if b.delayBetweenRetries <= 0 {
		b.log.Warnf("bridgeDataQuerier - invalid delayBetweenRetries: %v, falling back to default value of 1s",
			b.delayBetweenRetries)
		b.delayBetweenRetries = time.Second
	}

	ticker := time.NewTicker(b.delayBetweenRetries)
	defer ticker.Stop()

	for {
		bridgeReady, err := b.isSyncerCaughtUp(ctx, block)
		if err != nil {
			return fmt.Errorf("bridgeDataQuerier - error getting last processed block: %w", err)
		}

		claimReady, err := b.isClaimSyncerCaughtUp(ctx, block)
		if err != nil {
			return fmt.Errorf("bridgeDataQuerier - error checking claim syncer: %w", err)
		}

		if bridgeReady && claimReady {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			continue // Keep checking until the condition is met
		}
	}
}

// isSyncerCaughtUp checks whether the bridge syncer has processed up to the given block.
// Returns true if caught up, false if not yet.
func (b *bridgeDataQuerier) isSyncerCaughtUp(ctx context.Context, block uint64) (bool, error) {
	lastProcessedBlock, found, err := b.bridgeSyncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return false, err
	}

	if !found {
		b.log.Infof("bridgeDataQuerier - bridge syncer: no blocks have been processed yet, waiting to reach block: %d", block)
		return false, nil
	}

	if lastProcessedBlock >= block {
		b.log.Infof("bridgeDataQuerier - bridge syncer caught up to block: %d", block)
		return true, nil
	}

	b.log.Infof("bridgeDataQuerier - bridge syncer waiting to reach block: %d, current: %d", block, lastProcessedBlock)
	return false, nil
}

// isClaimSyncerCaughtUp checks whether the claim syncer has processed up to the given block.
// Returns true if caught up, false if not yet.
func (b *bridgeDataQuerier) isClaimSyncerCaughtUp(ctx context.Context, block uint64) (bool, error) {
	if b.claimSyncer == nil {
		return true, nil
	}
	lastProcessedBlock, found, err := b.claimSyncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return false, err
	}
	if !found {
		b.log.Infof("bridgeDataQuerier - claim syncer: no blocks have been processed yet, waiting to reach block: %d", block)
		return false, nil
	}

	if lastProcessedBlock >= block {
		b.log.Infof("bridgeDataQuerier - claim syncer caught up to block: %d", block)
		return true, nil
	}

	b.log.Infof("bridgeDataQuerier - claim syncer waiting to reach block: %d, current: %d", block, lastProcessedBlock)
	return false, nil
}

// GetUnsetClaimsForBlockRange gets unset claims from agglayer bridge L2 and converts to unclaim map
func (b *bridgeDataQuerier) GetUnsetClaimsForBlockRange(ctx context.Context,
	fromBlock, toBlock uint64) ([]claimsynctypes.Unclaim, error) {
	b.log.Debugf("getting unset claims for block range %d to %d", fromBlock, toBlock)
	return b.agglayerBridgeL2Reader.GetUnsetClaimsForBlockRange(ctx, fromBlock, toBlock)
}
