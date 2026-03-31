package flows

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkitdb "github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
)

var (
	errNoBridgesAndClaims = errors.New("no bridges and claims to build certificate")
	errNoNewBlocks        = errors.New("no new blocks to send a certificate")
)

// TimeNowUTC returns the current time in UTC as a uint32 timestamp.
func TimeNowUTC() uint32 {
	// Use a more precise time function to avoid collisions in tests
	// and ensure that the time is always in UTC.
	return uint32(time.Now().UTC().Unix())
}

// BaseFlowConfig is a struct that holds the configuration for the base flow
type BaseFlowConfig struct {
	// MaxCertSize is the maximum size of the certificate in bytes. 0 means no limit
	MaxCertSize uint
	// StartL2Block is the L2 block number from which to start sending certificates.
	// It is used to determine the first block to include in the certificate.
	// It can be 0
	StartL2Block uint64
	// RequireNoFEPBlockGap indicates whether the flow requires no gap between the
	// first FEP block and last settled certificate.
	RequireNoFEPBlockGap bool
	// FullClaimsNeeded indicates whether the flow requires full claims data
	FullClaimsNeeded bool
}

// NewBaseFlowConfigDefault returns a BaseFlowConfig with default values
func NewBaseFlowConfigDefault() BaseFlowConfig {
	return BaseFlowConfig{
		MaxCertSize:          0,     // 0 means no limit
		StartL2Block:         0,     // 0 means start from the first block
		RequireNoFEPBlockGap: false, // default is false, can be set to true if needed
		FullClaimsNeeded:     true,  // default is true, can be set to false if full claims are not needed
	}
}

// NewBaseFlowConfig returns a BaseFlowConfig with the specified maxCertSize and startL2Block
func NewBaseFlowConfig(
	maxCertSize uint,
	startL2Block uint64,
	requireNoFEPBlockGap bool,
	fullClaimsNeeded bool,
) BaseFlowConfig {
	return BaseFlowConfig{
		MaxCertSize:          maxCertSize,
		StartL2Block:         startL2Block,
		RequireNoFEPBlockGap: requireNoFEPBlockGap,
		FullClaimsNeeded:     fullClaimsNeeded,
	}
}

// baseFlow is a struct that holds the common logic for the different prover types
type baseFlow struct {
	l2BridgeQuerier       types.BridgeQuerier
	storage               db.AggSenderStorage
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
	lerQuerier            types.LERQuerier
	cfg                   BaseFlowConfig
	log                   types.Logger
	// TimeNowFunc is a function that returns the current time as a uint32 timestamp.
	timeNowFunc func() uint32
}

// NewBaseFlow creates a new instance of the base flow
func NewBaseFlow(
	log types.Logger,
	l2BridgeQuerier types.BridgeQuerier,
	storage db.AggSenderStorage,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
	lerQuerier types.LERQuerier,
	cfg BaseFlowConfig,
) *baseFlow {
	return &baseFlow{
		log:                   log,
		l2BridgeQuerier:       l2BridgeQuerier,
		storage:               storage,
		l1InfoTreeDataQuerier: l1InfoTreeDataQuerier,
		lerQuerier:            lerQuerier,
		cfg:                   cfg,
		timeNowFunc:           TimeNowUTC,
	}
}

// StartL2Block returns the L2 block number from which to start sending certificates.
func (f *baseFlow) StartL2Block() uint64 {
	return f.cfg.StartL2Block
}

