package certificatebuild

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

var (
	errNoBridgesAndClaims = errors.New("no bridges and claims to build certificate")
	errNoNewBlocks        = errors.New("no new blocks to send a certificate")

	emptyLER = common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
)

var timeNowFunc atomic.Value

func init() {
	timeNowFunc.Store(TimeNowUTC)
}

// TimeNow returns the current time as a uint32 timestamp (thread-safe).
func TimeNow() uint32 {
	return timeNowFunc.Load().(func() uint32)() //nolint:forcetypeassert
}

func SetTimeNowFunc(f func() uint32) {
	timeNowFunc.Store(f)
}

// TimeNowUTC returns the current time in UTC as a uint32 timestamp.
func TimeNowUTC() uint32 {
	// Use a more precise time function to avoid collisions in tests
	// and ensure that the time is always in UTC.
	return uint32(time.Now().UTC().Unix())
}

// CommonBuildConfig is a struct that holds the configuration for the certificate building process
type CommonBuildConfig struct {
	// MaxCertSize is the maximum size of the certificate in bytes. 0 means no limit
	MaxCertSize uint
	// StartL2Block is the L2 block number from which to start sending certificates.
	// It is used to determine the first block to include in the certificate.
	// It can be 0
	StartL2Block uint64
	// RequireNoFEPBlockGap indicates whether the certificate building process requires no gap between the
	// first FEP block and last settled certificate.
	RequireNoFEPBlockGap bool
}

// NewCommonBuildConfigDefault returns a CommonBuildConfig with default values
func NewCommonBuildConfigDefault() CommonBuildConfig {
	return CommonBuildConfig{
		MaxCertSize:          0, // 0 means no limit
		StartL2Block:         0, // 0 means start from the first block
		RequireNoFEPBlockGap: false,
	}
}

// NewCommonBuildConfig returns a CommonBuildConfig with the specified maxCertSize, startL2Block,
// and requireNoFEPBlockGap values
func NewCommonBuildConfig(
	maxCertSize uint,
	startL2Block uint64,
	requireNoFEPBlockGap bool) CommonBuildConfig {
	return CommonBuildConfig{
		MaxCertSize:          maxCertSize,
		StartL2Block:         startL2Block,
		RequireNoFEPBlockGap: requireNoFEPBlockGap,
	}
}

var _ types.CommonCertParamsBuilder = (*commonParamsBuilder)(nil)

// commonParamsBuilder is responsible for constructing common certificate-related data and operations.
// It manages dependencies such as logging, storage, bridge querying, and data conversion
// for bridge exits and imported bridge exits. The builder is configured via
// CommonParamsBuilderConfig to customize its behavior.
type commonParamsBuilder struct {
	log               types.Logger
	storage           db.AggSenderStorage
	l2BridgeQuerier   types.BridgeQuerier
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier
	lerQuerier        types.LERQuerier

	cfg CommonBuildConfig
}

// NewCommonParamsBuilder creates a new instance of CommonParamsBuilder.
func NewCommonParamsBuilder(
	log types.Logger,
	storage db.AggSenderStorage,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	lerQuerier types.LERQuerier,
	cfg CommonBuildConfig,
) types.CommonCertParamsBuilder {
	return &commonParamsBuilder{
		log:               log,
		cfg:               cfg,
		storage:           storage,
		lerQuerier:        lerQuerier,
		l2BridgeQuerier:   l2BridgeQuerier,
		l1InfoTreeQuerier: l1InfoTreeDataQuerier,
	}
}

// GeneratePreBuildParams prepares the parameters required before building a certificate.
// It retrieves the last sent certificate header, determines the next block range for the certificate,
// fetches the latest finalized L1 info root, and assembles all necessary data into a CertificatePreBuildParams struct.
// Returns the constructed CertificatePreBuildParams and an error if any step fails.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - certType: The type of certificate to be built.
//
// Returns:
//   - *types.CertificatePreBuildParams: The parameters needed for certificate pre-building.
//   - error: Non-nil if any retrieval or computation fails.
func (c *commonParamsBuilder) GeneratePreBuildParams(ctx context.Context,
	certType types.CertificateType) (*types.CertificatePreBuildParams, error) {
	lastSentCertificate, err := c.storage.GetLastSentCertificateHeader()
	if err != nil {
		return nil, fmt.Errorf("error getting last sent certificate: %w", err)
	}

	nextBlocks, retryCount, err := c.nextCertificateBlockRange(ctx, lastSentCertificate)
	if err != nil {
		return nil, fmt.Errorf("error getting next certificate block range: %w", err)
	}
	l1InfoRoot, _, err := c.l1InfoTreeQuerier.GetLatestFinalizedL1InfoRoot(ctx)
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
		CreatedAt: TimeNow(),
	}, nil
}

