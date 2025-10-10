package validator

import (
	"context"
	"fmt"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	typesv1 "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/grpc"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/protobuf/types/known/emptypb"
)

var _ types.ValidatorClient = (*ValidatorClient)(nil)

// ValidatorClient encapsulates the gRPC client and configuration
// required to interact with the AggsenderValidator service.
type ValidatorClient struct {
	client        v1.AggsenderValidatorClient
	grpcClientCfg *grpc.ClientConfig
}

// NewValidatorClient initializes a new ValidatorClient with the provided gRPC client configuration.
// It returns an error if the gRPC client cannot be created.
func NewValidatorClient(cfg *grpc.ClientConfig) (*ValidatorClient, error) {
	grpcClient, err := grpc.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	return &ValidatorClient{
		client:        v1.NewAggsenderValidatorClient(grpcClient.Conn()),
		grpcClientCfg: cfg,
	}, nil
}

func (v *ValidatorClient) HealthCheck(ctx context.Context) (*types.HealthCheckResponse, error) {
	response, err := v.client.HealthCheck(ctx, &emptypb.Empty{})
	if err != nil {
		return nil, fmt.Errorf("aggsender validator failed to get status: %w", err)
	}
	return &types.HealthCheckResponse{
		Status:       response.Status,
		StatusReason: response.Reason,
		Version:      response.Version,
	}, nil
}

// ValidateCertificate sends a certificate to the AggsenderValidator service for validation.
func (v *ValidatorClient) ValidateCertificate(
	ctx context.Context,
	previousCertificateID *common.Hash, // can be nil if there is no previous certificate
	certificate *agglayertypes.Certificate,
	lastL2BlockInCert uint64,
) ([]byte, error) {
	protoCert, err := agglayergrpc.ConvertCertToProtoCertificate(certificate)
	if err != nil {
		return nil, err
	}

	response, err := v.client.ValidateCertificate(ctx, &v1.ValidateCertificateRequest{
		PreviousCertificateId: certIDToProtoNullable(previousCertificateID),
		Certificate:           protoCert,
		LastL2BlockInCert:     lastL2BlockInCert,
	})

	if err != nil {
		return nil, fmt.Errorf("aggsender validator failed to successfully validate certificate: %w", err)
	}

	return response.Signature.Value, nil
}

// certIDToProtoNullable converts a common.Hash pointer to a nodev1.CertificateId proto message.
func certIDToProtoNullable(certID *common.Hash) *nodev1.CertificateId {
	if certID == nil {
		return nil
	}

	return &nodev1.CertificateId{
		Value: &typesv1.FixedBytes32{
			Value: certID.Bytes(),
		},
	}
}
