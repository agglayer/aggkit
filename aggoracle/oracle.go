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
	// GetLatestL1InfoLeaf exposes the full leaf (incl. mainnet+rollup exit roots)
	// so chain senders in two-root mode can forward both components instead of
	// only the combined hash. Needed to close the decomposition race on
	// sovereign chains that can't reverse keccak(mainnet||rollup).
	GetLatestL1InfoLeaf(ctx context.Context) (*l1infotreesync.L1InfoTreeLeaf, error)
}

// ChainSender sends Global Exit Roots (GERs) to the chain.
//
// The two-root variants (`ProcessGERWithRoots`, `InjectExitRoots`) carry the
// uncombined (mainnet, rollup) exit root pair. They exist because sovereign
// chains that receive only `insertGlobalExitRoot(combinedHash)` cannot recover
// the individual roots without racing live L1 state — the aggoracle already
// has the pair in the L1InfoTreeLeaf, so it's cleaner to forward them
// unchanged than to force the downstream service to decompose the one-way hash.
type ChainSender interface {
	IsGERInjected(ger common.Hash) (bool, error)
	InjectGER(ctx context.Context, ger common.Hash) error
	ProposeGER(ctx context.Context, ger common.Hash) error
	IsGERProposed(ger common.Hash) (bool, error)
	ProcessGER(ctx context.Context, ger common.Hash) error
	// UsesTwoRootMode reports whether this sender prefers the two-root path.
	// When true, AggOracle fetches the full leaf and calls ProcessGERWithRoots.
	UsesTwoRootMode() bool
	// ProcessGERWithRoots processes a GER alongside its uncombined exit root
	// pair. Only meaningful when UsesTwoRootMode returns true.
	ProcessGERWithRoots(ctx context.Context, ger, mainnetExitRoot, rollupExitRoot common.Hash) error
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

// processLatestGER fetches the latest finalized GER, checks if it is already
// injected and injects it if not. When the chain sender is in two-root mode
// we fetch the full leaf so the individual (mainnet, rollup) exit roots are
// forwarded alongside the combined GER — eliminating the decomposition race
// on sovereign chains that would otherwise read stale L1 state.
func (a *AggOracle) processLatestGER(ctx context.Context) error {
	a.logger.Debugf("checking for new GERs...")
	metrics.IncGERProcessCount()

	if a.chainSender.UsesTwoRootMode() {
		leaf, err := a.l1Info.GetLatestL1InfoLeaf(ctx)
		if err != nil {
			metrics.IncGERProcessErrCount()
			return err
		}
		a.logger.Debugf("latest L1 info leaf retrieved: ger=%s mainnet=%s rollup=%s",
			leaf.GlobalExitRoot, leaf.MainnetExitRoot, leaf.RollupExitRoot)
		go func() {
			start := time.Now()
			err := a.chainSender.ProcessGERWithRoots(ctx,
				leaf.GlobalExitRoot, leaf.MainnetExitRoot, leaf.RollupExitRoot)
			metrics.ObserveGERProcessDuration(time.Since(start))
			if err != nil {
				metrics.IncGERProcessErrCount()
				a.logger.Error(err)
			}
		}()
		return nil
	}

	// Single-hash path: legacy insertGlobalExitRoot / proposeGlobalExitRoot.
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