// GenerateBuildParams constructs a CertificateBuildParams object based on the provided
// CertificatePreBuildParams. It validates the input parameters, retrieves bridge and claim
// data for the specified block range using the l2BridgeQuerier, and populates the build
// parameters accordingly. Returns an error if required parameters are missing or if data
// retrieval fails.
//
// Parameters:
//   - ctx: context.Context for controlling cancellation and deadlines.
//   - preParams: types.CertificatePreBuildParams containing the necessary input data.
//
// Returns:
//   - *types.CertificateBuildParams: The populated build parameters for certificate creation.
//   - error: An error if input validation fails or bridge/claim retrieval encounters an issue.
func (c *commonParamsBuilder) GenerateBuildParams(ctx context.Context,
	preParams types.CertificatePreBuildParams) (*types.CertificateBuildParams, error) {
	if preParams.L1InfoTreeToProve == nil {
		return nil, fmt.Errorf("L1InfoTreeWhichToProve should be not nil for GenerateBuildParams")
	}

	bridges, claims, err := c.l2BridgeQuerier.GetBridgesAndClaims(ctx,
		preParams.BlockRange.FromBlock, preParams.BlockRange.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("generateBulidParams fails getting bridges and claims. Err: %w", err)
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
	}

	return buildParams, nil
}

// GetCommonCertificateBuildParams returns the common parameters to build a certificate
func (c *commonParamsBuilder) GetCommonCertificateBuildParams(
	ctx context.Context, certType types.CertificateType) (*types.CertificateBuildParams, error) {
	preParams, err := c.GeneratePreBuildParams(ctx, certType)
	if err != nil {
		return nil, fmt.Errorf("error generating pre build params: %w", err)
	}

	params, err := c.GenerateBuildParams(ctx, *preParams)
	if err != nil {
		return nil, fmt.Errorf("error generating build params: %w", err)
	}

	params, err = c.LimitCertSize(params)
	if err != nil {
		return nil, fmt.Errorf("error applying limit size: %w", err)
	}

	return params, nil
}

// LimitCertSize limits certificate size based on the max size configuration parameter
// size is expressed in bytes
func (c *commonParamsBuilder) LimitCertSize(
	certParams *types.CertificateBuildParams) (*types.CertificateBuildParams, error) {
	currentCert := certParams
	var err error
	maxCertSize := c.cfg.MaxCertSize
	for {
		if maxCertSize == 0 || currentCert.EstimatedSize() <= maxCertSize {
			return currentCert, nil
		}

		if currentCert.NumberOfBlocks() <= 1 {
			c.log.Warnf("Minimum number of blocks reached [%d to %d]. Estimated size: %d > max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), maxCertSize)
			return currentCert, nil
		}

		currentCert, err = currentCert.Range(currentCert.FromBlock, currentCert.ToBlock-1)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}
	}
}

// BuildCertificate constructs a new agglayertypes.Certificate based on the provided CertificateBuildParams,
// the last sent certificate, and a flag indicating whether empty certificates are allowed.
// It performs the following steps:
//   - Logs the certificate build attempt with relevant parameters.
//   - Validates that the certificate is not empty if allowEmptyCert is false.
//   - Converts bridge and claim data into bridge exits and imported bridge exits.
//   - Determines the next certificate height and previous local exit root (LER).
//   - Computes the new local exit root based on the provided parameters.
//   - Assembles certificate metadata and returns the constructed certificate.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - certParams: Parameters required to build the certificate.
//   - lastSentCertificate: The most recently sent certificate, used to determine the next height and previous LER.
//   - allowEmptyCert: If false, prevents building a certificate with no bridges or claims.
//
// Returns:
//   - Pointer to the constructed agglayertypes.Certificate on success.
//   - An error if any step in the process fails.
func (c *commonParamsBuilder) BuildCertificate(ctx context.Context,
	certParams *types.CertificateBuildParams,
	lastSentCertificate *types.CertificateHeader,
	allowEmptyCert bool) (*agglayertypes.Certificate, error) {
	c.log.Infof("building certificate for %s estimatedSize=%d", certParams.String(), certParams.EstimatedSize())

	if !allowEmptyCert && certParams.IsEmpty() {
		return nil, errNoBridgesAndClaims
	}

	bridgeExits := converters.ConvertToBridgeExits(certParams.Bridges)
	importedBridgeExits, err := converters.ConvertToImportedBridgeExits(
		ctx, certParams.Claims, certParams.L1InfoTreeRootFromWhichToProve, c.l1InfoTreeQuerier)
	if err != nil {
		return nil, fmt.Errorf("error getting imported bridge exits: %w", err)
	}

	height, previousLER, err := c.getNextHeightAndPreviousLER(lastSentCertificate)
	if err != nil {
		return nil, fmt.Errorf("error getting next height and previous LER: %w", err)
	}

	newLER, err := c.getNewLocalExitRootForParams(ctx, certParams, previousLER)
	if err != nil {
		return nil, fmt.Errorf("error getting new local exit root: %w", err)
	}

	meta := types.NewCertificateMetadata(
		certParams.FromBlock,
		uint32(certParams.ToBlock-certParams.FromBlock),
		certParams.CreatedAt,
		certParams.CertificateType.ToInt(),
	)

	return &agglayertypes.Certificate{
		NetworkID:           c.l2BridgeQuerier.OriginNetwork(),
		PrevLocalExitRoot:   previousLER,
		NewLocalExitRoot:    newLER,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		Height:              height,
		Metadata:            meta.ToHash(),
		L1InfoTreeLeafCount: certParams.L1InfoTreeLeafCount,
	}, nil
}

