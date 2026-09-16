package query

import (
	"context"
	"fmt"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/types"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	commontypes "github.com/agglayer/aggkit/common/types"
	configtypes "github.com/agglayer/aggkit/config/types"
)

const (
	// maxIBERPCLookupFailures is the upper bound on the number of consecutive RPC log-scan errors
	// (the scan itself could not complete: toBlock resolve failure, contract binding failure, or a
	// non-shrinkable FilterLogs chunk error) tolerated before giving up on the RPC fallback and
	// using a provable lower bound instead. A scan that completes but finds no match (found=false,
	// err=nil) is not transient and falls back immediately without consuming this budget, since
	// retrying the identical scan cannot change the outcome.
	//
	// This is only an upper bound: the budget actually used for a given call to
	// SetClaimSyncerNextRequiredBlock is min(maxIBERPCLookupFailures, the number of attempts its
	// retryHandler allows) -- see ibeRPCLookupBudget. Without that adjustment, a caller whose
	// retryHandler gives up (e.g. panics) in fewer than maxIBERPCLookupFailures attempts would
	// never reach the exhaustion fallback at all: the AggSender validator constructs its
	// initial-check retry handler with a small, fixed attempt budget and panics once it is
	// exhausted, so a fixed threshold larger than that budget would make this fallback dead code
	// for the validator specifically.
	maxIBERPCLookupFailures = 5
)

// SettledIBELowerBounder derives a provable lower-bound L2 block number for a settled imported
// bridge exit (IBE) from aggsender certificate storage, when the exact block cannot be resolved
// from the local claim DB nor from an RPC log scan. It is an optional dependency of
// SetInitialBlockToClaimSyncer: a nil SettledIBELowerBounder means "skip straight to the safe
// fallback of block 0" (see WithSettledIBELowerBounder).
type SettledIBELowerBounder interface {
	// LowerBoundForSettledIBE returns a provable lower-bound L2 block number for the given settled
	// imported bridge exit (identified by its global index and bridge exit hash). The returned
	// block number MUST be less than or equal to the true block in which that IBE was imported;
	// found is false when no such lower bound could be derived, in which case lowerBound is
	// meaningless and the caller must not use it.
	LowerBoundForSettledIBE(
		ctx context.Context,
		settledIBE *agglayertypes.SettledImportedBridgeExit,
	) (lowerBound uint64, found bool, err error)
}

type SetInitialBlockToClaimSyncer struct {
	certQuerier            types.CertificateQuerier
	agglayerClient         agglayer.AgglayerClientInterface
	l2OriginNetwork        uint32
	logger                 aggkitcommon.Logger
	settledIBELowerBounder SettledIBELowerBounder
	// ibeRPCLookupFailures counts consecutive RPC log-scan errors (found=false, err!=nil) for the
	// settled IBE fallback. It is reset to 0 on a successful RPC lookup and whenever a fallback is
	// taken (either because the scan completed with no match, or because this counter hit
	// ibeRPCLookupBudget).
	ibeRPCLookupFailures int
	// ibeRPCLookupBudget is min(maxIBERPCLookupFailures, the caller's retryHandler attempts),
	// computed once per call by SetClaimSyncerNextRequiredBlock (see computeIBERPCLookupBudget).
	// Zero means "not yet computed", in which case effectiveIBERPCLookupBudget defaults to
	// maxIBERPCLookupFailures.
	ibeRPCLookupBudget int
}

// SetInitialBlockToClaimSyncerOption configures optional behavior of SetInitialBlockToClaimSyncer.
type SetInitialBlockToClaimSyncerOption func(*SetInitialBlockToClaimSyncer)

// WithSettledIBELowerBounder configures an optional provable lower-bound resolver for the settled
// imported bridge exit (IBE) block, used as a last-resort fallback when the IBE claim's block
// cannot be found in the local claim DB nor via the RPC log-scan fallback. When this option is not
// used, settledIBELowerBounder is nil and the setter falls back straight to block 0, which
// SetNextRequiredBlock then caps to the claim syncer's InitialBlockNum.
func WithSettledIBELowerBounder(bounder SettledIBELowerBounder) SetInitialBlockToClaimSyncerOption {
	return func(s *SetInitialBlockToClaimSyncer) {
		s.settledIBELowerBounder = bounder
	}
}

