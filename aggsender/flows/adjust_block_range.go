package flows

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrMaxL2BlockNumberExceededInARetryCert = errors.New("maxL2BlockNumberLimiter. " +
		"Max L2 block number exceeded in a retry certificate")
	ErrMaxL2BlockRangeExceededInARetryCert = errors.New("maxL2BlockRangeLimiter. " +
		"Max L2 block range exceeded in a retry certificate")
	ErrComplete = errors.New("maxL2BlockNumberLimiter. " +
		"All certs send, no more certificates can be sent")
	ErrBuildParamsIsNil = errors.New("maxL2BlockNumberLimiter. BuildParams is nil")
)

type gerValidationCache struct {
	existsOnL1 map[common.Hash]bool
}

func newGERValidationCache() *gerValidationCache {
	return &gerValidationCache{
		existsOnL1: make(map[common.Hash]bool),
	}
}

func (f *baseFlow) AdjustBlockRange(
	ctx context.Context,
	buildParams *types.CertificateBuildParams,
	options types.BlockRangeAdjustmentOptions,
) (*types.CertificateBuildParams, error) {
	if buildParams == nil {
		return nil, ErrBuildParamsIsNil
	}

	current := buildParams
	cache := newGERValidationCache()

	current, err := f.adjustMaxL2BlockNumber(current, options)
	if err != nil {
		return nil, err
	}

	current, err = f.adjustMaxL2BlockRange(current, options)
	if err != nil {
		return nil, err
	}

	if options.ValidateRootToProve {
		if err := f.validateRootToProveIsFinalized(ctx, current); err != nil {
			return nil, err
		}
	}

	current, err = f.adjustClaimsNotProvableAgainstRoot(ctx, current, cache)
	if err != nil {
		return nil, err
	}

	if !options.DisableSizeLimit {
		current, err = f.limitCertSize(current)
		if err != nil {
			return nil, err
		}
	}

	current, err = f.adjustInvalidClaimsAreNotUnclaimed(current, cache)
	if err != nil {
		return nil, err
	}

	return current, nil
}

func (f *baseFlow) adjustMaxL2BlockNumber(
	buildParams *types.CertificateBuildParams,
	options types.BlockRangeAdjustmentOptions,
) (*types.CertificateBuildParams, error) {
	if options.MaxL2BlockNumber == 0 || buildParams.ToBlock <= options.MaxL2BlockNumber {
		return buildParams, nil
	}

	f.log.Infof("adjustBlockRange. Applying maxL2BlockNumber=%d to cert range [%d,%d]",
		options.MaxL2BlockNumber, buildParams.FromBlock, buildParams.ToBlock)

	if buildParams.IsARetry() && !options.AllowResizeRetryCert {
		return nil, fmt.Errorf("adjustBlockRange can't adapt the retry certificate, "+
			"the ToBlock %d is greater than the maxL2BlockNumber %d. Err: %w",
			buildParams.ToBlock, options.MaxL2BlockNumber, ErrMaxL2BlockNumberExceededInARetryCert)
	}

	if isUpcomingNextRange(options.MaxL2BlockNumber, buildParams.FromBlock, buildParams.ToBlock) {
		return nil, fmt.Errorf("adjustBlockRange finish. The next certificate is just the upcoming next range "+
			"after the last sent certificate. FromBlock: %d, ToBlock: %d, maxL2BlockNumber: %d. Err: %w",
			buildParams.FromBlock, buildParams.ToBlock, options.MaxL2BlockNumber, ErrComplete)
	}

	if buildParams.FromBlock > options.MaxL2BlockNumber {
		f.log.Warnf("adjustBlockRange. Next cert is not the upcoming next range, but is far from it. "+
			"maxL2BlockNumber: %d, FromBlock: %d", options.MaxL2BlockNumber, buildParams.FromBlock)
		return nil, fmt.Errorf("adjustBlockRange. Cert has exceeded the maximum block. "+
			"maxL2BlockNumber: %d, but the current buildParams has FromBlock: %d. Err: %w",
			options.MaxL2BlockNumber, buildParams.FromBlock, ErrComplete)
	}

	return f.adjustToMaxL2Block(buildParams, options, options.MaxL2BlockNumber, "maxL2BlockNumber",
		options.MaxL2BlockNumber, true)
}

