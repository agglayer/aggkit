package aggoracle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

type L1InfoTreer interface {
	GetLatestL1InfoLeaf(ctx context.Context) (*l1infotreesync.L1InfoTreeLeaf, error)
}

type ChainSender interface {
	IsGERInjected(ger common.Hash) (bool, error)
	InjectGER(ctx context.Context, ger common.Hash) error
}

type AggOracle struct {
	logger            *log.Logger
	waitPeriodNextGER time.Duration
	l1Client          ethereum.ChainReader
	l1Info            L1InfoTreer
	chainSender       ChainSender
}

// New creates a new AggOracle instance that will monitor the L1 info tree for new Global Exit Roots (GERs)
func New(
	logger *log.Logger,
	chainSender ChainSender,
	l1Client ethereum.ChainReader,
	l1InfoTreeSyncer L1InfoTreer,
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
	// Fetch the latest GER
	gerToInject, err := a.getLatestIndexedGER(ctx)
	if err != nil {
		return err
	}

	isGERInjected, err := a.chainSender.IsGERInjected(gerToInject)
	if err != nil {
		return fmt.Errorf("error checking if GER is already injected: %w", err)
	}

	if isGERInjected {
		a.logger.Debugf("GER %s is already injected", gerToInject.Hex())
		return nil
	}

	a.logger.Infof("injecting new GER: %s", gerToInject.Hex())
	if err := a.chainSender.InjectGER(ctx, gerToInject); err != nil {
		return fmt.Errorf("error injecting GER %s: %w", gerToInject.Hex(), err)
	}

	a.logger.Infof("GER %s is injected successfully", gerToInject.Hex())
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

// getLatestIndexedGER tries to return the latest indexed GER from l1 info tree syncer.
func (a *AggOracle) getLatestIndexedGER(ctx context.Context) (common.Hash, error) {
	info, err := a.l1Info.GetLatestL1InfoLeaf(ctx)
	if err != nil {
		return common.Hash{}, err
	}

	return info.GlobalExitRoot, nil
}
