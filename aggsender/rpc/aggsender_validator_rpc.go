package aggsenderrpc

import (
	"context"
	"encoding/json"

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

// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" -d '{"method":"aggsender-validator_validateDB", "params":["aggsender.sqlite"], "id":1}'
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
	hight := cert.Header.Height
	b.logger.Infof("rpc-validator. Height: %d oldest certificate in DB %s", hight, cert.Header.CertificateID.Hex())
	for hight >= 0 {
		cert, err = database.GetCertificateByHeight(hight)
		if err != nil {
			return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "failed to get certificate by height: "+err.Error())
		}
		if cert == nil || cert.SignedCertificate == nil {
			return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "no certificate found in the database")
		}
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
		if hight > 0 {
			prevCertHeader, err := database.GetCertificateHeaderByHeight(hight - 1)
			if err != nil {
				return nil, rpc.NewRPCError(rpc.DefaultErrorCode, "failed to get previous certificate header: "+err.Error())
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
		b.logger.Infof("rpc-validator. Validating Height: %d", hight)
		if err := b.aggsenderValidator.ValidateCertificate(ctx, params); err != nil {
			return nil, rpc.NewRPCError(rpc.DefaultErrorCode, err.Error())
		}
	}
	return nil, nil
}
