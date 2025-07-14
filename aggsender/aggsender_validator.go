package aggsender

import (
	"context"
	"errors"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/agglayer"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
)

var (
	ErrNotImplemented        = errors.New("aggsender-validator not implemented")
	ErrNilCertificate        = errors.New("aggsender-validator nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-validator metadata not compatible with the current version")
)

type AggsenderValidator struct {
	log              aggkitcommon.Logger
	validator        types.CertificateValidator
	validatorService *grpc.Server
}

func NewAggsenderValidator(ctx context.Context,
	logger aggkitcommon.Logger,
	cfg validator.Config,
	flowPP validator.FlowInterface,
	l1InfoTreeDataQuerier validator.L1InfoTreeRootByLeafQuerier,
	aggLayerClient agglayer.AgglayerClientInterface) (*AggsenderValidator, error) {
	validatorCert := validator.NewAggsenderValidator(
		logger, flowPP, l1InfoTreeDataQuerier)
	grpcServer, err := grpc.NewServer(cfg.ServerConfig)
	if err != nil {
		return nil, err
	}
	v1.RegisterAggsenderValidatorServer(grpcServer.GRPC(), validator.NewValidatorService(
		validatorCert,
		aggLayerClient,
	))
	return &AggsenderValidator{
		log:              logger,
		validator:        validatorCert,
		validatorService: grpcServer,
	}, nil
}
func (a *AggsenderValidator) Start(ctx context.Context) {
	a.validatorService.Start(ctx)
}

// GetRPCServices returns the list of services that the RPC provider exposes
func (a *AggsenderValidator) GetRPCServices() []jRPC.Service {
	logger := log.WithFields("aggsender-validator-rpc", aggkitcommon.AGGSENDERVALIDATOR)
	return []jRPC.Service{
		{
			Name:    "aggsender-validator",
			Service: aggsenderrpc.NewAggsenderValidatorRPC(logger, a.validator),
		},
	}
}

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *AggsenderValidator) ValidateCertificate(ctx context.Context, params types.VerifyIncomingRequest) error {
	return a.validator.ValidateCertificate(ctx, params)
}
