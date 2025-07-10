package validator

import (
	"context"
	"fmt"
	"strings"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

type FlowInterface interface {
	GenerateBuildParams(ctx context.Context,
		preParams *types.CertificatePreBuildParams) (*types.CertificateBuildParams, error)
	BuildCertificate(ctx context.Context,
		buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error)
}

type L1InfoTreeRootByLeafQuerier interface {
	// GetL1InfoRootByLeafIndex returns the L1 Info tree root for the given leaf index
	GetL1InfoRootByLeafIndex(ctx context.Context, leafCount uint32) (*treetypes.Root, error)
}

// CertificateValidator is a object to validate a certificate
type CertificateValidator struct {
	log                   aggkitcommon.Logger
	flowPP                FlowInterface
	l1InfoTreeDataQuerier L1InfoTreeRootByLeafQuerier
}

func NewAggsenderValidator(logger aggkitcommon.Logger,
	flowPP FlowInterface,
	l1InfoTreeDataQuerier L1InfoTreeRootByLeafQuerier) *CertificateValidator {
	return &CertificateValidator{
		log:                   logger,
		flowPP:                flowPP,
		l1InfoTreeDataQuerier: l1InfoTreeDataQuerier,
	}
}

type VerifyIncommingRequests = types.VerifyIncomingRequest

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *CertificateValidator) ValidateCertificate(ctx context.Context, params VerifyIncommingRequests) error {
	if params.Certificate == nil {
		return ErrNilCertificate
	}
	// If metadata is not lastest version when is generated again is always differ
	// metadata field
	if err := a.checkMetadataCompatibility(params); err != nil {
		return fmt.Errorf("failed CheckMetadataCompatibility: %w", err)
	}
	// Between cert must be no gap because if there are could be a attack vector
	if err := a.checkContigousCertificates(params); err != nil {
		return fmt.Errorf("failed CheckContigousCertificates: %w", err)
	}

	if err := a.checkCertificatesContents(params); err != nil {
		return fmt.Errorf("failed CheckCertificatesContents: %w", err)
	}
	// Build corresponding certificate
	preBuildParams, err := a.getCertificatePreBuildParams(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to get certificate pre-build params: %w", err)
	}
	a.log.Debugf("aggsender-validator: preBuild: %s", preBuildParams.String())
	buildParams, err := a.flowPP.GenerateBuildParams(ctx, preBuildParams)
	if err != nil {
		return fmt.Errorf("failed flow.GenerateBuildParams: %w", err)
	}
	certificate, err := a.flowPP.BuildCertificate(ctx, buildParams)
	if err != nil {
		return fmt.Errorf("failed flow.BuildCertificate: %w", err)
	}
	err = a.compareCertificates(params.Certificate, certificate)
	if err != nil {
		return fmt.Errorf("certificate not equal to expected: %w", err)
	}
	return nil
}

func (a *CertificateValidator) checkCertificatesContents(params VerifyIncommingRequests) error {
	if params.PreviousCertificate != nil {
		if !params.PreviousCertificate.Status.IsSettled() {
			return fmt.Errorf("previous certificate %s is not settled (status:%s), can't be used to validate certificate %s",
				params.PreviousCertificate.ID(), params.PreviousCertificate.Status.String(), params.Certificate.ID())
		}
	}
	return nil
}

// checkContigousCertificates checks if the incoming certificate is contiguous with the previous one.
func (a *CertificateValidator) checkContigousCertificates(params VerifyIncommingRequests) error {
	if params.Certificate == nil {
		return ErrNilCertificate
	}
	if params.PreviousCertificate == nil {
		return a.checkFirstCerficateBlocks(params)
	}
	if params.PreviousCertificate.Height+1 != params.Certificate.Height {
		return fmt.Errorf("certificate height not contigous, expected: %d, got: %d",
			params.PreviousCertificate.Height+1, params.Certificate.Height)
	}
	// certificate != nil && previousCertificate != nil
	currentBlockRange, err := getBlockRangeFromMetadata(params.Certificate.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get block range from certificate metadata: %w", err)
	}
	if currentBlockRange.IsEmpty() {
		return fmt.Errorf("certificate block range %s have no block! , certificate: %s",
			currentBlockRange.String(),
			params.Certificate.ID())
	}
	previousBlockRange, err := getBlockRangeFromMetadata(params.PreviousCertificate.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get block range from previous certificate metadata: %w", err)
	}
	if previousBlockRange.IsNextContigousBlock(currentBlockRange) {
		// No more check required is just the next one
		return nil
	}
	return fmt.Errorf("certificate block range %s is not contiguous with previous certificate block range %s, "+
		"certificate: %s, previous certificate: %s",
		currentBlockRange.String(),
		previousBlockRange.String(),
		params.Certificate.ID(),
		params.PreviousCertificate.ID())
}

func (a *CertificateValidator) checkMetadataCompatibility(params VerifyIncommingRequests) error {
	if params.Certificate == nil {
		return nil
	}
	// Check if metadata is compatible with the current version
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(params.Certificate.Metadata)
	if err != nil {
		return fmt.Errorf("error unmarshalling certificate metadata: %w. Err: %w", err, ErrMetadataNotCompatible)
	}
	if metadataUnmarshal.Version != types.LatestCertificateMetadataVersion {
		return fmt.Errorf("certificate metadata version is not latest, expected: %d, got: %d."+
			"Can't generate a certificate if metadata version is not latest because the field."+
			" will differ. Err: %w",
			types.LatestCertificateMetadataVersion, metadataUnmarshal.Version, ErrMetadataNotCompatible)
	}
	return nil
}

