package validator

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/grpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

var _ types.CertificateValidateAndSigner = (*RemoteValidator)(nil)

// RemoteValidator encapsulates the gRPC client and configuration
// required to interact with the AggsenderValidator service.
type RemoteValidator struct {
	url     string
	address common.Address
	client  types.ValidatorClient
	storage db.AggSenderStorage
	index   uint32
}

// NewRemoteValidator initializes a new RemoteValidator with the provided gRPC client configuration.
// It returns an error if the gRPC client cannot be created.
func NewRemoteValidator(
	cfg *grpc.ClientConfig,
	storage db.AggSenderStorage,
	address common.Address,
	index uint32,
) (*RemoteValidator, error) {
	client, err := NewValidatorClient(cfg)
	if err != nil {
		return nil, err
	}

	return &RemoteValidator{
		url:     cfg.URL,
		client:  client,
		storage: storage,
		address: address,
		index:   index,
	}, nil
}

// String returns a string representation of the RemoteValidator.
func (v *RemoteValidator) String() string {
	return fmt.Sprintf("RemoteValidator (URL=%s, Address=%s)", v.url, v.address.String())
}

// URL returns an URL for the remote validator
func (v *RemoteValidator) URL() string {
	return v.url
}

// Address returns the Ethereum address of the remote validator
func (v *RemoteValidator) Address() common.Address {
	return v.address
}

// Index is the index of the signer in the signers list on the Multisig contract
func (v *RemoteValidator) Index() uint32 {
	return v.index
}

// HealthCheck performs a health check on the AggsenderValidator service.
func (v *RemoteValidator) HealthCheck(ctx context.Context) (*types.HealthCheckResponse, error) {
	return v.client.HealthCheck(ctx)
}

// ValidateAndSignCertificate sends a certificate to the AggsenderValidator service for validation.
func (v *RemoteValidator) ValidateAndSignCertificate(
	ctx context.Context,
	certificate *agglayertypes.Certificate,
	lastL2BlockInCert uint64,
) ([]byte, error) {
	// Get the hash of the certificate, fail fast if something is wrong in it

	certificateHash, err := HashCertificateToSign(certificate)
	if err != nil {
		return nil, fmt.Errorf("internal error getting certificate hash: %w", err)
	}

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
		lastL2BlockInCert,
	)
	if err != nil {
		return nil, fmt.Errorf("error validating certificate on aggsender validator service: %w", err)
	}

	// Validate received signature
	// We do not support ethereum legacy v+27 signatures

	recoveredPublicKey, err := crypto.SigToPub(certificateHash[:], signature)
	if err != nil {
		return nil, fmt.Errorf("error validating remote validator signature: %w", err)
	}

	recoveredAddress := crypto.PubkeyToAddress(*recoveredPublicKey)
	if v.address != recoveredAddress {
		return nil, fmt.Errorf("error validating remote validator signature, mismatch expected:%v current:%v",
			v.address, recoveredAddress)
	}

	return signature, nil
}
