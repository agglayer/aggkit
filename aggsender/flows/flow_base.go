package flows

import (
	"context"
	"errors"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
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
	lastSentCertificate *types.CertificateHeader) (types.BlockRange, int, error) {
	lastL2BlockSynced, err := f.l2BridgeQuerier.GetLastProcessedBlock(ctx)
	if err != nil {
		return types.BlockRangeZero, 0, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	previousToBlock, retryCount := f.getLastSentBlockAndRetryCount(lastSentCertificate)
	if previousToBlock >= lastL2BlockSynced {
		f.log.Infof("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return types.BlockRangeZero, 0, errNoNewBlocks
	}
	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced
	return types.NewBlockRange(fromBlock, toBlock), retryCount, nil
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
	currentCert := certParams
	var err error
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
	unclaimCnt := make(map[string]int)
	for _, u := range unclaims {
		// Adjust accessor as needed:
		//   - if GlobalIndex is uint64: key := strconv.FormatUint(u.GlobalIndex, 10)
		//   - if it's *big.Int:         key := u.GlobalIndex.String()
		//   - if it's a struct method:   key := u.GlobalIndex().String()
		key := u.GlobalIndex.String()
		unclaimCnt[key]++
	}

	// Build claim counts by GlobalIndex
	claimCnt := make(map[string]int)
	for _, c := range claims {
		key := c.GlobalIndex.String()
		claimCnt[key]++
	}

	// Compute how many claims should remain per index after cancelling unclaims
	remaining := make(map[string]int, len(claimCnt))
	for k, c := range claimCnt {
		u := unclaimCnt[k]
		if c > u {
			remaining[k] = c - u
		} else {
			remaining[k] = 0
		}
	}

	// Filter claims: keep in original order, but only as many as remaining[idx]
	filteredClaims := make([]bridgesync.Claim, 0, len(claims))
	for _, c := range claims {
		key := c.GlobalIndex.String()
		if remaining[key] > 0 {
			filteredClaims = append(filteredClaims, c)
			remaining[key]--
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

	nextBlockRange := types.NewBlockRange(newFromBlock, newToBlock)
	lastBlockRange := types.NewBlockRange(lastSettledFromBlock, lastSettledToBlock)

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
	for _, c := range certParams.Claims {
		isGERFinalized, err := f.l1InfoTreeDataQuerier.IsGERFinalized(
			c.GlobalExitRoot, certParams.L1InfoTreeLeafCount)
		if err != nil {
			return nil, fmt.Errorf("error checking if GER %s is finalized: %w", c.GlobalExitRoot.String(), err)
		}

		if !isGERFinalized {
			f.log.Warnf("found a non-finalized GER: %s on block: %d. "+
				"Certificate will be resized to exclude it and all blocks after it",
				c.GlobalExitRoot.String(), c.BlockNum)
			return certParams.AdjustToBlock(c.BlockNum - 1)
		}
	}

	return certParams, nil
}