func NewSetInitialBlockToClaimSyncer(
	certQuerier types.CertificateQuerier,
	agglayerClient agglayer.AgglayerClientInterface,
	l2OriginNetwork uint32,
	logger aggkitcommon.Logger,
	opts ...SetInitialBlockToClaimSyncerOption,
) *SetInitialBlockToClaimSyncer {
	s := &SetInitialBlockToClaimSyncer{
		certQuerier:     certQuerier,
		agglayerClient:  agglayerClient,
		l2OriginNetwork: l2OriginNetwork,
		logger:          logger,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
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
	n.ibeRPCLookupBudget = computeIBERPCLookupBudget(retryHandler)
	_, err = aggkitcommon.Execute(retryHandler,
		ctx,
		n.logger.Infof,
		"Setting next required block for claim syncer based on agglayer's latest settled certificate",
		func() (bool, error) {
			nextBlock, blocksStr, err := n.claimSyncerStartingBlock(ctx, l2ClaimSyncer)
			if err != nil {
				return true, fmt.Errorf("error getting next block number for claim syncer: %w", err)
			}
			if err := l2ClaimSyncer.SetNextRequiredBlock(ctx, nextBlock); err != nil {
				return true, fmt.Errorf("error setting next required block for claim syncer: %w", err)
			}
			n.logger.Infof("Set next required block for claim syncer to %d. settled blocks: %s", nextBlock, blocksStr)
			return true, nil
		})
	if err != nil {
		return fmt.Errorf("error setting next required block for claim syncer: %w", err)
	}
	return nil
}

// computeIBERPCLookupBudget returns min(maxIBERPCLookupFailures, the number of attempts
// retryHandler allows), by probing MustExecuteAttempt for attempt indices
// 0..maxIBERPCLookupFailures-1. Each invocation of claimSyncerStartingBlockBasedOnLatestSettledCert
// (and therefore each potential RPC log-scan failure) corresponds 1:1 to one such attempt within
// the same outer retry loop (aggkitcommon.Execute, driven by this same retryHandler), so this is
// exactly the number of consecutive RPC failures the caller can actually absorb before its own
// retry budget -- not just this fallback's counter -- runs out. This is what makes the exhaustion
// fallback (see fallbackSettledIBEBlock) reachable for a caller with a small, fixed attempt budget
// (e.g. the AggSender validator, which panics once its own budget is exhausted) instead of dead
// code that a fixed maxIBERPCLookupFailures threshold would never reach in time. A retryHandler
// with an infinite or larger budget is simply capped at maxIBERPCLookupFailures, which was already
// the intended ceiling, so this has no effect on such callers (e.g. the AggSender proposer).
func computeIBERPCLookupBudget(retryHandler commontypes.RetryHandler) int {
	budget := 0
	for attempt := 0; attempt < maxIBERPCLookupFailures; attempt++ {
		if !retryHandler.MustExecuteAttempt(attempt) {
			break
		}
		budget++
	}
	if budget == 0 {
		// Always allow at least one RPC attempt before falling back, even for a pathological
		// retryHandler that would not execute attempt 0.
		budget = 1
	}
	return budget
}

// effectiveIBERPCLookupBudget returns the consecutive-RPC-failure budget to use right now:
// n.ibeRPCLookupBudget when SetClaimSyncerNextRequiredBlock has computed one (the normal path),
// or maxIBERPCLookupFailures when it has not (e.g. claimSyncerStartingBlockBasedOnLatestSettledCert
// exercised directly, as some white-box tests do).
func (n *SetInitialBlockToClaimSyncer) effectiveIBERPCLookupBudget() int {
	if n.ibeRPCLookupBudget <= 0 {
		return maxIBERPCLookupFailures
	}
	return n.ibeRPCLookupBudget
}

// claimSyncerStartingBlock returns the starting block number for the claim syncer, along with a
// human-readable rendering of the settled sources it was derived from (SettledBlocks.String()),
// so callers can log both together without re-deriving the sources.
// It queries the latest settled certificate from agglayer to determine from which block claims must be synced.
// If certHeader is nil (no settled certificate yet), GetSettledBlocksFromCertHeader handles it
// by skipping the per-cert queries and returning only the FEP start block.
func (n *SetInitialBlockToClaimSyncer) claimSyncerStartingBlock(ctx context.Context,
	l2ClaimSyncer claimsynctypes.ClaimSyncer) (uint64, string, error) {
	certHeader, err := n.agglayerClient.GetLatestSettledCertificateHeader(ctx, n.l2OriginNetwork)
	if err != nil {
		return 0, "", fmt.Errorf("error getting latest settled certificate header from agglayer: %w", err)
	}
	toBlock, blocksStr, err := n.claimSyncerStartingBlockBasedOnLatestSettledCert(ctx, l2ClaimSyncer, certHeader)
	if err != nil {
		return 0, "", fmt.Errorf("error getting last settled certificate to block: %w", err)
	}
	return toBlock, blocksStr, nil
}

// claimSyncerStartingBlockBasedOnLatestSettledCert returns the starting block number for the
// claim syncer along with SettledBlocks.String() of the (possibly RPC-fallback-adjusted) sources
// it was computed from, so the caller can log the three settled sources on every path, not only
// on the fallback path.
func (n *SetInitialBlockToClaimSyncer) claimSyncerStartingBlockBasedOnLatestSettledCert(
	ctx context.Context,
	l2ClaimSyncer claimsynctypes.ClaimSyncer,
	agglayerLastSettledCert *agglayertypes.CertificateHeader,
) (uint64, string, error) {
	blocks := n.certQuerier.GetSettledBlocksFromCertHeader(ctx, agglayerLastSettledCert)

	// If the problem is that can't find the block for latest claim, use RPC as fallback. This is
	// only attempted when the other two sources are healthy: if LastBridgeExitBlockErr or
	// LastSettledL2BlockNumErr is set, EarliestBlock() will fail regardless of what the IBE
	// fallback resolves (see EarliestBlock's error precedence), so there is no point paying for a
	// chunked RPC scan (and emitting its WARN/metric) on every retry attempt while those sources
	// keep retrying on their own. Gating here makes the fallback fire effectively once per
	// resolution instead of once per attempt.
	if blocks.LastImportedBridgeExitBlockErr != nil && blocks.SettledImportedBridgeExit != nil &&
		blocks.LastBridgeExitBlockErr == nil && blocks.LastSettledL2BlockNumErr == nil {
		globalIdx := blocks.SettledImportedBridgeExit.GlobalIndex
		blockNumber, found, err := l2ClaimSyncer.GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIdx, nil)
		switch {
		case err != nil:
			// The scan itself could not complete: this is transient (e.g. RPC hiccup), so count
			// consecutive failures and keep letting the caller's retry loop retry until the limit
			// is hit, at which point we give up and fall back.
			n.ibeRPCLookupFailures++
			if n.ibeRPCLookupFailures < n.effectiveIBERPCLookupBudget() {
				return 0, "", fmt.Errorf("error searching global index %s via RPC fallback: %w", globalIdx.String(), err)
			}
			n.fallbackSettledIBEBlock(ctx, &blocks, metrics.ReasonIBERPCLookupExhausted,
				fmt.Sprintf("RPC lookup for global index %s failed %d consecutive times, last error: %v",
					globalIdx.String(), n.ibeRPCLookupFailures, err))
			n.ibeRPCLookupFailures = 0
		case !found:
			// The scan completed over its whole range and found no match. Retrying the identical
			// scan cannot help, so fall back immediately instead of consuming retry attempts.
			n.ibeRPCLookupFailures = 0
			n.fallbackSettledIBEBlock(ctx, &blocks, metrics.ReasonIBENotFoundOnRPC,
				fmt.Sprintf("no claim found for global index %s via RPC fallback", globalIdx.String()))
		default:
			n.ibeRPCLookupFailures = 0
			blocks.LastImportedBridgeExitBlock = blockNumber
			blocks.LastImportedBridgeExitBlockErr = nil
			n.logger.Infof("obtained last imported bridge exit block number %d for global index %s from RPC",
				blockNumber, globalIdx.String())
		}
	}
	startingClaimBlockNumber, err := blocks.EarliestBlock()
	if err != nil {
		return 0, "", fmt.Errorf("error getting earliest block: %w", err)
	}
	return startingClaimBlockNumber, blocks.String(), nil
}

