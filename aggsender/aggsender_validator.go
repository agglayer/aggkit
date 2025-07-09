package aggsender

import (
	"context"
	"errors"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	aggsenderrpc "github.com/agglayer/aggkit/aggsender/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
)

var (
	ErrNotImplemented        = errors.New("aggsender-validator not implemented")
	ErrNilCertificate        = errors.New("aggsender-validator nil certificate")
	ErrMetadataNotCompatible = errors.New("aggsender-validator metadata not compatible with the current version")
)

type AggsenderValidator struct {
	log       aggkitcommon.Logger
	validator types.CertificateValidator
}

func NewAggsenderValidator(ctx context.Context,
	logger *log.Logger,
	flowPP validator.FlowInterface,
	l1InfoTreeDataQuerier validator.L1InfoTreeRootByLeafQuerier) (*AggsenderValidator, error) {
	return &AggsenderValidator{
		log: logger,
		validator: validator.NewAggsenderValidator(
			logger, flowPP, l1InfoTreeDataQuerier),
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
			Service: aggsenderrpc.NewAggsenderValidatorRPC(logger, a.validator),
		},
	}
}

// ValidateCertificate validates the incoming certificate against the previous one.
func (a *AggsenderValidator) ValidateCertificate(ctx context.Context, params types.VerifyIncommingRequests) error {
	return a.validator.ValidateCertificate(ctx, params)
}
