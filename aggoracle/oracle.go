package aggoracle

import (
	"context"
	"errors"
	"time"

	"github.com/agglayer/aggkit/aggoracle/metrics"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

// L1InfoTreeSyncer is an interface that defines the methods required to interact with the L1 info tree syncer
type L1InfoTreeSyncer interface {
	GetLatestL1InfoGER(ctx context.Context) (common.Hash, error)
}

// ChainSender is an interface that defines the methods required to send Global Exit Roots (GERs) to the chain.
// How a GER ends up on the chain depends on the mode (direct injection, or signatures collected from the GER
// validators for the AggOracleCommittee v2), so each mode is a different implementation, and the AggOracle only
// needs ProcessGER.
type ChainSender interface {
	// IsGERInjected returns true if the GER is already registered on the chain
	IsGERInjected(ger common.Hash) (bool, error)
	// ProcessGER does what is needed to get the GER registered on the chain
	ProcessGER(ctx context.Context, ger common.Hash) error
}

type AggOracle struct {
	logger            *log.Logger
	waitPeriodNextGER time.Duration
	l1Client          types.EthChainReader
	l1Info            L1InfoTreeSyncer
	chainSender       ChainSender
}

// New creates a new AggOracle instance that will monitor the L1 info tree for new Global Exit Roots (GERs)
func New(
	logger *log.Logger,
	chainSender ChainSender,
	l1Client types.EthChainReader,
	l1InfoTreeSyncer L1InfoTreeSyncer,
	waitPeriodNextGER time.Duration,
) (*AggOracle, error) {
	return &AggOracle{
		logger:            logger,
		chainSender:       chainSender,
		l1Client:          l1Client,
		l1Info:            l1InfoTreeSyncer,
		waitPeriodNextGER: waitPeriodNextGER,
	}, nil
}

// Start starts the AggOracle process that checks for new GERs and injects them if not already injected
func (a *AggOracle) Start(ctx context.Context) {
	// Register metrics
	metrics.Register()

	for {
		if err := a.processLatestGER(ctx); err != nil {
			a.handleGERProcessingError(err)
		}

		select {
		case <-time.After(a.waitPeriodNextGER):
			continue

		case <-ctx.Done():
			return
		}
	}
}

// processLatestGER fetches the latest finalized GER, checks if it is already injected and injects it if not
func (a *AggOracle) processLatestGER(ctx context.Context) error {
	a.logger.Debugf("checking for new GERs...")
	metrics.IncGERProcessCount()
	// Fetch the latest GER
	latestGER, err := a.l1Info.GetLatestL1InfoGER(ctx)
	if err != nil {
		metrics.IncGERProcessErrCount()
		return err
	}

	a.logger.Debugf("latest GER retrieved: %s", latestGER.String())

	go func() {
		start := time.Now()
		err := a.chainSender.ProcessGER(ctx, latestGER)
		metrics.ObserveGERProcessDuration(time.Since(start))
		if err != nil {
			metrics.IncGERProcessErrCount()
			a.logger.Error(err)
		}
	}()

	return nil
}

// handleGERProcessingError handles global exit root processing error
func (a *AggOracle) handleGERProcessingError(err error) {
	switch {
	case errors.Is(err, l1infotreesync.ErrNotFound):
		a.logger.Debugf("syncer has not indexed any GERs")
	default:
		a.logger.Error("unexpected error processing GER: ", err)
	}
}