// nextCertificateBlockRange returns the block range and retryCount for the next certificate
func (c *commonParamsBuilder) nextCertificateBlockRange(ctx context.Context,
	lastSentCertificate *types.CertificateHeader) (types.BlockRange, int, error) {
	lastL2BlockSynced, err := c.l2BridgeQuerier.GetLastProcessedBlock(ctx)
	if err != nil {
		return types.BlockRangeZero, 0, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	previousToBlock, retryCount := c.getLastSentBlockAndRetryCount(lastSentCertificate)
	if previousToBlock >= lastL2BlockSynced {
		c.log.Warnf("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return types.BlockRangeZero, 0, errNoNewBlocks
	}

	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced

	return types.NewBlockRange(fromBlock, toBlock), retryCount, nil
}

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previously sent certificate, it returns startL2Block and 0
func (c *commonParamsBuilder) getLastSentBlockAndRetryCount(
	lastSentCertificateInfo *types.CertificateHeader) (uint64, int) {
	if lastSentCertificateInfo == nil {
		// this is the first certificate so we start from what we have set in start L2 block
		return c.cfg.StartL2Block, 0
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

// GetNewLocalExitRootForCert gets the new local exit root for the new certificate
func (c *commonParamsBuilder) GetNewLocalExitRootForCert(ctx context.Context,
	certParams *types.CertificateBuildParams) (common.Hash, error) {
	if certParams == nil {
		return common.Hash{},
			fmt.Errorf("commonParamsBuilder.GetNewLocalExitRoot. certificate build parameters cannot be nil")
	}

	_, previousLER, err := c.getNextHeightAndPreviousLER(certParams.LastSentCertificate)
	if err != nil {
		return common.Hash{},
			fmt.Errorf("commonParamsBuilder.GetNewLocalExitRoot. error getting next height and previous LER: %w", err)
	}

	newLER, err := c.getNewLocalExitRootForParams(ctx, certParams, previousLER)
	if err != nil {
		return common.Hash{},
			fmt.Errorf("commonParamsBuilder.GetNewLocalExitRoot. error getting new local exit root: %w", err)
	}

	return newLER, nil
}

// getNewLocalExitRootForParams gets the new local exit root for the new certificate build params
func (c *commonParamsBuilder) getNewLocalExitRootForParams(
	ctx context.Context,
	certParams *types.CertificateBuildParams,
	previousLER common.Hash) (common.Hash, error) {
	if certParams.NumberOfBridges() == 0 {
		// if there is no bridge exits we return the previous LER
		// since there was no change in the local exit root
		return previousLER, nil
	}

	depositCount := certParams.MaxDepositCount()

	exitRoot, err := c.l2BridgeQuerier.GetExitRootByIndex(ctx, depositCount)
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting exit root by index: %d. Error: %w", depositCount, err)
	}

	return exitRoot, nil
}

// getNextHeightAndPreviousLER returns the height and previous LER for the new certificate
func (c *commonParamsBuilder) getNextHeightAndPreviousLER(
	lastSentCertificateInfo *types.CertificateHeader) (uint64, common.Hash, error) {
	if lastSentCertificateInfo == nil {
		ler, err := c.getStartLER()
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
			ler, err := c.getStartLER()
			return uint64(0), ler, err
		}
		// We get previous certificate that must be settled
		c.log.Debugf("last certificate %s is in error, getting previous settled certificate height:%d",
			lastSentCertificateInfo.Height-1)
		lastSettleCert, err := c.storage.GetCertificateHeaderByHeight(lastSentCertificateInfo.Height - 1)
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

// getStartLER returns the last local exit root (LER) based on the configuration
func (c *commonParamsBuilder) getStartLER() (common.Hash, error) {
	ler, err := c.lerQuerier.GetLastLocalExitRoot()
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting last local exit root: %w", err)
	}

	if ler == aggkitcommon.ZeroHash {
		return emptyLER, nil
	}

	return ler, nil
}
