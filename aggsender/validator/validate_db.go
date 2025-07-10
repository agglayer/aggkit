package validator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

type DBValidator struct {
	logger             aggkitcommon.Logger
	aggsenderValidator types.CertificateValidator
}

func NewDBValidator(
	logger aggkitcommon.Logger,
	aggsenderValidator types.CertificateValidator,
) *DBValidator {
	return &DBValidator{
		logger:             logger,
		aggsenderValidator: aggsenderValidator,
	}
}

func (b *DBValidator) ValidateDB(dbPath string) (string, rpc.Error) {
	b.logger.Infof("Validating Aggsender DB at path: %s", dbPath)
	cfg := db.AggSenderSQLStorageConfig{
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
	}
	database, err := db.NewAggSenderSQLStorage(b.logger, cfg)
	if err != nil {
		return "", rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
	}
	cert, err := database.GetLastSentCertificate()
	if err != nil {
		return "", rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
	}
	if cert == nil {
		return "", rpc.NewRPCError(rpc.DefaultErrorCode, "no certificate found in the database")
	}
	height := cert.Header.Height
	done := false
	result := ""
	b.logger.Infof("rpc-validator. Height: %d oldest certificate in DB %s", height, cert.Header.CertificateID.Hex())
	for !done {
		cert, err = database.GetCertificateByHeight(height)
		if err != nil {
			return "", rpc.NewRPCError(rpc.DefaultErrorCode, "failed to get certificate by height: "+err.Error())
		}
		if cert == nil || cert.SignedCertificate == nil {
			return "", rpc.NewRPCError(rpc.DefaultErrorCode, "no certificate found in the database")
		}
		if cert.Header.Status.IsSettled() {
			// Unmarshall string with a json of a certificate
			var unmarshalCert *agglayertypes.Certificate
			err = json.Unmarshal([]byte(*cert.SignedCertificate), &unmarshalCert)
			if err != nil {
				return "", rpc.NewRPCError(rpc.DefaultErrorCode, "failed to unmarshal certificate: "+err.Error())
			}
			if unmarshalCert == nil {
				return "", rpc.NewRPCError(rpc.DefaultErrorCode, "unmarshalled certificate is nil")
			}
			var previousCertificate *agglayertypes.CertificateHeader
			if height > 0 {
				prevCertHeader, err := database.GetCertificateHeaderByHeight(height - 1)
				if err != nil {
					return result, rpc.NewRPCError(rpc.DefaultErrorCode, "failed to get previous certificate header: "+err.Error())
				}
				if prevCertHeader != nil {
					previousCertificate = AggsenderCertificateHeaderToAgglayer(prevCertHeader, unmarshalCert.NetworkID)
				}
			}
			params := types.VerifyIncomingRequest{
				Certificate:         unmarshalCert,
				PreviousCertificate: previousCertificate,
			}
			ctx := context.Background()
			b.logger.Infof("rpc-validator. Validating Height: %d", height)
			if err := b.aggsenderValidator.ValidateCertificate(ctx, params); err != nil {
				b.logger.Infof("rpc-validator. Validation failed for %d / %s, Error: %s",
					unmarshalCert.Height, unmarshalCert.ID(), err.Error())
				return result, rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
			}
			b.logger.Infof("rpc-validator. Validated Height: %d. OK", height)
			result += "Certificate " + unmarshalCert.ID() + " at height " +
				fmt.Sprintf("%d", unmarshalCert.Height) + " is valid\n"
		} else {
			b.logger.Infof("rpc-validator. Certificate %s at height %d is not settled yet, skipping validation",
				cert.Header.CertificateID.Hex(), height)
		}
		if cert.Header.Height == 0 {
			b.logger.Infof("rpc-validator.All certs done, stopping validation")
			done = true
		}
		height--
	}
	b.logger.Infof("rpc-validator. Validation completed for database %s. Result: %s", dbPath, result)
	return result, nil
}
