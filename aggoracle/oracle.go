package aggoracle

import (
	"context"
	"errors"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggoracle/metrics"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

// L1InfoTreeSyncer is an interface that defines the methods required to interact with the L1 info tree syncer
type L1InfoTreeSyncer interface {
	GetLatestL1InfoGER(ctx context.Context) (common.Hash, error)
}

// ChainSender is an interface that defines the methods required to send Global Exit Roots (GERs) to the chain
type ChainSender interface {
	IsGERInjected(ger common.Hash) (bool, error)
	InjectGER(ctx context.Context, ger common.Hash) error
	ProposeGER(ctx context.Context, ger common.Hash) error
	IsGERProposed(ger common.Hash) (bool, error)
	ProcessGER(ctx context.Context, ger common.Hash) error
	InjectGERWithSignatures(ctx context.Context, ger common.Hash, signatures [][]byte) error
}

// GERValidatorPoller is an interface for polling validators to validate GERs
type GERValidatorPoller interface {
	PollValidators(ctx context.Context, ger common.Hash) (*agglayertypes.Multisig, error)
}

type AggOracle struct {
	logger                *log.Logger
	waitPeriodNextGER     time.Duration
	l1Client              ethereum.ChainReader
	l1Info                L1InfoTreeSyncer
	chainSender           ChainSender
	validatorPoller       GERValidatorPoller
	enableValidatorSigned bool
}

// New creates a new AggOracle instance that will monitor the L1 info tree for new Global Exit Roots (GERs)
func New(
	logger *log.Logger,
	chainSender ChainSender,
	l1Client ethereum.ChainReader,
	l1InfoTreeSyncer L1InfoTreeSyncer,
	waitPeriodNextGER time.Duration,
	validatorPoller GERValidatorPoller,
	enableValidatorSigned bool,
) (*AggOracle, error) {
	return &AggOracle{
		logger:                logger,
		chainSender:           chainSender,
		l1Client:              l1Client,
		l1Info:                l1InfoTreeSyncer,
		waitPeriodNextGER:     waitPeriodNextGER,
		validatorPoller:       validatorPoller,
		enableValidatorSigned: enableValidatorSigned,
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

	// Check if GER is already injected
	isInjected, err := a.chainSender.IsGERInjected(latestGER)
	if err != nil {
		metrics.IncGERProcessErrCount()
		return err
	}

	if isInjected {
		a.logger.Debugf("GER (%s) is already injected", latestGER.Hex())
		return nil
	}

	// Handle ValidatorSigned mode
	if a.enableValidatorSigned {
		if a.validatorPoller == nil {
			return fmt.Errorf("validatorPoller is required for ValidatorSigned mode")
		}
		return a.processGERWithValidatorSigned(ctx, latestGER)
	}

	// Default mode: process GER normally
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

// processGERWithValidatorSigned processes GER in ValidatorSigned mode
func (a *AggOracle) processGERWithValidatorSigned(ctx context.Context, ger common.Hash) error {
	a.logger.Infof("processing GER in ValidatorSigned mode: %s", ger.Hex())

	// Call validator service to get signatures
	multisig, err := a.validatorPoller.PollValidators(ctx, ger)
	if err != nil {
		metrics.IncGERProcessErrCount()
		return fmt.Errorf("failed to poll validators for GER: %w", err)
	}

	// Extract signatures from multisig
	signatures := make([][]byte, 0, len(multisig.Signatures))
	for _, sigEntry := range multisig.Signatures {
		signatures = append(signatures, sigEntry.Signature)
	}

	a.logger.Infof("collected %d signatures for GER: %s", len(signatures), ger.Hex())

	// Call L2 contract with signatures and GER
	go func() {
		start := time.Now()
		err := a.chainSender.InjectGERWithSignatures(ctx, ger, signatures)
		metrics.ObserveGERProcessDuration(time.Since(start))
		if err != nil {
			metrics.IncGERProcessErrCount()
			a.logger.Error(err)
		} else {
			a.logger.Infof("successfully injected GER with signatures: %s", ger.Hex())
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
