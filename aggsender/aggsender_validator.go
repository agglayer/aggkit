package aggsender

import (
	"context"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/metrics"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/grpc"
	signertypes "github.com/agglayer/go_signer/signer/types"
	ethcommon "github.com/ethereum/go-ethereum/common"
)


type AggsenderValidator struct {
	log                           aggkitcommon.Logger
	validator                     types.CertificateValidator
	validatorService              *grpc.Server
	initialBlockClaimSyncerSetter types.InitialBlockClaimSyncerSetter
	l2ClaimSyncer                 claimsynctypes.ClaimSyncer
	cfg                           validator.Config
}

func NewAggsenderValidator(ctx context.Context,
	logger aggkitcommon.Logger,
	cfg validator.Config,
	l2ClaimSyncer claimsynctypes.ClaimSyncer,
	flow types.AggsenderVerifierFlow,
	l1InfoTreeDataQuerier validator.L1InfoTreeRootByLeafQuerier,
	aggLayerClient agglayer.AggLayerClientCertificateIDQuerier,
	certQuerier types.CertificateQuerier,
	aggchainFEPQuerier types.AggchainFEPRollupQuerier,
	initialLER ethcommon.Hash,
	signer signertypes.Signer,
	initialBlockClaimSyncerSetter types.InitialBlockClaimSyncerSetter) (*AggsenderValidator, error) {
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
		log:                           logger,
		validator:                     validatorCert,
		validatorService:              grpcServer,
		cfg:                           cfg,
		l2ClaimSyncer:                 l2ClaimSyncer,
		initialBlockClaimSyncerSetter: initialBlockClaimSyncerSetter,
	}, nil
}
func (a *AggsenderValidator) Start(ctx context.Context) {
	metrics.Register()
	// This is hardcoded because validator to just do 1 retry if fails it and stop
	rh := aggkitcommon.NewRetryHandler([]configtypes.Duration{{Duration: a.cfg.DelayBetweenRetries.Duration}},
		1)
	err := a.initialBlockClaimSyncerSetter.SetClaimSyncerNextRequiredBlock(ctx, a.l2ClaimSyncer, rh)
	if err != nil {
		a.log.Panicf("failed to set claim syncer next required block: %v", err)
	}
	a.validatorService.Start(ctx)
}

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *AggsenderValidator) ValidateCertificate(ctx context.Context, params types.VerifyIncomingRequest) error {
	return a.validator.ValidateCertificate(ctx, params)
}
