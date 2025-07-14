package validator

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

// LocalValidator is a struct that implements the types.Validator interface
// and is used to validate and sign certificates locally.
// This is a temporary check, in the future it will be replaced with a object that
// calls to aggsender-validator using grpc
type LocalValidator struct {
	Log       aggkitcommon.Logger
	Storage   db.AggSenderStorage
	Validator types.CertificateValidator
}

func (a *LocalValidator) String() string {
	return "LocalValidator"
}

func (a *LocalValidator) ValidateAndSignCertificate(
	ctx context.Context,
	certificate *agglayertypes.Certificate,
) ([]byte, error) {
	a.Log.Infof("certificate validation: %s ....", certificate.Brief())
	verifyParams := types.VerifyIncomingRequest{
		Certificate:         certificate,
		PreviousCertificate: nil,
	}
	if certificate.Height != 0 {
		previousSettledCertificate, err := a.Storage.GetCertificateHeaderByHeight(certificate.Height - 1)
		if err != nil {
			a.Log.Errorf("error getting previous certificate header by height %d: %s", certificate.Height-1, err.Error())
			return nil, fmt.Errorf("error getting previous certificate header by height %d: %w", certificate.Height-1, err)
		}
		if previousSettledCertificate != nil {
			verifyParams.PreviousCertificate = AggsenderCertificateHeaderToAgglayer(
				previousSettledCertificate, certificate.NetworkID)
		}
	}
	if err := a.Validator.ValidateCertificate(ctx, verifyParams); err != nil {
		a.Log.Errorf("certificate validation failed: %s. Cert: %s", err.Error(), certificate.Brief())
		return nil, fmt.Errorf("certificate validation failed: %w", err)
	}
	a.Log.Infof("certificate validation passed: %s", certificate.Brief())
	// Current code is not able to sign the certificate, so we return a dummy signature.
	return []byte{1, 2, 3}, nil
}
