package aggsender

import (
	"context"
	"errors"
	"fmt"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
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

type FlowInterface interface {
	GenerateBuildParams(ctx context.Context,
		preParams *types.CertificatePreBuildParams) (*types.CertificateBuildParams, error)
	BuildCertificate(ctx context.Context,
		buildParams *types.CertificateBuildParams) (*agglayertypes.Certificate, error)
}

type AggsenderValidator struct {
	log                   aggkitcommon.Logger
	flowPP                FlowInterface
	l1InfoTreeDataQuerier L1InfoTreeRootByLeafQuerier
}

func NewAggsenderValidator(ctx context.Context,
	logger *log.Logger,
	flowPP FlowInterface,
	l1InfoTreeDataQuerier L1InfoTreeRootByLeafQuerier) (*AggsenderValidator, error) {
	return &AggsenderValidator{
		log:                   logger,
		flowPP:                flowPP,
		l1InfoTreeDataQuerier: l1InfoTreeDataQuerier,
	}, nil
}
func (a *AggsenderValidator) Start(ctx context.Context) {
}

// GetRPCServices returns the list of services that the RPC provider exposes
func (a *AggsenderValidator) GetRPCServices() []jRPC.Service {

	logger := log.WithFields("aggsender-validator-rpc", aggkitcommon.BRIDGE)
	return []jRPC.Service{
		{
			Name:    "aggsender-validator",
			Service: aggsenderrpc.NewAggsenderValidatorRPC(logger, a),
		},
	}
}

type VerifyIncommingRequests = types.VerifyIncommingRequests

func (a *AggsenderValidator) ValidateCertificate(ctx context.Context, params VerifyIncommingRequests) error {
	if params.Certificate == nil {
		return ErrNilCertificate
	}
	// Between cert must be no gap because if there are could be a attack vector
	if err := a.CheckContigousCertificates(params); err != nil {
		return err
	}
	// Build corresponding certificate
	preBuildParams, err := a.GetCertificatePreBuildParams(ctx, params)
	if err != nil {
		return fmt.Errorf("failed to get certificate pre-build params: %w", err)
	}
	buildParams, err := a.flowPP.GenerateBuildParams(ctx, preBuildParams)
	if err != nil {
		return fmt.Errorf("failed to generate certificate build params: %w", err)
	}
	certificate, err := a.flowPP.BuildCertificate(ctx, buildParams)
	if err != nil {
		return fmt.Errorf("failed to build certificate: %w", err)
	}
	err = a.CompareCertificates(params.Certificate, certificate)
	if err != nil {
		return fmt.Errorf("failed to compare certificates: %w", err)
	}
	return ErrNotImplemented
}
func (a *AggsenderValidator) CompareCertificates(
	incomingCertificate *agglayertypes.Certificate,
	localCertificate *agglayertypes.Certificate) error {
	if incomingCertificate == nil || localCertificate == nil {
		return fmt.Errorf("one of the certificates is nil, incoming: %v, local: %v",
			incomingCertificate, localCertificate)
	}
	if incomingCertificate.Hash() != localCertificate.Hash() {
		return fmt.Errorf("certificates hash mismatch, incoming: %s, local: %s",
			incomingCertificate.Hash().Hex(), localCertificate.Hash().Hex())
	}
	return nil
}

func (a *AggsenderValidator) CheckContigousCertificates(params VerifyIncommingRequests) error {
	if params.Certificate == nil {
		return ErrNilCertificate
	}
	if params.PreviousCertificate == nil {
		// TODO: Check that the first certificate start at the begining of genesis
		return a.CheckFirstCerficateBlocks(params)
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
	if params.Certificate == nil {
		return nil, ErrNilCertificate
	}
	lastSentCertificate, err := validator.AgglayerCertificateHeaderToAggsender(params.PreviousCertificate)
	if err != nil {
		return nil, fmt.Errorf("failed to convert previous certificate to Aggsender format: %w", err)
	}
	metadataUnmarshal, err := types.NewCertificateMetadataFromHash(params.Certificate.Metadata)
	if err != nil {
		return nil, ErrMetadataNotCompatible
	}
	blockRange, err := metadataUnmarshal.BlockRange()
	if err != nil {
		return nil, fmt.Errorf("failed to get block range from certificate metadata: %w", err)
	}

	l1InfoRoot, err := a.l1InfoTreeDataQuerier.GetL1InfoRootByLeafCount(ctx, params.Certificate.L1InfoTreeLeafCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get L1 Info tree root by leaf count %d: %w",
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