func (f *baseFlow) adjustMaxL2BlockRange(
	buildParams *types.CertificateBuildParams,
	options types.BlockRangeAdjustmentOptions,
) (*types.CertificateBuildParams, error) {
	if options.MaxL2BlockRange == 0 || buildParams.ToBlock <= buildParams.FromBlock ||
		buildParams.ToBlock-buildParams.FromBlock <= options.MaxL2BlockRange {
		return buildParams, nil
	}

	newToBlock := buildParams.FromBlock + options.MaxL2BlockRange
	f.log.Infof("adjustBlockRange. Applying maxL2BlockRange=%d to cert range [%d,%d]",
		options.MaxL2BlockRange, buildParams.FromBlock, buildParams.ToBlock)

	if buildParams.IsARetry() && !options.AllowResizeRetryCert {
		return nil, fmt.Errorf("adjustBlockRange can't adapt the retry certificate, "+
			"the block range %d is greater than the maxL2BlockRange %d. Err: %w",
			buildParams.ToBlock-buildParams.FromBlock, options.MaxL2BlockRange,
			ErrMaxL2BlockRangeExceededInARetryCert)
	}

	return f.adjustToMaxL2Block(buildParams, options, newToBlock, "maxL2BlockRange", options.MaxL2BlockRange, false)
}

func (f *baseFlow) adjustToMaxL2Block(
	buildParams *types.CertificateBuildParams,
	options types.BlockRangeAdjustmentOptions,
	maxL2BlockNumber uint64,
	limitName string,
	limitValue uint64,
	completeOnEmptyRequiredBridge bool,
) (*types.CertificateBuildParams, error) {
	adjusted, err := cloneCertificateBuildParamsWithRange(buildParams, buildParams.FromBlock, maxL2BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("adjustBlockRange error adjusting the ToBlock of the certificate %d -> %d: %w",
			buildParams.ToBlock, maxL2BlockNumber, err)
	}

	if !options.RequireOneBridgeInCertificate && adjusted.IsEmpty() {
		return adjusted, nil
	}

	if options.RequireOneBridgeInCertificate && adjusted.NumberOfBridges() == 0 {
		if adjusted.NumberOfClaims() > 0 {
			return nil, fmt.Errorf("adjustBlockRange can't send cert. %s: %d. "+
				"the current reduced range [%d to %d] has no bridges but has %d imported bridges",
				limitName, limitValue, adjusted.FromBlock, adjusted.ToBlock, adjusted.NumberOfClaims())
		}

		if completeOnEmptyRequiredBridge {
			f.log.Warnf("Nothing to do. We have submitted all permitted certificate for %s: %d",
				limitName, limitValue)
			return nil, ErrComplete
		}

		return nil, fmt.Errorf("adjustBlockRange can't send cert. %s: %d. "+
			"the current reduced range [%d to %d] has no bridges",
			limitName, limitValue, adjusted.FromBlock, adjusted.ToBlock)
	}

	return adjusted, nil
}

func (f *baseFlow) validateRootToProveIsFinalized(
	ctx context.Context,
	buildParams *types.CertificateBuildParams,
) error {
	if buildParams.L1InfoTreeLeafCount == 0 {
		return fmt.Errorf("L1InfoTreeLeafCount must be greater than 0")
	}

	rootForLeafCount, err := f.l1InfoTreeDataQuerier.GetL1InfoRootByLeafIndex(ctx, buildParams.L1InfoTreeLeafCount-1)
	if err != nil {
		return fmt.Errorf("error getting L1 info tree root by leaf count %d: %w",
			buildParams.L1InfoTreeLeafCount, err)
	}

	if rootForLeafCount.Hash != buildParams.L1InfoTreeRootFromWhichToProve {
		return fmt.Errorf("L1InfoTreeRootFromWhichToProve %s does not match root %s for leaf count %d",
			buildParams.L1InfoTreeRootFromWhichToProve.Hex(), rootForLeafCount.Hash.Hex(), buildParams.L1InfoTreeLeafCount)
	}

	latestFinalizedRoot, _, err := f.l1InfoTreeDataQuerier.GetTargetL1InfoRoot(ctx)
	if err != nil {
		return fmt.Errorf("error getting latest finalized L1 info root: %w", err)
	}

	if rootForLeafCount.Index > latestFinalizedRoot.Index {
		return fmt.Errorf("L1 info tree root %s at leaf count %d is not finalized yet. latest finalized index: %d",
			buildParams.L1InfoTreeRootFromWhichToProve.Hex(), buildParams.L1InfoTreeLeafCount, latestFinalizedRoot.Index+1)
	}

	return nil
}

