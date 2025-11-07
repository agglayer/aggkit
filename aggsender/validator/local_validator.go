package validator

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/converters"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
)

const localValidatorIdentifier = "LocalValidator"

var _ types.CertificateValidateAndSigner = (*LocalValidator)(nil)

// LocalValidator is a struct that implements the types.Validator interface
// and is used to validate and sign certificates locally.
// This is a temporary check, in the future it will be replaced with a object that
// calls to aggsender-validator using grpc
type LocalValidator struct {
	log       aggkitcommon.Logger
	storage   db.AggSenderStorage
	validator types.CertificateValidator
}

// NewLocalValidator creates a new LocalValidator instance.
func NewLocalValidator(
	log aggkitcommon.Logger,
	storage db.AggSenderStorage,
	validator types.CertificateValidator,
) *LocalValidator {
	return &LocalValidator{
		log:       log,
		storage:   storage,
		validator: validator,
	}
}

// String returns a string representation of the LocalValidator.
func (a *LocalValidator) String() string {
	return localValidatorIdentifier
}

// URL returns an URL for the LocalValidator
func (a *LocalValidator) URL() string {
	return "N/A"
}

// Address returns the Ethereum address of the LocalValidator
func (a *LocalValidator) Address() common.Address {
	return common.Address{}
}

// Index returns the index of the validator in the signers list
// For local validator it is always 0
func (a *LocalValidator) Index() uint32 {
	return 0
}

func (a *LocalValidator) HealthCheck(ctx context.Context) (*types.HealthCheckResponse, error) {
	return &types.HealthCheckResponse{
		Status:  "OK",
		Version: "local",
	}, nil
}

// ValidateAndSignCertificate validates the certificate.
// LocalValidator does not sign the certificate, it just validates it.
func (a *LocalValidator) ValidateAndSignCertificate(
	ctx context.Context,
	certificate *agglayertypes.Certificate,
	lastL2BlockInCert uint64,
) ([]byte, error) {
	a.log.Infof("certificate validation: %s ....", certificate.Brief())
	verifyParams := types.VerifyIncomingRequest{
		Certificate:         certificate,
		PreviousCertificate: nil,
		LastL2BlockInCert:   lastL2BlockInCert,
	}
	if certificate.Height != 0 {
		previousSettledCertificate, err := getPreviousCertificate(a.storage, certificate.Height, certificate.NetworkID)
		if err != nil {
			a.log.Errorf("error getting previous certificate header by height %d: %s", certificate.Height-1, err.Error())
			return nil, fmt.Errorf("error getting previous certificate header by height %d: %w", certificate.Height-1, err)
		}

		verifyParams.PreviousCertificate = previousSettledCertificate
	}
	if err := a.validator.ValidateCertificate(ctx, verifyParams); err != nil {
		a.log.Errorf("certificate validation failed: %s. Cert: %s", err.Error(), certificate.Brief())
		return nil, fmt.Errorf("certificate validation failed: %w", err)
	}
	a.log.Infof("certificate validation passed: %s", certificate.Brief())

	// local validator does not sign the certificate, it just validates it
	return aggkitcommon.EmptySignature, nil
}

// ValidateAndSignGER validates the GlobalExitRoot that needs to be injected.
// LocalValidator does not sign the GER, it just validates it.
func (a *LocalValidator) ValidateAndSignGER(
	ctx context.Context,
	ger common.Hash,
) ([]byte, error) {
	a.log.Infof("GER validation: %s ....", ger.Hex())

	err := a.validator.ValidateGER(ctx, ger)
	if err != nil {
		a.log.Errorf("GER %s validation failed: %s", ger.Hex(), err.Error())
		return nil, fmt.Errorf("GER %s validation failed: %w", ger.Hex(), err)
	}

	a.log.Infof("GER %s validation passed", ger.Hex())

	// local validator does not sign the GER, it just validates it
	return aggkitcommon.EmptySignature, nil
}

// getPreviousCertificate retrieves the previous certificate based on the new certificate's height.
func getPreviousCertificate(
	storage db.AggSenderStorage,
	newCertHeight uint64,
	networkID uint32) (*agglayertypes.CertificateHeader, error) {
	if newCertHeight == 0 {
		// No previous certificate for height 0
		return nil, nil
	}

	certHeader, err := storage.GetCertificateHeaderByHeight(newCertHeight - 1)
	if err != nil {
		return nil, fmt.Errorf("error getting previous certificate header by height %d: %w", newCertHeight-1, err)
	}

	return converters.ConvertAggsenderCertHeaderToAgglayer(certHeader, networkID), nil
}
