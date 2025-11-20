package aggsender

import (
	"context"
	"errors"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
)

var (
	ErrNilCertificate        = errors.New("aggsender-validator nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-validator metadata not compatible with the current version")
)

type AggsenderValidator struct {
	log              aggkitcommon.Logger
	validator        types.CertificateValidator
	validatorService *grpc.Server
	cfg              validator.Config
}

func NewAggsenderValidator(ctx context.Context,
	logger aggkitcommon.Logger,
	cfg validator.Config,
	flow types.AggsenderVerifierFlow,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
	aggLayerClient agglayer.AggLayerClientCertificateIDQuerier,
	certQuerier types.CertificateQuerier,
	aggchainFEPQuerier types.AggchainFEPRollupQuerier,
	lerQuerier types.LERQuerier,
	l1GERQuerier types.L1GERQuerier,
	signer signertypes.Signer) (*AggsenderValidator, error) {
	validatorCert := validator.NewAggsenderValidator(
		logger, flow, l1InfoTreeDataQuerier, certQuerier, lerQuerier, l1GERQuerier)
	grpcServer, err := grpc.NewServer(cfg.ServerConfig)
	if err != nil {
		return nil, err
	}

	v1.RegisterAggsenderValidatorServer(grpcServer.GRPC(), validator.NewValidatorService(
		logger,
		validatorCert,
		aggLayerClient,
		signer,
	))
	return &AggsenderValidator{
		log:              logger,
		validator:        validatorCert,
		validatorService: grpcServer,
		cfg:              cfg,
	}, nil
}
func (a *AggsenderValidator) Start(ctx context.Context) {
	a.validatorService.Start(ctx)
}

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *AggsenderValidator) ValidateCertificate(ctx context.Context, params types.VerifyIncomingRequest) error {
	return a.validator.ValidateCertificate(ctx, params)
}

// ValidateGER validates the GlobalExitRoot that needs to be injected.
func (a *AggsenderValidator) ValidateGER(ctx context.Context, ger common.Hash) error {
	return a.validator.ValidateGER(ctx, ger)
}
