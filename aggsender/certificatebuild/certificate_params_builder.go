package certificatebuild

import (
	"context"
	"errors"
	"fmt"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	errNoBridgesAndClaims = errors.New("no bridges and claims to build a certificate")
	ErrNoNewBlocks        = errors.New("no new blocks to send a certificate")

	ZeroLER = common.HexToHash("0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757")
)

// CertificateBuilderConfig is a struct that holds the configuration for the certificate builder
type CertificateBuilderConfig struct {
	// MaxCertSize is the maximum size of the certificate in bytes. 0 means no limit
	MaxCertSize uint
	// StartL2Block is the L2 block number from which to start sending certificates.
	// It is used to determine the first block to include in the certificate.
	// It can be 0
	StartL2Block uint64
}

// NewCertificateBuilderConfigDefault returns a CertificateBuilderConfig with default values
func NewCertificateBuilderConfigDefault() CertificateBuilderConfig {
	return CertificateBuilderConfig{
		MaxCertSize:  0, // 0 means no limit
		StartL2Block: 0, // 0 means start from the first block
	}
}

// NewCertificateBuilderConfig returns a CertificateBuilderConfig with the specified maxCertSize and startL2Block
func NewCertificateBuilderConfig(maxCertSize uint, startL2Block uint64) CertificateBuilderConfig {
	return CertificateBuilderConfig{
		MaxCertSize:  maxCertSize,
		StartL2Block: startL2Block,
	}
}

var _ types.CertificateBuilder = (*CertificateBuilder)(nil)

// CertificateBuilder is responsible for constructing certificate-related data and operations.
// It manages dependencies such as logging, storage, bridge querying, and data conversion
// for bridge exits and imported bridge exits. The builder is configured via
// CertificateBuilderConfig to customize its behavior.
type CertificateBuilder struct {
	log                          types.Logger
	storage                      db.AggSenderStorage
	l2BridgeQuerier              types.BridgeQuerier
	bridgeExitsConverter         *converters.BridgeExitConverter
	importedBridgeExitsConverter *converters.ImportedBridgeExitConverter

	cfg CertificateBuilderConfig
}

// NewCertificateBuilder creates a new instance of CertificateBuilder.
func NewCertificateBuilder(
	log types.Logger,
	storage db.AggSenderStorage,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
	l2BridgeQuerier types.BridgeQuerier,
	cfg CertificateBuilderConfig,
) *CertificateBuilder {
	return &CertificateBuilder{
		log:                  log,
		cfg:                  cfg,
		storage:              storage,
		l2BridgeQuerier:      l2BridgeQuerier,
		bridgeExitsConverter: converters.NewBridgeExitConverter(),
		importedBridgeExitsConverter: converters.NewImportedBridgeExitConverter(
			log,
			l1InfoTreeDataQuerier,
		),
	}
}

// GetImportedBridgeExitsConverter returns the ImportedBridgeExitConverter associated with the CertificateBuilder.
// This converter is used to handle the conversion of imported bridge exit data within the certificate context.
func (b *CertificateBuilder) GetImportedBridgeExitsConverter() *converters.ImportedBridgeExitConverter {
	return b.importedBridgeExitsConverter
}

// GetCertificateBuildParams constructs and returns the parameters required to build a certificate.
// It queries the latest processed L2 block and the last sent certificate header, determines the block range
// for the new certificate, and fetches the relevant bridges and claims data. The function also handles
// certificate size limitations and retry logic.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - allowEmptyCert: If true, allows building a certificate even if there are no new claims or bridges.
//   - certType: The type of certificate to build.
//
// Returns:
//   - *types.CertificateBuildParams: The parameters for building the certificate.
//   - error: An error if any step fails, or if there are no new blocks to include in the certificate.
func (b *CertificateBuilder) GetCertificateBuildParams(
	ctx context.Context,
	allowEmptyCert bool,
	certType types.CertificateType,
) (*types.CertificateBuildParams, error) {
	lastL2BlockSynced, err := b.l2BridgeQuerier.GetLastProcessedBlock(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting last processed block from l2: %w", err)
	}

	lastSentCertificate, err := b.storage.GetLastSentCertificateHeader()
	if err != nil {
		return nil, err
	}

	previousToBlock, retryCount := b.getLastSentBlockAndRetryCount(lastSentCertificate)

	if previousToBlock >= lastL2BlockSynced {
		b.log.Warnf("no new blocks to send a certificate, last certificate block: %d, last L2 block: %d",
			previousToBlock, lastL2BlockSynced)
		return nil, ErrNoNewBlocks
	}

	fromBlock := previousToBlock + 1
	toBlock := lastL2BlockSynced

	bridges, claims, err := b.l2BridgeQuerier.GetBridgesAndClaims(ctx, fromBlock, toBlock, allowEmptyCert)
	if err != nil {
		return nil, err
	}

	buildParams := &types.CertificateBuildParams{
		FromBlock:           fromBlock,
		ToBlock:             toBlock,
		RetryCount:          retryCount,
		LastSentCertificate: lastSentCertificate,
		Bridges:             bridges,
		Claims:              claims,
		CreatedAt:           uint32(time.Now().UTC().Unix()),
		CertificateType:     certType,
	}

	buildParams, err = b.limitCertSize(buildParams, allowEmptyCert)
	if err != nil {
		return nil, fmt.Errorf("error limitCertSize: %w", err)
	}

	return buildParams, nil
}