// fallbackSettledIBEBlock resolves a safe lower-bound block number for a settled imported bridge
// exit (IBE) whose block could not be resolved via the local claim DB or the RPC log-scan
// fallback, and stores it into blocks.LastImportedBridgeExitBlock (clearing the error).
//
// It tries the optional settledIBELowerBounder (see WithSettledIBELowerBounder) first; if that
// dependency is nil, errors, or reports no result, block 0 is used instead. Block 0 -- like any
// value a correct SettledIBELowerBounder would return -- is guaranteed to be <= the true IBE
// block, which is the safety invariant this fallback must uphold: choosing a lower bound only
// ever makes the claim syncer start earlier (re-processing already-claimed events, which is
// harmless) and never later than the real settled claim.
//
// When the chosen lower bound is 0, SettledBlocks.EarliestBlock() still includes it in the
// minimum -- the imported-bridge-exit term is included whenever SettledImportedBridgeExit != nil,
// regardless of its value -- so the result is 0 even if the other two sources hold higher,
// non-zero values. ClaimSync.SetNextRequiredBlock then caps that up to the claim syncer's
// configured InitialBlockNum. That behavior is unchanged and intended: it is exactly as safe as
// any other starting point at or below the real IBE block.
func (n *SetInitialBlockToClaimSyncer) fallbackSettledIBEBlock(
	ctx context.Context,
	blocks *types.SettledBlocks,
	metricReason string,
	reasonMsg string,
) {
	var lowerBound uint64
	lowerBoundSource := "no provable lower bound available, using 0 (capped to claim syncer's InitialBlockNum)"
	if n.settledIBELowerBounder != nil {
		bound, found, err := n.settledIBELowerBounder.LowerBoundForSettledIBE(ctx, blocks.SettledImportedBridgeExit)
		switch {
		case err != nil:
			n.logger.Warnf("error getting provable lower bound for settled IBE, falling back to 0: %v", err)
		case found:
			lowerBound = bound
			lowerBoundSource = fmt.Sprintf("local-cert FromBlock=%d", bound)
		}
	}
	blocks.LastImportedBridgeExitBlock = lowerBound
	blocks.LastImportedBridgeExitBlockErr = nil
	n.logger.Warnf("falling back claim syncer start block for settled imported bridge exit: %s. "+
		"Using block %d (%s) as the lower bound. settled blocks: %s",
		reasonMsg, lowerBound, lowerBoundSource, blocks.String())
	metrics.ClaimSyncerStartBlockFallback(metricReason)
}