func (f *baseFlow) adjustClaimsNotProvableAgainstRoot(
	ctx context.Context,
	buildParams *types.CertificateBuildParams,
	cache *gerValidationCache,
) (*types.CertificateBuildParams, error) {
	for _, claim := range buildParams.Claims {
		_, _, err := f.l1InfoTreeDataQuerier.GetProofForGER(
			ctx, claim.GlobalExitRoot, buildParams.L1InfoTreeRootFromWhichToProve,
		)
		if err == nil {
			cache.existsOnL1[claim.GlobalExitRoot] = true
			continue
		}

		existsOnL1, existsErr := f.getGERExistsOnL1(cache, claim.GlobalExitRoot)
		if existsErr != nil {
			return nil, fmt.Errorf("error checking if GER %s exists on L1: %w", claim.GlobalExitRoot.String(), existsErr)
		}

		if !existsOnL1 {
			f.log.Warnf("GER %s used on claim %+v does not exist on L1", claim.GlobalExitRoot.Hex(), claim)
			continue
		}

		if errors.Is(err, query.ErrGERNotProvableAgainstRoot) {
			return nil, fmt.Errorf("GER %s exists on L1 but cannot be proved against selected root %s: %w",
				claim.GlobalExitRoot.Hex(), buildParams.L1InfoTreeRootFromWhichToProve.Hex(), err)
		}

		return nil, fmt.Errorf("proof lookup failed for GER %s against root %s: %w",
			claim.GlobalExitRoot.Hex(), buildParams.L1InfoTreeRootFromWhichToProve.Hex(), err)
	}

	return buildParams, nil
}

func (f *baseFlow) limitCertSize(buildParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	currentCert := buildParams
	maxCertSize := f.cfg.MaxCertSize

	for {
		if maxCertSize == 0 || currentCert.EstimatedSize() <= maxCertSize {
			return currentCert, nil
		}

		if currentCert.NumberOfBlocks() <= 1 {
			f.log.Warnf("Minimum number of blocks reached [%d to %d]. Estimated size: %d > max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), maxCertSize)
			return currentCert, nil
		}

		nextCert, err := cloneCertificateBuildParamsWithRange(
			currentCert, currentCert.FromBlock, currentCert.ToBlock-1,
		)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}

		currentCert = nextCert
	}
}

func (f *baseFlow) adjustInvalidClaimsAreNotUnclaimed(
	buildParams *types.CertificateBuildParams,
	cache *gerValidationCache,
) (*types.CertificateBuildParams, error) {
	current := buildParams

	for {
		invalidClaim, found, err := f.findFirstMissingGERClaimWithoutFinalUnclaim(current, cache)
		if err != nil {
			return nil, err
		}
		if !found {
			return current, nil
		}

		if invalidClaim.BlockNum <= current.FromBlock {
			return nil, fmt.Errorf("cannot create certificate: invalid claim at block %d "+
				"(start block %d) has no matching posterior unclaim in the final block range",
				invalidClaim.BlockNum, current.FromBlock)
		}

		f.log.Warnf("found a claim (%+v) that uses a GER (%s) that doesn't exist on L1 and "+
			"has no matching posterior unclaim for the final block range (from %d to %d). Trimming down block range to block %d",
			invalidClaim, invalidClaim.GlobalExitRoot.String(), current.FromBlock, current.ToBlock, invalidClaim.BlockNum-1)

		current, err = trimCertificateToBlock(current, invalidClaim.BlockNum-1)
		if err != nil {
			return nil, err
		}
	}
}