// BuildCertificate constructs a new agglayertypes.Certificate based on the provided certificate parameters.
// It processes bridge exits, imported bridge exits, and computes the next certificate height and local exit roots.
// If allowEmptyCert is false and the certificate parameters are empty, it returns an error.
// Returns the constructed Certificate or an error if any step in the process fails.
//
// Parameters:
//   - ctx: Context for controlling cancellation and deadlines.
//   - certParams: Parameters required to build the certificate.
//   - lastSentCertificate: The last certificate that was sent,
//     used to determine the next height and previous local exit root.
//   - allowEmptyCert: If false, the function returns an error when certParams is empty.
//
// Returns:
//   - *agglayertypes.Certificate: The newly built certificate.
//   - error: An error if certificate construction fails at any step.
func (b *CertificateBuilder) BuildCertificate(ctx context.Context,
	certParams *types.CertificateBuildParams,
	lastSentCertificate *types.CertificateHeader,
	allowEmptyCert bool) (*agglayertypes.Certificate, error) {
	b.log.Infof("building certificate for %s estimatedSize=%d", certParams.String(), certParams.EstimatedSize())

	if !allowEmptyCert && certParams.IsEmpty() {
		return nil, errNoBridgesAndClaims
	}

	bridgeExits := b.bridgeExitsConverter.ConvertToBridgeExits(certParams.Bridges)
	importedBridgeExits, err := b.importedBridgeExitsConverter.ConvertToImportedBridgeExits(
		ctx,
		certParams.Claims,
		certParams.L1InfoTreeRootFromWhichToProve,
	)
	if err != nil {
		return nil, fmt.Errorf("error getting imported bridge exits: %w", err)
	}

	height, previousLER, err := b.getNextHeightAndPreviousLER(lastSentCertificate)
	if err != nil {
		return nil, fmt.Errorf("error getting next height and previous LER: %w", err)
	}

	newLER, err := b.getNewLocalExitRoot(ctx, certParams, previousLER)
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
		NetworkID:           b.l2BridgeQuerier.OriginNetwork(),
		PrevLocalExitRoot:   previousLER,
		NewLocalExitRoot:    newLER,
		BridgeExits:         bridgeExits,
		ImportedBridgeExits: importedBridgeExits,
		Height:              height,
		Metadata:            meta.ToHash(),
		L1InfoTreeLeafCount: certParams.L1InfoTreeLeafCount,
	}, nil
}

// limitCertSize limits certificate size based on the max size configuration parameter
// size is expressed in bytes
func (b *CertificateBuilder) limitCertSize(
	fullCert *types.CertificateBuildParams, allowEmptyCert bool) (*types.CertificateBuildParams, error) {
	currentCert := fullCert
	var err error
	maxCertSize := b.cfg.MaxCertSize
	for {
		if currentCert.NumberOfBridges() == 0 && !allowEmptyCert {
			return nil, fmt.Errorf("error on reducing the certificate size. "+
				"No bridge exits found in range from: %d, to: %d and empty certificate is not allowed",
				currentCert.FromBlock, currentCert.ToBlock)
		}

		if maxCertSize == 0 || currentCert.EstimatedSize() <= maxCertSize {
			return currentCert, nil
		}

		if currentCert.NumberOfBlocks() <= 1 {
			b.log.Warnf("Minimum number of blocks reached [%d to %d]. Estimated size: %d > max size: %d",
				currentCert.FromBlock, currentCert.ToBlock, currentCert.EstimatedSize(), maxCertSize)
			return currentCert, nil
		}

		currentCert, err = currentCert.Range(currentCert.FromBlock, currentCert.ToBlock-1)
		if err != nil {
			return nil, fmt.Errorf("error reducing certificate: %w", err)
		}
	}
}

