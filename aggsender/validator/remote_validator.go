package validator

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/grpc"
	"github.com/ethereum/go-ethereum/common"
)

var _ types.CertificateValidateAndSigner = (*RemoteValidator)(nil)

// RemoteValidator encapsulates the gRPC client and configuration
// required to interact with the AggsenderValidator service.
type RemoteValidator struct {
	client  types.ValidatorClient
	storage db.AggSenderStorage
}

// NewRemoteValidator initializes a new RemoteValidator with the provided gRPC client configuration.
// It returns an error if the gRPC client cannot be created.
func NewRemoteValidator(cfg *grpc.ClientConfig, storage db.AggSenderStorage) (*RemoteValidator, error) {
	client, err := NewValidatorClient(cfg)
	if err != nil {
		return nil, err
	}

	return &RemoteValidator{
		client:  client,
		storage: storage,
	}, nil
}

// String returns a string representation of the RemoteValidator.
func (v *RemoteValidator) String() string {
	return "RemoteValidator"
}

// ValidateAndSignCertificate sends a certificate to the AggsenderValidator service for validation.
func (v *RemoteValidator) ValidateAndSignCertificate(
	ctx context.Context,
	certificate *agglayertypes.Certificate,
) ([]byte, error) {
	previousCertificate, err := getPreviousCertificate(v.storage, certificate.Height, certificate.NetworkID)
	if err != nil {
		return nil, fmt.Errorf("error getting previous certificate header by height %d: %w", certificate.Height-1, err)
	}

	var previousCertificateID *common.Hash
	if previousCertificate != nil {
		previousCertificateID = &previousCertificate.CertificateID
	}

	signature, err := v.client.ValidateCertificate(
		ctx,
		previousCertificateID,
		certificate,
	)
	if err != nil {
		return nil, fmt.Errorf("error validating certificate on aggsender validator service: %w", err)
	}

	return signature, nil
}