func (f *baseFlow) getGERExistsOnL1(cache *gerValidationCache, ger common.Hash) (bool, error) {
	if exists, ok := cache.existsOnL1[ger]; ok {
		return exists, nil
	}

	exists, err := f.l1InfoTreeDataQuerier.DoesGERExistsOnL1(ger)
	if err != nil {
		return false, err
	}

	cache.existsOnL1[ger] = exists
	return exists, nil
}

func (f *baseFlow) findFirstMissingGERClaimWithoutFinalUnclaim(
	buildParams *types.CertificateBuildParams,
	cache *gerValidationCache,
) (claimsynctypes.Claim, bool, error) {
	missingClaims := make([]claimsynctypes.Claim, 0, len(buildParams.Claims))
	for _, claim := range buildParams.Claims {
		existsOnL1, err := f.getGERExistsOnL1(cache, claim.GlobalExitRoot)
		if err != nil {
			return claimsynctypes.Claim{}, false,
				fmt.Errorf("error checking if GER %s exists on L1: %w", claim.GlobalExitRoot.String(), err)
		}
		if !existsOnL1 {
			missingClaims = append(missingClaims, claim)
		}
	}

	if len(missingClaims) == 0 {
		return claimsynctypes.Claim{}, false, nil
	}

	usedUnclaims := make([]bool, len(buildParams.Unclaims))
	for _, claim := range missingClaims {
		if claimHasPosteriorUnclaim(claim, buildParams.Unclaims, usedUnclaims) {
			continue
		}
		return claim, true, nil
	}

	return claimsynctypes.Claim{}, false, nil
}

func claimHasPosteriorUnclaim(
	claim claimsynctypes.Claim,
	unclaims []claimsynctypes.Unclaim,
	usedUnclaims []bool,
) bool {
	if claim.GlobalIndex == nil {
		return false
	}

	for idx, unclaim := range unclaims {
		if usedUnclaims[idx] || unclaim.GlobalIndex == nil {
			continue
		}

		if claim.GlobalIndex.Cmp(unclaim.GlobalIndex) != 0 {
			continue
		}

		if compareClaimToUnclaimOrder(claim, unclaim) >= 0 {
			continue
		}

		usedUnclaims[idx] = true
		return true
	}

	return false
}

func compareClaimToUnclaimOrder(claim claimsynctypes.Claim, unclaim claimsynctypes.Unclaim) int {
	// Event ordering is block number first, then the intra-block position.
	// Equal positions are treated as simultaneous, so the unclaim is not considered posterior.
	return compareEventOrder(claim.BlockNum, claim.BlockPos, unclaim.BlockNumber, unclaim.LogIndex)
}

func compareEventOrder(leftBlock, leftIndex, rightBlock, rightIndex uint64) int {
	switch {
	case leftBlock < rightBlock:
		return -1
	case leftBlock > rightBlock:
		return 1
	case leftIndex < rightIndex:
		return -1
	case leftIndex > rightIndex:
		return 1
	default:
		return 0
	}
}

func trimCertificateToBlock(
	buildParams *types.CertificateBuildParams,
	newToBlock uint64,
) (*types.CertificateBuildParams, error) {
	if buildParams.ToBlock < newToBlock {
		return nil, fmt.Errorf("cannot adjust toBlock to a higher value. current toBlock: %d, new toBlock: %d",
			buildParams.ToBlock, newToBlock)
	}

	if buildParams.ToBlock == newToBlock {
		return buildParams, nil
	}

	if newToBlock < buildParams.FromBlock {
		return nil, fmt.Errorf("cannot create certificate: trimming to block %d would move before start block %d",
			newToBlock, buildParams.FromBlock)
	}

	return cloneCertificateBuildParamsWithRange(buildParams, buildParams.FromBlock, newToBlock)
}

