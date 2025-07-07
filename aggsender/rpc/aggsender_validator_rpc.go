package aggsenderrpc

import (
	"context"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
)

type AggsenderValidatorRPC struct {
	logger             *log.Logger
	aggsenderValidator types.CertificateValidator
}

func NewAggsenderValidatorRPC(
	logger *log.Logger,
	aggsenderValidator types.CertificateValidator,
) *AggsenderValidatorRPC {
	return &AggsenderValidatorRPC{
		logger:             logger,
		aggsenderValidator: aggsenderValidator,
	}
}

func (b *AggsenderValidatorRPC) Validate(certificate *agglayertypes.Certificate,
	PreviousCertificate *agglayertypes.CertificateHeader) rpc.Error {
	params := types.VerifyIncommingRequests{
		Certificate:         certificate,
		PreviousCertificate: PreviousCertificate,
	}
	ctx := context.Background()
	if err := b.aggsenderValidator.ValidateCertificate(ctx, params); err != nil {
		return rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
	}
	return nil
}