// NextCertificateBlockRange returns the block range and retryCount for the next certificate
func (f *baseFlow) NextCertificateBlockRange(ctx context.Context,
	lastSentCertificate *types.CertificateHeader) (aggkitcommon.BlockRange, int, error) {
	lastL2BlockSynced, err := f.l2BridgeQuerier.GetLastProcessedBlock(ctx)
	if err != nil {
		return aggkitcommon.BlockRangeZero, 0, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	previousToBlock, retryCount := f.getLastSentBlockAndRetryCount(lastSentCertificate)
	if previousToBlock >= lastL2BlockSynced {
		f.log.Infof("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return aggkitcommon.BlockRangeZero, 0, errNoNewBlocks
	}
	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced
	return aggkitcommon.NewBlockRange(fromBlock, toBlock), retryCount, nil
}

// GetLastCertificate returns latest certificate in local database
func (f *baseFlow) GetLastCertificate(ctx context.Context) (*types.CertificateHeader, error) {
	lastSentCertificate, err := f.storage.GetLastSentCertificateHeader()
	if err != nil {
		return nil, fmt.Errorf("fails to GetLastCertificate. Err: %w", err)
	}
	return lastSentCertificate, nil
}

func (f *baseFlow) GeneratePreBuildParams(ctx context.Context,
	certType types.CertificateType) (*types.CertificatePreBuildParams, error) {
	lastSentCertificate, err := f.GetLastCertificate(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last sent certificate: %w", err)
	}

	nextBlocks, retryCount, err := f.NextCertificateBlockRange(ctx, lastSentCertificate)
	if err != nil {
		return nil, fmt.Errorf("error getting next certificate block range: %w", err)
	}
	l1InfoRoot, _, err := f.l1InfoTreeDataQuerier.GetLatestFinalizedL1InfoRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting latest finalized L1 info root: %w", err)
	}

	return &types.CertificatePreBuildParams{
		BlockRange:          nextBlocks,
		RetryCount:          retryCount,
		LastSentCertificate: lastSentCertificate,
		CertificateType:     certType,
		L1InfoTreeToProve: &types.CertificateL1InfoTreeData{
			L1InfoTreeRootToProve: l1InfoRoot.Hash,
			L1InfoTreeLeafCount:   l1InfoRoot.Index + 1,
		},
		CreatedAt: f.timeNowFunc(),
	}, nil
}

func (f *baseFlow) GenerateBuildParams(ctx context.Context,
	preParams types.CertificatePreBuildParams) (*types.CertificateBuildParams, error) {
	if preParams.L1InfoTreeToProve == nil {
		return nil, fmt.Errorf("L1InfoTreeWhichToProve should be not nil for GenerateBuildParams")
	}

	bridges, claims, err := f.l2BridgeQuerier.GetBridgesAndClaims(ctx,
		preParams.BlockRange.FromBlock, preParams.BlockRange.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("generateBuildParams fails getting bridges and claims. Err: %w", err)
	}

	unclaims, err := f.l2BridgeQuerier.GetUnsetClaimsForBlockRange(ctx,
		preParams.BlockRange.FromBlock, preParams.BlockRange.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("error getting unset claims for block range: %w", err)
	}

	buildParams := &types.CertificateBuildParams{
		FromBlock:                      preParams.BlockRange.FromBlock,
		ToBlock:                        preParams.BlockRange.ToBlock,
		RetryCount:                     preParams.RetryCount,
		LastSentCertificate:            preParams.LastSentCertificate,
		Bridges:                        bridges,
		Claims:                         claims,
		CreatedAt:                      preParams.CreatedAt,
		CertificateType:                preParams.CertificateType,
		L1InfoTreeRootFromWhichToProve: preParams.L1InfoTreeToProve.L1InfoTreeRootToProve,
		L1InfoTreeLeafCount:            preParams.L1InfoTreeToProve.L1InfoTreeLeafCount,
		Unclaims:                       unclaims,
	}

	buildParams, err = f.adjustCertificateIfNonFinalizedClaims(buildParams)
	if err != nil {
		return nil, fmt.Errorf("error adjusting certificate if non-finalized claims: %w", err)
	}

	return buildParams, nil
}

// GetCertificateBuildParamsInternal returns the parameters to build a certificate
func (f *baseFlow) GetCertificateBuildParamsInternal(
	ctx context.Context, certType types.CertificateType) (*types.CertificateBuildParams, error) {
	preParams, err := f.GeneratePreBuildParams(ctx, certType)
	if err != nil {
		return nil, fmt.Errorf("error generating pre build params: %w", err)
	}
	params, err := f.GenerateBuildParams(ctx, *preParams)
	if err != nil {
		return nil, fmt.Errorf("error generating build params: %w", err)
	}
	params, err = f.LimitCertSize(params)
	if err != nil {
		return nil, fmt.Errorf("error applying limit size: %w", err)
	}
	return params, nil
}

// VerifyBuildParams verifies the build parameters
func (f *baseFlow) VerifyBuildParams(ctx context.Context, fullCert *types.CertificateBuildParams) error {
	if err := f.verifyRetryCertStartingBlock(fullCert); err != nil {
		return fmt.Errorf("error verifying retry certificate starting block: %w", err)
	}

	if err := f.verifyClaimGERs(fullCert.Claims); err != nil {
		return err
	}

	return nil
}

// LimitCertSize limits certificate size based on the max size configuration parameter
// size is expressed in bytes
func (f *baseFlow) LimitCertSize(
	certParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	originalCert := certParams
	currentCert := certParams
	var err error
	maxCertSize := f.cfg.MaxCertSize
	for {
		if maxCertSize == 0 || currentCert.EstimatedSize() <= maxCertSize {
			if originalCert != nil && currentCert != nil && currentCert.ToBlock < originalCert.ToBlock {
				if err := f.logLimiterBlockedInvalidClaim(originalCert, currentCert, "MaxCertSize"); err != nil {
					f.log.Warnf("unable to assess non-finalized claim after MaxCertSize reduction: %v", err)
				}
			}
			return currentCert, nil
		}

		if currentCert.NumberOfBlocks() <= 1 {
			f.log.Warnf("Minimum number of blocks reached [%d to %d]. Estimated size: %d > max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), maxCertSize)
			return currentCert, nil
		}

		currentCert, err = currentCert.Range(currentCert.FromBlock, currentCert.ToBlock-1)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}
	}
}

// GetNewLocalExitRoot gets the new local exit root for the certificate
func (f *baseFlow) GetNewLocalExitRoot(ctx context.Context,
	certParams *types.CertificateBuildParams) (common.Hash, error) {
	if certParams == nil {
		return common.Hash{}, fmt.Errorf("baseFlow.GetNewLocalExitRoot. certificate build parameters cannot be nil")
	}
	_, previousLER, err := f.getNextHeightAndPreviousLER(certParams.LastSentCertificate)
	if err != nil {
		return common.Hash{}, fmt.Errorf("baseFlow.GetNewLocalExitRoot. error getting next height and previous LER: %w", err)
	}

	newLER, err := f.getNewLocalExitRoot(ctx, certParams, previousLER)
	if err != nil {
		return common.Hash{}, fmt.Errorf("baseFlow.GetNewLocalExitRoot. error getting new local exit root: %w", err)
	}
	return newLER, nil
}

func (f *baseFlow) BuildCertificate(ctx context.Context,
	certParams *types.CertificateBuildParams,
	lastSentCertificate *types.CertificateHeader,
	allowEmptyCert bool) (*agglayertypes.Certificate, error) {
	f.log.Infof("building certificate for %s estimatedSize=%d", certParams.String(), certParams.EstimatedSize())

	if !allowEmptyCert && certParams.IsEmpty() {
		return nil, errNoBridgesAndClaims
	}

	bridgeExits := f.getBridgeExits(certParams.Bridges)
	importedBridgeExits, err := f.getImportedBridgeExits(
		ctx, certParams.Claims, certParams.Unclaims, certParams.L1InfoTreeRootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf("error getting imported bridge exits: %w", err)
	}

	height, previousLER, err := f.getNextHeightAndPreviousLER(lastSentCertificate)
	if err != nil {
		return nil, fmt.Errorf("error getting next height and previous LER: %w", err)
	}

	newLER, err := f.getNewLocalExitRoot(ctx, certParams, previousLER)
	if err != nil {
		return nil, fmt.Errorf("error getting new local exit root: %w", err)
	}

	return &agglayertypes.Certificate{
		NetworkID:           f.l2BridgeQuerier.OriginNetwork(),
		PrevLocalExitRoot:   previousLER,
		NewLocalExitRoot:    newLER,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		Height:              height,
		L1InfoTreeLeafCount: certParams.L1InfoTreeLeafCount,
	}, nil
}

// getNewLocalExitRoot gets the new local exit root for the certificate
func (f *baseFlow) getNewLocalExitRoot(
	ctx context.Context,
	certParams *types.CertificateBuildParams,
	previousLER common.Hash) (common.Hash, error) {
	if certParams.NumberOfBridges() == 0 {
		// if there is no bridge exits we return the previous LER
		// since there was no change in the local exit root
		return previousLER, nil
	}

	depositCount := certParams.MaxDepositCount()

	exitRoot, err := f.l2BridgeQuerier.GetExitRootByIndex(ctx, depositCount)
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting exit root by index: %d. Error: %w", depositCount, err)
	}

	return exitRoot, nil
}