// compareCertificates compares the incoming certificate with the one generated.
func (a *CertificateValidator) compareCertificates(
	incomingCertificate *agglayertypes.Certificate,
	localCertificate *agglayertypes.Certificate) error {
	if incomingCertificate == nil || localCertificate == nil {
		return fmt.Errorf("one of the certificates is nil, incoming: %v, local: %v",
			incomingCertificate, localCertificate)
	}
	diffs := DiffsCertificate(incomingCertificate, localCertificate)
	diffStr := strings.Join(diffs, "\n")
	// This is redudant, but just in case
	if incomingCertificate.Hash() != localCertificate.Hash() {
		return fmt.Errorf("certificates hash mismatch, incoming: %s, local: %s.\n FullDiff: %s",
			incomingCertificate.Hash().Hex(), localCertificate.Hash().Hex(), diffStr)
	}
	if incomingCertificate.Metadata != localCertificate.Metadata {
		return fmt.Errorf("certificates metadata mismatch, incoming: %s, local: %s.\n FullDiff: %s",
			incomingCertificate.Metadata.Hex(), localCertificate.Metadata.Hex(), diffStr)
	}
	if len(diffs) > 0 {
		return fmt.Errorf("certificates mismatch. FullDiff: %s",
			diffStr)
	}
	return nil
}

// checkFirstCerficateBlocks checks that the first certificate blocks are correct
// so it's starts from genesis?!?
func (a *CertificateValidator) checkFirstCerficateBlocks(params VerifyIncommingRequests) error {
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(params.Certificate.Metadata)
	if err != nil {
		return fmt.Errorf("error checking first certificate because can't unmarshal metadata. Err: %w", err)
	}
	if metadataUnmarshal.FromBlock != 1 {
		// The first certificate must start from block 0
		return fmt.Errorf("first certificate must start from block 1, but got: %d",
			metadataUnmarshal.FromBlock)
	}
	if params.Certificate.Height != 0 {
		// The first certificate must have height 0
		return fmt.Errorf("first certificate must have height 0, but got: %d",
			params.Certificate.Height)
	}
	return nil
}

// getCertificatePreBuildParams prepares the parameters needed to build a certificate based
// on incomming certificate
func (a *CertificateValidator) getCertificatePreBuildParams(ctx context.Context,
	params VerifyIncommingRequests) (*types.CertificatePreBuildParams, error) {
	if params.Certificate == nil {
		return nil, fmt.Errorf("preBuildParams. Err: %w", ErrNilCertificate)
	}
	lastSentCertificate, err := AgglayerCertificateHeaderToAggsender(params.PreviousCertificate)
	if err != nil {
		return nil, fmt.Errorf("preBuildParams. failed to convert previous certificate to Aggsender format: %w", err)
	}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(params.Certificate.Metadata)
	if err != nil {
		return nil, fmt.Errorf("preBuildParams. Error unmarshal cert.metadata. Err: %w", err)
	}
	blockRange, err := metadataUnmarshal.BlockRange()
	if err != nil {
		return nil, fmt.Errorf("preBuildParams. failed to get block range from certificate metadata: %w", err)
	}

	l1InfoRoot, err := a.l1InfoTreeDataQuerier.GetL1InfoRootByLeafIndex(ctx, params.Certificate.L1InfoTreeLeafCount-1)
	if err != nil {
		return nil, fmt.Errorf("preBuildParams. failed to get L1 Info tree root by leaf count %d: %w",
			params.Certificate.L1InfoTreeLeafCount, err)
	}

	return &types.CertificatePreBuildParams{
		BlockRange:          blockRange,
		RetryCount:          0, // TODO: ???
		CertificateType:     guessCertificateType(params.Certificate, metadataUnmarshal.CertificateType()),
		LastSentCertificate: lastSentCertificate,
		L1InfoTreeWhichToProve: &types.CertificateL1InfoTree{
			L1InfoTreeRootFromWhichToProve: l1InfoRoot.Hash,
			L1InfoTreeLeafCount:            params.Certificate.L1InfoTreeLeafCount,
		},
		CreatedAt: metadataUnmarshal.CreatedAt,
	}, nil
}

// guessCertificateType tries to guess the certificate type based on the certificate and metadata.
func guessCertificateType(certificate *agglayertypes.Certificate,
	metadataCertType types.CertificateType) types.CertificateType {
	if metadataCertType != types.CertificateTypeUnknown {
		return metadataCertType
	}
	// Metadata doesn't have the cert type,  I will try to guess from the certificate
	// TODO: Double check this logic... what about optimistic, PP have something in this field
	if certificate.AggchainData != nil {
		proof, err := certificate.AggchainData.MarshalJSON()
		if err != nil {
			return types.CertificateTypePP
		}
		if len(proof) > 0 {
			return types.CertificateTypeFEP
		}
	}
	return types.CertificateTypePP
}

func getBlockRangeFromMetadata(metadata common.Hash) (types.BlockRange, error) {
	emptyBlockRange := types.BlockRange{}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(metadata)
	if err != nil {
		return emptyBlockRange, ErrMetadataNotCompatible
	}
	return metadataUnmarshal.BlockRange()
}