func cloneCertificateBuildParamsWithRange(
	buildParams *types.CertificateBuildParams,
	fromBlock uint64,
	toBlock uint64,
) (*types.CertificateBuildParams, error) {
	if buildParams == nil {
		return nil, ErrBuildParamsIsNil
	}

	if buildParams.FromBlock > fromBlock || buildParams.ToBlock < toBlock {
		return nil, fmt.Errorf("invalid range. FromBlock %d and ToBlock %d are not within "+
			"the certificate range FromBlock %d and ToBlock %d",
			fromBlock, toBlock, buildParams.FromBlock, buildParams.ToBlock)
	}

	if fromBlock > toBlock {
		return nil, fmt.Errorf("invalid range. FromBlock %d is greater than toBlock %d", fromBlock, toBlock)
	}

	if buildParams.FromBlock == fromBlock && buildParams.ToBlock == toBlock {
		return buildParams, nil
	}

	span := toBlock - fromBlock + 1
	fullSpan := buildParams.ToBlock - buildParams.FromBlock + 1

	return &types.CertificateBuildParams{
		FromBlock:                      fromBlock,
		ToBlock:                        toBlock,
		Bridges:                        filterBridgesInRange(buildParams.Bridges, fromBlock, toBlock, span, fullSpan),
		Claims:                         filterClaimsInRange(buildParams.Claims, fromBlock, toBlock, span, fullSpan),
		Unclaims:                       filterUnclaimsInRange(buildParams.Unclaims, fromBlock, toBlock, span, fullSpan),
		CreatedAt:                      buildParams.CreatedAt,
		RetryCount:                     buildParams.RetryCount,
		LastSentCertificate:            buildParams.LastSentCertificate,
		L1InfoTreeRootFromWhichToProve: buildParams.L1InfoTreeRootFromWhichToProve,
		L1InfoTreeLeafCount:            buildParams.L1InfoTreeLeafCount,
		AggchainProof:                  buildParams.AggchainProof,
		CertificateType:                buildParams.CertificateType,
		ExtraData:                      buildParams.ExtraData,
	}, nil
}

func filterBridgesInRange(
	bridges []bridgesync.Bridge,
	fromBlock uint64,
	toBlock uint64,
	span uint64,
	fullSpan uint64,
) []bridgesync.Bridge {
	filtered := make([]bridgesync.Bridge, 0, aggkitcommon.EstimateSliceCapacity(len(bridges), span, fullSpan))
	for _, bridge := range bridges {
		if bridge.BlockNum >= fromBlock && bridge.BlockNum <= toBlock {
			filtered = append(filtered, bridge)
		}
	}
	return filtered
}

func filterClaimsInRange(
	claims []claimsynctypes.Claim,
	fromBlock uint64,
	toBlock uint64,
	span uint64,
	fullSpan uint64,
) []claimsynctypes.Claim {
	filtered := make([]claimsynctypes.Claim, 0, aggkitcommon.EstimateSliceCapacity(len(claims), span, fullSpan))
	for _, claim := range claims {
		if claim.BlockNum >= fromBlock && claim.BlockNum <= toBlock {
			filtered = append(filtered, claim)
		}
	}
	return filtered
}

func filterUnclaimsInRange(
	unclaims []claimsynctypes.Unclaim,
	fromBlock uint64,
	toBlock uint64,
	span uint64,
	fullSpan uint64,
) []claimsynctypes.Unclaim {
	filtered := make([]claimsynctypes.Unclaim, 0, aggkitcommon.EstimateSliceCapacity(len(unclaims), span, fullSpan))
	for _, unclaim := range unclaims {
		if unclaim.BlockNumber >= fromBlock && unclaim.BlockNumber <= toBlock {
			filtered = append(filtered, unclaim)
		}
	}
	return filtered
}

func isUpcomingNextRange(maxL2BlockNumber, fromBlock, toBlock uint64) bool {
	if maxL2BlockNumber == 0 {
		return false
	}

	return fromBlock == maxL2BlockNumber+1 && toBlock > maxL2BlockNumber
}