// ConvertClaimToImportedBridgeExit converts a claim to an ImportedBridgeExit object
func (f *baseFlow) ConvertClaimToImportedBridgeExit(claim bridgesync.Claim) (*agglayertypes.ImportedBridgeExit, error) {
	return converters.ConvertToImportedBridgeExitWithoutClaimData(claim)
}

// getBridgeExits converts bridges to agglayer.BridgeExit objects
func (f *baseFlow) getBridgeExits(bridges []bridgesync.Bridge) []*agglayertypes.BridgeExit {
	return converters.ConvertToBridgeExits(bridges)
}

// getImportedBridgeExits converts claims to agglayertypes.ImportedBridgeExit objects and calculates necessary proofs
func (f *baseFlow) getImportedBridgeExits(
	ctx context.Context,
	claims []bridgesync.Claim,
	unclaims []bridgesynctypes.Unclaim,
	rootFromWhichToProve common.Hash,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	// Build unclaim counts by GlobalIndex
	// Use string representation as map key since *big.Int pointer comparison doesn't work
	unclaimCnt := make(map[string]int)
	for _, u := range unclaims {
		if u.GlobalIndex != nil {
			key := u.GlobalIndex.String()
			unclaimCnt[key]++
		}
	}

	filteredClaims := make([]bridgesync.Claim, 0)
	for _, c := range claims {
		if c.GlobalIndex != nil {
			key := c.GlobalIndex.String()
			if unclaimCnt[key] > 0 {
				unclaimCnt[key]--
			} else {
				filteredClaims = append(filteredClaims, c)
			}
		} else {
			// If GlobalIndex is nil, include the claim
			filteredClaims = append(filteredClaims, c)
		}
	}

	if f.cfg.FullClaimsNeeded {
		return converters.ConvertToImportedBridgeExits(
			ctx, filteredClaims, rootFromWhichToProve, f.l1InfoTreeDataQuerier,
		)
	}
	return converters.ConvertToImportedBridgeExitsWithoutClaimData(filteredClaims)
}

