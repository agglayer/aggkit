package query

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	commontypes "github.com/agglayer/aggkit/common/types"
	configtypes "github.com/agglayer/aggkit/config/types"
)

type SetInitialBlockToClaimSyncer struct {
	certQuerier     types.CertificateQuerier
	agglayerClient  agglayer.AgglayerClientInterface
	l2OriginNetwork uint32
	logger          aggkitcommon.Logger
}

func NewSetInitialBlockToClaimSyncer(
	certQuerier types.CertificateQuerier,
	agglayerClient agglayer.AgglayerClientInterface,
	l2OriginNetwork uint32,
	logger aggkitcommon.Logger,
) *SetInitialBlockToClaimSyncer {
	return &SetInitialBlockToClaimSyncer{
		certQuerier:     certQuerier,
		agglayerClient:  agglayerClient,
		l2OriginNetwork: l2OriginNetwork,
		logger:          logger,
	}
}

func (n *SetInitialBlockToClaimSyncer) SetClaimSyncerNextRequiredBlock(
	ctx context.Context,
	l2ClaimSyncer claimsynctypes.ClaimSyncer,
	retryHandler commontypes.RetryHandler) error {
	if l2ClaimSyncer == nil {
		n.logger.Debugf("l2 claim syncer is nil, skipping setClaimSyncerNextRequiredBlock")
		return nil
	}
	claimSyncerLatestProcessedBlock, found, err := l2ClaimSyncer.GetLastProcessedBlock(ctx)
	if err != nil {
		return fmt.Errorf("error getting last processed block from claim syncer: %w", err)
	}
	if found {
		n.logger.Infof("claim syncer already has processed blocks (latest=%d), skipping setClaimSyncerNextRequiredBlock",
			claimSyncerLatestProcessedBlock)
		return nil
	}
	if retryHandler == nil {
		retryHandler = aggkitcommon.NewRetryHandler(
			[]configtypes.Duration{{Duration: time.Second}},
			aggkitcommon.MaxAttemptsInfinite,
		)
	}
	_, err = aggkitcommon.Execute(retryHandler,
		ctx,
		n.logger.Infof,
		"Setting next required block for claim syncer based on agglayer's latest settled certificate",
		func() (bool, error) {
			nextBlock, err := n.claimSyncerStartingBlock(ctx, l2ClaimSyncer)
			if err != nil {
				return true, fmt.Errorf("error getting next block number for claim syncer: %w", err)
			}
			if err := l2ClaimSyncer.SetNextRequiredBlock(ctx, nextBlock); err != nil {
				return true, fmt.Errorf("error setting next required block for claim syncer: %w", err)
			}
			n.logger.Infof("Set next required block for claim syncer to %d", nextBlock)
			return true, nil
		})
	if err != nil {
		return fmt.Errorf("error setting next required block for claim syncer: %w", err)
	}
	return nil
}

// claimSyncerStartingBlock returns the starting block number for the claim syncer.
// It queries the latest settled certificate from agglayer to determine from which block claims must be synced.
func (n *SetInitialBlockToClaimSyncer) claimSyncerStartingBlock(ctx context.Context,
	l2ClaimSyncer claimsynctypes.ClaimSyncer) (uint64, error) {
	certHeader, err := n.agglayerClient.GetLatestSettledCertificateHeader(ctx, n.l2OriginNetwork)
	if err != nil {
		return 0, fmt.Errorf("error getting latest settled certificate header from agglayer: %w", err)
	}
	// Even if certHeader is nil it returns the first block number
	toBlock, err := n.claimSyncerStartingBlockBasedOnLatestSettledCert(ctx, l2ClaimSyncer, certHeader)
	if err != nil {
		return 0, fmt.Errorf("error getting last settled certificate to block: %w", err)
	}
	return toBlock, nil
}

func (n *SetInitialBlockToClaimSyncer) claimSyncerStartingBlockBasedOnLatestSettledCert(
	ctx context.Context,
	l2ClaimSyncer claimsynctypes.ClaimSyncer,
	agglayerLastSettledCert *agglayertypes.CertificateHeader,
) (uint64, error) {
	// There is no certificate in the local database, so we need to start claim syncer because
	// the GetLastSettledCertificateToBlock to obtain toBlock need to find the latest claim
	if agglayerLastSettledCert == nil {
		n.logger.Debugf("no settled certificate in agglayer, skipping setClaimSyncerFromEmptyDB. Nothing to do")
		return 0, fmt.Errorf("no settled certificate in agglayer, cannot set claim syncer from empty DB")
	}

	blocks := n.certQuerier.GetBlockNumbersFromCertHeader(ctx, agglayerLastSettledCert)

	// If the problem is that can't find the block for latest claim, use RPC for it
	if blocks.LastImportedBridgeExitBlockErr != nil {
		globalIdx := blocks.SettledImportedBridgeExit.GlobalIndex
		blockNumber, found, err := l2ClaimSyncer.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIdx, nil)
		if err != nil {
			return 0, fmt.Errorf("error searching global index %s via RPC fallback: %w", globalIdx.String(), err)
		}
		if !found {
			return 0, fmt.Errorf("no claim found for global index %s via RPC fallback", globalIdx.String())
		}
		blocks.LastImportedBridgeExitBlock = blockNumber
		blocks.LastImportedBridgeExitBlockErr = nil
		n.logger.Infof("obtained last imported bridge exit block number %d for global index %s from RPC",
			blockNumber, globalIdx.String())
	}
	startingClaimBlockNumber, err := blocks.EarliestBlock()
	if err != nil {
		return 0, fmt.Errorf("error getting earliest block: %w", err)
	}
	return startingClaimBlockNumber, nil
}
