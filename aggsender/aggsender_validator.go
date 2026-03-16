package aggsender

import (
	"context"
	"errors"

	signertypes "github.com/agglayer/go_signer/signer/types"
	ethcommon "github.com/ethereum/go-ethereum/common"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
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
	l1InfoTreeDataQuerier validator.L1InfoTreeRootByLeafQuerier,
	aggLayerClient agglayer.AggLayerClientCertificateIDQuerier,
	certQuerier types.CertificateQuerier,
	aggchainFEPQuerier types.AggchainFEPRollupQuerier,
	initialLER ethcommon.Hash,
	signer signertypes.Signer) (*AggsenderValidator, error) {
	validatorCert := validator.NewAggsenderValidator(
		logger, flow, l1InfoTreeDataQuerier, certQuerier, initialLER)
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
	metrics.Register()
	a.validatorService.Start(ctx)
}

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *AggsenderValidator) ValidateCertificate(ctx context.Context, params types.VerifyIncomingRequest) error {
	return a.validator.ValidateCertificate(ctx, params)
}