// getNextHeightAndPreviousLER returns the height and previous LER for the new certificate
func (f *baseFlow) getNextHeightAndPreviousLER(
	lastSentCertificateInfo *types.CertificateHeader) (uint64, common.Hash, error) {
	if lastSentCertificateInfo == nil {
		ler, err := f.lerQuerier.GetLastLocalExitRoot()
		return uint64(0), ler, err
	}
	if !lastSentCertificateInfo.Status.IsClosed() {
		return 0, aggkitcommon.ZeroHash, fmt.Errorf("last certificate %s is not closed (status: %s)",
			lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
	}
	if lastSentCertificateInfo.Status.IsSettled() {
		return lastSentCertificateInfo.Height + 1, lastSentCertificateInfo.NewLocalExitRoot, nil
	}

	if lastSentCertificateInfo.Status.IsInError() {
		// We can reuse last one of lastCert?
		if lastSentCertificateInfo.PreviousLocalExitRoot != nil {
			return lastSentCertificateInfo.Height, *lastSentCertificateInfo.PreviousLocalExitRoot, nil
		}
		// Is the first one, so we can set the zeroLER
		if lastSentCertificateInfo.Height == 0 {
			ler, err := f.lerQuerier.GetLastLocalExitRoot()
			return uint64(0), ler, err
		}
		// We get previous certificate that must be settled
		f.log.Debugf("last certificate %s is in error, getting previous settled certificate height:%d",
			lastSentCertificateInfo.Height-1)
		lastSettleCert, err := f.storage.GetCertificateHeaderByHeight(lastSentCertificateInfo.Height - 1)
		if err != nil {
			return 0, aggkitcommon.ZeroHash, fmt.Errorf("error getting last settled certificate: %w", err)
		}
		if lastSettleCert == nil {
			return 0, aggkitcommon.ZeroHash, fmt.Errorf("none settled certificate: %w", err)
		}
		if !lastSettleCert.Status.IsSettled() {
			return 0, aggkitcommon.ZeroHash, fmt.Errorf("last settled certificate %s is not settled (status: %s)",
				lastSettleCert.ID(), lastSettleCert.Status.String())
		}

		return lastSentCertificateInfo.Height, lastSettleCert.NewLocalExitRoot, nil
	}
	return 0, aggkitcommon.ZeroHash, fmt.Errorf("last certificate %s has an unknown status: %s",
		lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
}

// verifyClaimGERs verifies the correctnes GERs of the claims
func (f *baseFlow) verifyClaimGERs(claims []bridgesync.Claim) error {
	for _, claim := range claims {
		ger := l1infotreesync.CalculateGER(claim.MainnetExitRoot, claim.RollupExitRoot)
		if ger != claim.GlobalExitRoot {
			return fmt.Errorf("claim[GlobalIndex: %s, BlockNum: %d]: GER mismatch. Expected: %s, got: %s",
				claim.GlobalIndex.String(), claim.BlockNum, claim.GlobalExitRoot.String(), ger.String())
		}
	}

	return nil
}

// verifyRetryCertStartingBlock verifies that the starting block of a retry certificate
// matches the last sent (InError) certificate's starting block.
func (f *baseFlow) verifyRetryCertStartingBlock(buildParams *types.CertificateBuildParams) error {
	if buildParams.IsARetry() && buildParams.FromBlock != buildParams.LastSentCertificate.FromBlock {
		return fmt.Errorf("retry certificate fromBlock %d != last sent certificate fromBlock %d",
			buildParams.FromBlock, buildParams.LastSentCertificate.FromBlock)
	}

	return nil
}

// VerifyBlockRangeGaps checks if there are any gaps in the block range of the certificate
// and verifies that there are no new bridges or claims in the gap.
func (f *baseFlow) VerifyBlockRangeGaps(
	ctx context.Context,
	lastSentCertificate *types.CertificateHeader,
	newFromBlock, newToBlock uint64) error {
	if lastSentCertificate == nil {
		return nil
	}

	lastSettledFromBlock := uint64(0)
	lastSettledToBlock := uint64(0)
	if lastSentCertificate.Status.IsInError() {
		// if the last certificate was in error, we need to check the last
		// settled range to be correct
		// we will leave the from block as 0, since we only require the to block
		// to check the gap between the last sent certificate and the new one
		if lastSentCertificate.FromBlock > 0 {
			lastSettledToBlock = lastSentCertificate.FromBlock - 1
		}
	} else {
		lastSettledFromBlock = lastSentCertificate.FromBlock
		lastSettledToBlock = lastSentCertificate.ToBlock
	}

	nextBlockRange := aggkitcommon.NewBlockRange(newFromBlock, newToBlock)
	lastBlockRange := aggkitcommon.NewBlockRange(lastSettledFromBlock, lastSettledToBlock)

	if lastBlockRange.Greater(nextBlockRange) {
		// This is a strange situation, but don't need to check anything.
		// the way of using this function is that newXXXBlock is SC.StartingBlockNumber
		// so newXXXBlock can be a previous block
		return nil
	}

	// case 2: is a new cert but is not contiguous to previous one
	gap := nextBlockRange.Gap(lastBlockRange)
	if gap.IsEmpty() {
		return nil
	}

	bridgeDataInTheGap, claimDataInTheGap, err := f.l2BridgeQuerier.GetBridgesAndClaims(
		ctx, gap.FromBlock, gap.ToBlock)
	if err != nil {
		return fmt.Errorf("error getting bridges and claims in the gap %s: %w", gap.String(), err)
	}
	if len(bridgeDataInTheGap) > 0 || len(claimDataInTheGap) > 0 {
		return fmt.Errorf("there are new bridges or claims in the gap %s, len(bridges)=%d. len(claims)=%d",
			gap.String(), len(bridgeDataInTheGap), len(claimDataInTheGap))
	}

	if !gap.IsEmpty() && f.cfg.RequireNoFEPBlockGap {
		// even though we do not have bridge transactions in the gap,
		// we need to return an error if RequireNoFEPBlockGap is true
		return fmt.Errorf("block gap detected: %s without bridge transactions, but RequireNoFEPBlockGap is true",
			gap.String())
	}

	return nil
}

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previously sent certificate, it returns startL2Block and 0
func (f *baseFlow) getLastSentBlockAndRetryCount(lastSentCertificateInfo *types.CertificateHeader) (uint64, int) {
	if lastSentCertificateInfo == nil {
		// this is the first certificate so we start from what we have set in start L2 block
		return f.StartL2Block(), 0
	}

	retryCount := 0
	lastSentBlock := lastSentCertificateInfo.ToBlock

	if lastSentCertificateInfo.Status == agglayertypes.InError {
		// if the last certificate was in error, we need to resend it
		// from the block before the error
		if lastSentCertificateInfo.FromBlock > 0 {
			lastSentBlock = lastSentCertificateInfo.FromBlock - 1
		}

		retryCount = lastSentCertificateInfo.RetryCount + 1
	}
	return lastSentBlock, retryCount
}

// gerStatusCache stores the cached status of GER checks
type gerStatusCache struct {
	finalized  map[common.Hash]bool
	existsOnL1 map[common.Hash]bool
	errors     map[common.Hash]error
}

type invalidClaimAssessmentReason string

const (
	invalidClaimAssessmentReasonRecoverable invalidClaimAssessmentReason = "recoverable"
	invalidClaimAssessmentReasonNoUnclaim   invalidClaimAssessmentReason = "no_unclaim"
)

type invalidClaimAssessment struct {
	reason            invalidClaimAssessmentReason
	cutBlock          uint64
	cutClaim          *bridgesync.Claim
	culpritClaim      *bridgesync.Claim
	culpritUnclaim    uint64
	hasCulpritUnclaim bool
}

// newGERStatusCache creates a new GER status cache
func newGERStatusCache() *gerStatusCache {
	return &gerStatusCache{
		finalized:  make(map[common.Hash]bool),
		existsOnL1: make(map[common.Hash]bool),
		errors:     make(map[common.Hash]error),
	}
}

// getGERFinalizedStatus checks if a GER is finalized, using cache if available
func (f *baseFlow) getGERFinalizedStatus(
	cache *gerStatusCache,
	ger common.Hash,
	l1InfoTreeLeafCount uint32) (bool, error) {
	if cached, ok := cache.finalized[ger]; ok {
		// Check if there was an error for this GER
		if err, hasErr := cache.errors[ger]; hasErr {
			return false, err
		}
		return cached, nil
	}

	// Not in cache, call the querier
	isFinalized, err := f.l1InfoTreeDataQuerier.IsGERFinalized(ger, l1InfoTreeLeafCount)
	if err != nil && !errors.Is(err, aggkitdb.ErrNotFound) {
		cache.errors[ger] = err
		return false, err
	}
	cache.finalized[ger] = isFinalized
	return isFinalized, nil
}

// isGERExistentOnL1Status checks if a GER exists on L1, using cache if available
func (f *baseFlow) isGERExistentOnL1Status(
	cache *gerStatusCache,
	ger common.Hash) (bool, error) {
	if cached, ok := cache.existsOnL1[ger]; ok {
		// Check if there was an error for this GER
		if err, hasErr := cache.errors[ger]; hasErr {
			return false, err
		}
		return cached, nil
	}

	// Not in cache, call the querier
	exists, err := f.l1InfoTreeDataQuerier.DoesGERExistsOnL1(ger)
	if err != nil {
		cache.errors[ger] = err
		return false, err
	}
	cache.existsOnL1[ger] = exists
	return exists, nil
}

// adjustCertificateIfNonFinalizedClaims checks if any claims in the certificate parameters
// contain non-finalized Global Exit Roots (GERs). If a non-finalized GER is found, it
// adjusts the certificate parameters to exclude that block and all subsequent blocks by
// resizing the certificate to the block before the non-finalized claim.
//
// The function iterates through all claims in the certificate parameters and verifies
// each claim's Global Exit Root finalization status using the L1 info tree data querier.
// When a non-finalized GER is encountered, the certificate is truncated at the block
// number preceding the problematic claim to ensure all included claims are finalized.
//
// Parameters:
//   - certParams: Certificate build parameters containing claims to be validated
//
// Returns:
//   - *types.CertificateBuildParams: Adjusted certificate parameters if non-finalized
//     claims are found, otherwise returns the original parameters
//   - error: Error if GER finalization status check fails
func (f *baseFlow) adjustCertificateIfNonFinalizedClaims(
	certParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	// Create a cache to avoid multiple calls for the same GER
	cache := newGERStatusCache()

	for _, c := range certParams.Claims {
		isGERFinalized, err := f.getGERFinalizedStatus(cache, c.GlobalExitRoot, certParams.L1InfoTreeLeafCount)
		if err != nil {
			return nil, fmt.Errorf("error checking if GER %s is finalized: %w", c.GlobalExitRoot.String(), err)
		}

		if !isGERFinalized {
			// check on L1 if GER exists
			exists, err := f.isGERExistentOnL1Status(cache, c.GlobalExitRoot)
			if err != nil {
				return nil, fmt.Errorf("error checking if GER %s exists on L1: %w", c.GlobalExitRoot.String(), err)
			}

			if exists {
				f.log.Warnf("found a non-finalized GER: %s on block: %d. "+
					"we will adjust the certificate to exclude it and all blocks after it",
					c.GlobalExitRoot.String(), c.BlockNum)
				certParams, err = certParams.AdjustToBlock(c.BlockNum - 1)
				if err != nil {
					return nil, fmt.Errorf("error adjusting certificate to block %d: %w", c.BlockNum-1, err)
				}
				break
			}
		}
	}

	// Validate unclaims for unfinalized GERs that don't exist on L1 in a single pass.
	// This checks both:
	// 1. If any claim with unfinalized GER that doesn't exist on L1 has an unclaim
	//    that appears after a later unfinalized claim
	// 2. If any previous claims with unfinalized GERs that don't exist on L1 have their
	//    unclaims before the current block
	assessment, err := f.validateUnclaimsForUnfinalizedGERs(certParams, cache)
	if err != nil {
		return nil, fmt.Errorf("error validating unclaims for unfinalized GERs: %w", err)
	}
	if assessment != nil && assessment.reason == invalidClaimAssessmentReasonRecoverable {
		f.log.Infof("found invalid claim with matching unclaim in current cert, aggsender can proceed. %s, unclaim_block=%d, cert_range=%d-%d",
			formatClaimForLogs(*assessment.culpritClaim), assessment.culpritUnclaim, certParams.FromBlock, certParams.ToBlock)
	}
	if assessment != nil && assessment.cutBlock != 0 {
		f.logInvalidClaimNeedsUnclaim(certParams, assessment)
		newToBlock := assessment.cutBlock - 1
		if newToBlock < certParams.FromBlock {
			return nil, fmt.Errorf(
				"cannot create certificate: claim at block %d (start block %d) cannot be included and no valid blocks before it",
				assessment.cutBlock, certParams.FromBlock)
		}
		f.log.Warnf("found claim with unclaim after later unfinalized claim at block %d, cutting certificate at block %d",
			assessment.cutBlock, newToBlock)
		return certParams.AdjustToBlock(newToBlock)
	}

	return certParams, nil
}

// validateUnclaimsForUnfinalizedGERs validates unclaims for unfinalized GERs that don't exist on L1
// in a single pass. This function combines two checks:
//  1. Checks if any claim with unfinalized GER that doesn't exist on L1 has an unclaim that appears
//     after a later unfinalized claim. If so, returns the block number to cut at.
//  2. Validates that any previous claims with unfinalized GERs that don't exist on L1 have their
//     unclaims before the current claim's block. If a claim without an unclaim is found, returns
//     the block number to cut at.
//
// Returns the earliest cut block found (or 0 if no cut is needed) and an error if validation fails.
func (f *baseFlow) validateUnclaimsForUnfinalizedGERs(
	certParams *types.CertificateBuildParams,
	cache *gerStatusCache) (*invalidClaimAssessment, error) {
	// Build a map of unclaims by GlobalIndex for quick lookup
	unclaimMap := make(map[string]uint64)
	for _, unclaim := range certParams.Unclaims {
		key := unclaim.GlobalIndex.String()
		// Keep the earliest unclaim if there are multiple
		if existing, ok := unclaimMap[key]; !ok || unclaim.BlockNumber < existing {
			unclaimMap[key] = unclaim.BlockNumber
		}
	}

	var recoverableClaim *invalidClaimAssessment

	// Single pass through all claims to perform both checks
	for i, claim := range certParams.Claims {
		// Check if this claim's GER is finalized
		isGERFinalized, err := f.getGERFinalizedStatus(cache, claim.GlobalExitRoot, certParams.L1InfoTreeLeafCount)
		if err != nil {
			return nil, fmt.Errorf("error checking if claim's GER %s is finalized: %w",
				claim.GlobalExitRoot.String(), err)
		}

		// Skip if GER is finalized
		if isGERFinalized {
			continue
		}

		// Check 1: Validate that claims before currentBlockNum with unfinalized GERs that don't exist on L1
		// have their unclaims before currentBlockNum
		unclaimBlock, hasUnclaim := unclaimMap[claim.GlobalIndex.String()]
		if !hasUnclaim {
			currentClaim := claim
			return &invalidClaimAssessment{
				reason:       invalidClaimAssessmentReasonNoUnclaim,
				cutBlock:     claim.BlockNum,
				cutClaim:     &currentClaim,
				culpritClaim: &currentClaim,
			}, nil
		}

		// Check 2: Ensure we can include this claim's unclaim without being forced to include
		// a later unfinalized claim that doesn't exist on L1 and remains active in the same
		// certificate range.
		//
		// If the later claim also has an unclaim somewhere within the candidate certificate,
		// getImportedBridgeExits will cancel that claim out regardless of the relative order of
		// the two unclaims inside the range. What matters here is whether the later claim is
		// still active by the end of the certificate, not whether its unclaim happens before the
		// current claim's unclaim.
		for j := i + 1; j < len(certParams.Claims); j++ {
			laterClaim := certParams.Claims[j]
			if laterClaim.BlockNum > unclaimBlock {
				// Later claim is after the unclaim, so we can include the unclaim without including the later claim
				continue
			}

			// Check if the later claim is unfinalized
			isLaterGERFinalized, err := f.getGERFinalizedStatus(cache, laterClaim.GlobalExitRoot, certParams.L1InfoTreeLeafCount)
			if err != nil {
				return nil, fmt.Errorf("error checking if later claim's GER %s is finalized: %w",
					laterClaim.GlobalExitRoot.String(), err)
			}
			if isLaterGERFinalized {
				continue
			}

			// Later unfinalized claim that doesn't exist on L1.
			// If it also has an unclaim within the current certificate range, both claims will be
			// canceled out when imported bridge exits are built.
			if _, hasLaterUnclaim := unclaimMap[laterClaim.GlobalIndex.String()]; hasLaterUnclaim {
				continue
			}
			// Later claim doesn't have an unclaim in the current certificate range.
			// We need to cut before the current claim
			cutClaim := claim
			blockingClaim := laterClaim
			return &invalidClaimAssessment{
				reason:       invalidClaimAssessmentReasonNoUnclaim,
				cutBlock:     claim.BlockNum,
				cutClaim:     &cutClaim,
				culpritClaim: &blockingClaim,
			}, nil
		}

		if recoverableClaim == nil {
			currentClaim := claim
			recoverableClaim = &invalidClaimAssessment{
				reason:            invalidClaimAssessmentReasonRecoverable,
				culpritClaim:      &currentClaim,
				culpritUnclaim:    unclaimBlock,
				hasCulpritUnclaim: true,
			}
		}
	}

	return recoverableClaim, nil
}

func (f *baseFlow) logInvalidClaimNeedsUnclaim(
	certParams *types.CertificateBuildParams,
	assessment *invalidClaimAssessment,
) {
	if assessment == nil || assessment.culpritClaim == nil {
		return
	}

	msg := fmt.Sprintf("blocking invalid claim requires an unclaim before aggsender can proceed. %s, synced_cert_range=%d-%d",
		formatClaimForLogs(*assessment.culpritClaim), certParams.FromBlock, certParams.ToBlock)
	if assessment.cutClaim != nil && assessment.cutClaim.GlobalIndex != nil &&
		assessment.culpritClaim.GlobalIndex != nil &&
		assessment.cutClaim.GlobalIndex.Cmp(assessment.culpritClaim.GlobalIndex) != 0 {
		msg += fmt.Sprintf(", current_cut_claim_block=%d, current_cut_claim_global_index=%s",
			assessment.cutClaim.BlockNum, assessment.cutClaim.GlobalIndex.String())
	}
	f.log.Warnf("%s. No matching unclaim was found in the current DB-backed candidate certificate. An unclaim needs to happen for aggsender to get unstuck.", msg)
}

func (f *baseFlow) logLimiterBlockedInvalidClaim(
	fullCert *types.CertificateBuildParams,
	limitedCert *types.CertificateBuildParams,
	limiterName string,
) error {
	if fullCert == nil || limitedCert == nil || limitedCert.ToBlock >= fullCert.ToBlock {
		return nil
	}

	cache := newGERStatusCache()
	unclaimMap := make(map[string]uint64)
	for _, unclaim := range fullCert.Unclaims {
		key := bigIntKey(unclaim.GlobalIndex)
		if key == "" {
			continue
		}
		if existing, ok := unclaimMap[key]; !ok || unclaim.BlockNumber < existing {
			unclaimMap[key] = unclaim.BlockNumber
		}
	}

	for _, claim := range fullCert.Claims {
		if claim.BlockNum > limitedCert.ToBlock {
			break
		}

		isGERFinalized, err := f.getGERFinalizedStatus(cache, claim.GlobalExitRoot, fullCert.L1InfoTreeLeafCount)
		if err != nil {
			return fmt.Errorf("error checking if claim's GER %s is finalized: %w", claim.GlobalExitRoot.String(), err)
		}
		if isGERFinalized {
			continue
		}

		unclaimBlock, hasUnclaim := unclaimMap[bigIntKey(claim.GlobalIndex)]
		if !hasUnclaim || unclaimBlock <= limitedCert.ToBlock {
			continue
		}

		suggestion := fmt.Sprintf("increase %s so block %d fits in the same certificate as this claim", limiterName, unclaimBlock)
		if limiterName == "MaxL2BlockNumber" {
			suggestion = fmt.Sprintf("increase MaxL2BlockNumber to at least %d", unclaimBlock)
		}

		f.log.Warnf("%s prevents aggsender from including the unclaim that clears a blocking invalid claim. %s, required_unclaim_block=%d, full_cert_range=%d-%d, limited_cert_range=%d-%d. Suggested config change: %s.",
			limiterName, formatClaimForLogs(claim), unclaimBlock, fullCert.FromBlock, fullCert.ToBlock, limitedCert.FromBlock, limitedCert.ToBlock, suggestion)
		return nil
	}

	return nil
}

func formatClaimForLogs(claim bridgesync.Claim) string {
	amount := "nil"
	if claim.Amount != nil {
		amount = claim.Amount.String()
	}

	return fmt.Sprintf("claim_block=%d, global_index=%s, token=%s, amount=%s",
		claim.BlockNum, bigIntKey(claim.GlobalIndex), claim.OriginAddress.Hex(), amount)
}

func bigIntKey(value *big.Int) string {
	if value == nil {
		return "nil"
	}

	return value.String()
}

func logLimiterBlockedInvalidClaim(
	base types.AggsenderFlowBaser,
	fullCert *types.CertificateBuildParams,
	limitedCert *types.CertificateBuildParams,
	limiterName string,
) {
	concreteBaseFlow, ok := base.(*baseFlow)
	if !ok {
		return
	}

	if err := concreteBaseFlow.logLimiterBlockedInvalidClaim(fullCert, limitedCert, limiterName); err != nil {
		concreteBaseFlow.log.Warnf("unable to assess non-finalized claim after %s reduction: %v", limiterName, err)
	}
}
