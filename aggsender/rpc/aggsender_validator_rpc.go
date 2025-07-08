package aggsenderrpc

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/db"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
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

// gomng:nolll
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" -d '{"method":"aggsender-validator_validateDB", "params":["aggsender.sqlite"], "id":1}'
//
//nolint:lll
func (b *AggsenderValidatorRPC) ValidateDB(dbPath string) (interface{}, rpc.Error) {
	b.logger.Infof("Validating Aggsender DB at path: %s", dbPath)
	cfg := db.AggSenderSQLStorageConfig{
		DBPath:                  dbPath,
		KeepCertificatesHistory: true,
	}
	database, err := db.NewAggSenderSQLStorage(b.logger, cfg)
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
	}
	cert, err := database.GetLastSentCertificate()
	if err != nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
	}
	if cert == nil {
		return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "no certificate found in the database")
	}
	height := cert.Header.Height
	done := false
	result := ""
	b.logger.Infof("rpc-validator. Height: %d oldest certificate in DB %s", height, cert.Header.CertificateID.Hex())
	for !done {
		cert, err = database.GetCertificateByHeight(height)
		if err != nil {
			return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "failed to get certificate by height: "+err.Error())
		}
		if cert == nil || cert.SignedCertificate == nil {
			return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "no certificate found in the database")
		}
		if cert.Header.Status.IsSettled() {
			// Unmarshall string with a json of a certificate
			var unmarshalCert *agglayertypes.Certificate
			err = json.Unmarshal([]byte(*cert.SignedCertificate), &unmarshalCert)
			if err != nil {
				return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "failed to unmarshal certificate: "+err.Error())
			}
			if unmarshalCert == nil {
				return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "unmarshalled certificate is nil")
			}
			var previousCertificate *agglayertypes.CertificateHeader
			if height > 0 {
				prevCertHeader, err := database.GetCertificateHeaderByHeight(height - 1)
				if err != nil {
					return err, rpc.NewRPCError(rpc.DefaultErrorCode, "failed to get previous certificate header: "+err.Error())
				}
				if prevCertHeader != nil {
					previousCertificate = validator.AggsenderCertificateHeaderToAgglayer(prevCertHeader, unmarshalCert.NetworkID)
				}
			}
			params := types.VerifyIncommingRequests{
				Certificate:         unmarshalCert,
				PreviousCertificate: previousCertificate,
			}
			ctx := context.Background()
			b.logger.Infof("rpc-validator. Validating Height: %d", height)
			if err := b.aggsenderValidator.ValidateCertificate(ctx, params); err != nil {
				b.logger.Infof("rpc-validator. Validation failed for %d / %s, Error: %s",
					unmarshalCert.Height, unmarshalCert.ID(), err.Error())
				return err.Error(), rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
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
