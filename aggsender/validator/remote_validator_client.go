package validator

import (
	"context"
	"fmt"

	nodev1 "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/node/types/v1"
	typesv1 "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	agglayergrpc "github.com/agglayer/aggkit/agglayer/grpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	v1 "github.com/agglayer/aggkit/aggsender/validator/proto/v1"
	"github.com/agglayer/aggkit/grpc"
)

var _ types.CertificateValidateAndSigner = (*RemoteValidatorClient)(nil)

// RemoteValidatorClient encapsulates the gRPC client and configuration
// required to interact with the AggsenderValidator service.
type RemoteValidatorClient struct {
	client        v1.AggsenderValidatorClient
	grpcClientCfg *grpc.ClientConfig
	storage       db.AggSenderStorage
}

// NewRemoteValidatorClient initializes a new RemoteValidatorClient with the provided gRPC client configuration.
// It returns an error if the gRPC client cannot be created.
func NewRemoteValidatorClient(cfg *grpc.ClientConfig, storage db.AggSenderStorage) (*RemoteValidatorClient, error) {
	grpcClient, err := grpc.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	return &RemoteValidatorClient{
		client:        v1.NewAggsenderValidatorClient(grpcClient.Conn()),
		grpcClientCfg: cfg,
		storage:       storage,
	}, nil
}

// String returns a string representation of the ValidatorClient.
func (v *RemoteValidatorClient) String() string {
	return "RemoteValidator"
}

// ValidateAndSignCertificate sends a certificate to the AggsenderValidator service for validation.
func (v *RemoteValidatorClient) ValidateAndSignCertificate(
	ctx context.Context,
	certificate *agglayertypes.Certificate,
) ([]byte, error) {
	protoCert, err := agglayergrpc.ConvertCertToProtoCertificate(certificate)
	if err != nil {
		return nil, err
	}

	previousCertificate, err := getPreviousCertificate(v.storage, certificate.Height, certificate.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("error getting previous certificate header by height %d: %w", certificate.Height-1, err)
	}

	response, err := v.client.ValidateCertificate(ctx, &v1.ValidateCertificateRequest{
		PreviousCertificateId: certIDToProto(previousCertificate),
		Certificate:           protoCert,
	})

	if err != nil {
		return nil, fmt.Errorf("aggsender validator failed to successfully validate certificate: %w", err)
	}

	return response.Signature.Value, nil
}

// certIDToProto converts a common.Hash CertificateID to a nodev1.CertificateId proto message.
func certIDToProto(cert *agglayertypes.CertificateHeader) *nodev1.CertificateId {
	if cert == nil {
		return nil
	}

	return &nodev1.CertificateId{
		Value: &typesv1.FixedBytes32{
			Value: cert.CertificateID.Bytes(),
		},
	}
}
