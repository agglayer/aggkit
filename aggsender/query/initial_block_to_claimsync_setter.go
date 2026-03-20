package query

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
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
	if retryHandler == nil {
		retryHandler = aggkitcommon.NewRetryHandler(
			[]configtypes.Duration{{Duration: time.Second}},
			aggkitcommon.MaxAttemptsInfinite,
		)
	}
	_, err := aggkitcommon.Execute(retryHandler,
		ctx,
		n.logger.Infof,
		"Setting next required block for claim syncer based on agglayer's latest settled certificate",
		func() (bool, error) {
			nextBlock, err := n.getNextBlockNumber(ctx)
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

// getNextBlockNumber returns the starting block number for the claim syncer.
// It queries the latest settled certificate from agglayer to determine from which block claims must be synced.
func (n *SetInitialBlockToClaimSyncer) getNextBlockNumber(ctx context.Context) (uint64, error) {
	certHeader, err := n.agglayerClient.GetLatestSettledCertificateHeader(ctx, n.l2OriginNetwork)
	if err != nil {
		return 0, fmt.Errorf("error getting latest settled certificate header from agglayer: %w", err)
	}
	// Even if certHeader is nil it returns the first block number
	toBlock, err := n.certQuerier.GetLastSettledCertificateToBlock(ctx, certHeader)
	if err != nil {
		return 0, fmt.Errorf("error getting last settled certificate to block: %w", err)
	}
	return toBlock, nil
}