// getLastSentBlockAndRetryCount returns the last sent block of the last sent certificate
// if there is no previosly sent certificate, it returns startL2Block and 0
func (b *CertificateBuilder) getLastSentBlockAndRetryCount(
	lastSentCertificateInfo *types.CertificateHeader) (uint64, int) {
	if lastSentCertificateInfo == nil {
		// this is the first certificate so we start from what we have set in start L2 block
		return b.cfg.StartL2Block, 0
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

// getNextHeightAndPreviousLER returns the height and previous LER for the new certificate
func (b *CertificateBuilder) getNextHeightAndPreviousLER(
	lastSentCertificateInfo *types.CertificateHeader) (uint64, common.Hash, error) {
	if lastSentCertificateInfo == nil {
		return 0, ZeroLER, nil
	}
	if !lastSentCertificateInfo.Status.IsClosed() {
		return 0, ZeroLER, fmt.Errorf("last certificate %s is not closed (status: %s)",
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
			return 0, ZeroLER, nil
		}
		// We get previous certificate that must be settled
		b.log.Debugf("last certificate %s is in error, getting previous settled certificate height:%d",
			lastSentCertificateInfo.Height-1)
		lastSettleCert, err := b.storage.GetCertificateHeaderByHeight(lastSentCertificateInfo.Height - 1)
		if err != nil {
			return 0, common.Hash{}, fmt.Errorf("error getting last settled certificate: %w", err)
		}
		if lastSettleCert == nil {
			return 0, common.Hash{}, fmt.Errorf("none settled certificate: %w", err)
		}
		if !lastSettleCert.Status.IsSettled() {
			return 0, common.Hash{}, fmt.Errorf("last settled certificate %s is not settled (status: %s)",
				lastSettleCert.ID(), lastSettleCert.Status.String())
		}

		return lastSentCertificateInfo.Height, lastSettleCert.NewLocalExitRoot, nil
	}
	return 0, ZeroLER, fmt.Errorf("last certificate %s has an unknown status: %s",
		lastSentCertificateInfo.ID(), lastSentCertificateInfo.Status.String())
}

// getNewLocalExitRoot gets the new local exit root for the certificate
func (b *CertificateBuilder) getNewLocalExitRoot(
	ctx context.Context,
	certParams *types.CertificateBuildParams,
	previousLER common.Hash) (common.Hash, error) {
	if certParams.NumberOfBridges() == 0 {
		// if there is no bridge exits we return the previous LER
		// since there was no change in the local exit root
		return previousLER, nil
	}

	depositCount := certParams.MaxDepositCount()

	exitRoot, err := b.l2BridgeQuerier.GetExitRootByIndex(ctx, depositCount)
	if err != nil {
		return common.Hash{}, fmt.Errorf("error getting exit root by index: %d. Error: %w", depositCount, err)
	}

	return exitRoot, nil
}

// GetNewLocalExitRoot gets the new local exit root for the certificate
func (b *CertificateBuilder) GetNewLocalExitRoot(ctx context.Context,
	certParams *types.CertificateBuildParams) (common.Hash, error) {
	if certParams == nil {
		return common.Hash{}, fmt.Errorf("baseFlow.GetNewLocalExitRoot. certificate build parameters cannot be nil")
	}
	_, previousLER, err := b.getNextHeightAndPreviousLER(certParams.LastSentCertificate)
	if err != nil {
		return common.Hash{}, fmt.Errorf("baseFlow.GetNewLocalExitRoot. error getting next height and previous LER: %w", err)
	}

	newLER, err := b.getNewLocalExitRoot(ctx, certParams, previousLER)
	if err != nil {
		return common.Hash{}, fmt.Errorf("baseFlow.GetNewLocalExitRoot. error getting new local exit root: %w", err)
	}
	return newLER, nil
}
