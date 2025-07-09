package aggoracle

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

type L1InfoTreer interface {
	GetLatestInfoUntilBlock(ctx context.Context, blockNum uint64) (*l1infotreesync.L1InfoTreeLeaf, error)
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
	blockFinality     *big.Int
}

func New(
	logger *log.Logger,
	chainSender ChainSender,
	l1Client ethereum.ChainReader,
	l1InfoTreeSyncer L1InfoTreer,
	blockFinalityType aggkittypes.BlockNumberFinality,
	waitPeriodNextGER time.Duration,
) (*AggOracle, error) {
	finality, err := blockFinalityType.ToBlockNum()
	if err != nil {
		return nil, err
	}

	return &AggOracle{
		logger:            logger,
		chainSender:       chainSender,
		l1Client:          l1Client,
		l1Info:            l1InfoTreeSyncer,
		blockFinality:     finality,
		waitPeriodNextGER: waitPeriodNextGER,
	}, nil
}

// Start starts the AggOracle process that checks for new GERs and injects them if not already injected
func (a *AggOracle) Start(ctx context.Context) {
	for {
		if blockNum, err := a.processLatestGER(ctx); err != nil {
			a.handleGERProcessingError(err, blockNum)
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
func (a *AggOracle) processLatestGER(ctx context.Context) (uint64, error) {
	// Fetch the latest GER
	blockNum, gerToInject, err := a.getLatestIndexedGER(ctx)
	if err != nil {
		return blockNum, err
	}

	isGERInjected, err := a.chainSender.IsGERInjected(gerToInject)
	if err != nil {
		return blockNum, fmt.Errorf("error checking if GER is already injected: %w", err)
	}

	if isGERInjected {
		a.logger.Debugf("GER %s is already injected", gerToInject.Hex())
		return blockNum, nil
	}

	a.logger.Infof("injecting new GER: %s", gerToInject.Hex())
	if err := a.chainSender.InjectGER(ctx, gerToInject); err != nil {
		return blockNum, fmt.Errorf("error injecting GER %s: %w", gerToInject.Hex(), err)
	}

	a.logger.Infof("GER %s is injected successfully", gerToInject.Hex())
	return blockNum, nil
}

// handleGERProcessingError handles global exit root processing error
func (a *AggOracle) handleGERProcessingError(err error, blockNumToFetch uint64) {
	switch {
	case errors.Is(err, l1infotreesync.ErrBlockNotProcessed):
		a.logger.Debugf("syncer is not ready for the block %d", blockNumToFetch)
	case errors.Is(err, l1infotreesync.ErrNotFound):
		a.logger.Debugf("syncer has not found any GER until block %d", blockNumToFetch)
	default:
		a.logger.Error("unexpected error processing GER: ", err)
	}
}

// getLatestIndexedGER tries to return the latest indexed GER from l1 info tree syncer.
// It fetches the latest block header, retrieves the latest info from the syncer,
// and returns the block number and the global exit root.
// If there is an error during the process, it returns an error.
func (a *AggOracle) getLatestIndexedGER(ctx context.Context) (uint64, common.Hash, error) {
	header, err := a.l1Client.HeaderByNumber(ctx, a.blockFinality)
	if err != nil {
		return 0, common.Hash{}, err
	}
	blockNum := header.Number.Uint64()

	info, err := a.l1Info.GetLatestInfoUntilBlock(ctx, blockNum)
	if err != nil {
		return blockNum, common.Hash{}, err
	}

	return blockNum, info.GlobalExitRoot, nil
}
