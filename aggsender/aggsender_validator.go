package aggsender

import (
	"context"
	"errors"
	"fmt"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrNotImplemented        = errors.New("aggsender-validator not implemented")
	ErrNilCertificate        = errors.New("aggsender-validator nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-validator metadata not compatible with the current version")
)

type L1InfoTreeRootByLeafQuerier interface {
	// GetL1InfoRootByLeafCount returns the L1 Info tree root for the given leaf count
	GetL1InfoRootByLeafCount(ctx context.Context, leafCount uint32) (*treetypes.Root, error)
}

type AggsenderValidator struct {
	log                   aggkitcommon.Logger
	l1InfoTreeDataQuerier L1InfoTreeRootByLeafQuerier
}

func NewAggsenderValidator(ctx context.Context,
	logger *log.Logger,
	cfg config.Config,
	l1InfoTreeSyncer *l1infotreesync.L1InfoTreeSync,
	l2Syncer types.L2BridgeSyncer,
	l1Client aggkittypes.BaseEthereumClienter,
	l2Client aggkittypes.BaseEthereumClienter,
	rollupDataQuerier types.RollupDataQuerier) (*AggsenderValidator, error) {
	return nil, nil
}
func (a *AggsenderValidator) Start(ctx context.Context) {
}

func (a *AggsenderValidator) GetRPCServices() []jRPC.Service {
	return []jRPC.Service{}
}

type VerifyIncommingRequests struct {
	certificate         *agglayertypes.Certificate
	previousCertificate *agglayertypes.CertificateHeader
}

func (a *AggsenderValidator) ValidateCertificate(params VerifyIncommingRequests) error {
	if params.certificate == nil {
		return ErrNilCertificate
	}
	// Between cert must be no gap because if there are could be a attack vector
	if err := a.CheckContigousCertificates(params); err != nil {
		return err
	}
	// Build corresponding certificate

	return ErrNotImplemented
}

func (a *AggsenderValidator) CheckContigousCertificates(params VerifyIncommingRequests) error {
	if params.certificate == nil {
		return ErrNilCertificate
	}
	if params.previousCertificate == nil {
		// TODO: Check that the first certificate start at the begining of genesis
		return a.CheckFirstCerficateBlocks(params)
	}
	// certificate != nil && previousCertificate != nil
	currentBlockRange, err := getBlockRangeFromMetadata(params.certificate.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get block range from certificate metadata: %w", err)
	}
	if currentBlockRange.IsEmpty() {
		return fmt.Errorf("certificate block range %s have no block! , certificate: %s",
			currentBlockRange.String(),
			params.certificate.ID())
	}
	previousBlockRange, err := getBlockRangeFromMetadata(params.previousCertificate.Metadata)
	if err != nil {
		return fmt.Errorf("failed to get block range from previous certificate metadata: %w", err)
	}
	if previousBlockRange.IsNextContigousBlock(currentBlockRange) {
		// No more check required is just the next one
		return nil
	}
	// TODO: Check logic for gaps (no data beetween certificates)
	return nil
}

// CheckFirstCerficateBlocks checks that the first certificate blocks are correct
// so it's starts from genesis?!?
func (a *AggsenderValidator) CheckFirstCerficateBlocks(params VerifyIncommingRequests) error {
	// TODO: fill it
	return ErrNotImplemented
}

func (a *AggsenderValidator) GetCertificatePreBuildParams(ctx context.Context, params VerifyIncommingRequests) (*types.CertificatePreBuildParams, error) {
	if params.certificate == nil {
		return nil, ErrNilCertificate
	}
	lastSentCertificate, err := validator.AgglayerCertificateHeaderToAggsender(params.previousCertificate)
	if err != nil {
		return nil, fmt.Errorf("failed to convert previous certificate to Aggsender format: %w", err)
	}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(params.certificate.Metadata)
	if err != nil {
		return nil, ErrMetadataNotCompatible
	}
	blockRange, err := metadataUnmarshal.BlockRange()
	if err != nil {
		return nil, fmt.Errorf("failed to get block range from certificate metadata: %w", err)
	}

	l1InfoRoot, err := a.l1InfoTreeDataQuerier.GetL1InfoRootByLeafCount(ctx, params.certificate.L1InfoTreeLeafCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get L1 Info tree root by leaf count %d: %w",
			params.certificate.L1InfoTreeLeafCount, err)
	}

	return &types.CertificatePreBuildParams{
		BlockRange:          blockRange,
		RetryCount:          0, // TODO: ???
		CertificateType:     guessCertificateType(params.certificate, metadataUnmarshal.CertificateType()),
		LastSentCertificate: lastSentCertificate,
		L1InfoTreeWhichToProve: &types.CertificateL1InfoTree{
			L1InfoTreeRootFromWhichToProve: l1InfoRoot.Hash,
			L1InfoTreeLeafCount:            params.certificate.L1InfoTreeLeafCount,
		},
	}, nil
}

// guessCertificateType tries to guess the certificate type based on the certificate and metadata.
func guessCertificateType(certificate *agglayertypes.Certificate, metadataCertType types.CertificateType) types.CertificateType {
	if metadataCertType != types.CertificateTypeUnknown {
		return metadataCertType
	}
	// Metadata doesn't have the cert type,  I will try to guess from the certificate
	// TODO: Double check this logic... what about optimistic, PP have something in this field
	proof, err := certificate.AggchainData.MarshalJSON()
	if err != nil {
		return types.CertificateTypePP
	}
	if len(proof) > 0 {
		return types.CertificateTypeFEP
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
