package aggsenderrpc

import (
	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	aggkitcommon "github.com/agglayer/aggkit/common"
)

type AggsenderValidatorRPC struct {
	logger      aggkitcommon.Logger
	dbValidator *validator.DBValidator
}

func NewAggsenderValidatorRPC(
	logger aggkitcommon.Logger,
	aggsenderValidator types.CertificateValidator,
) *AggsenderValidatorRPC {
	return &AggsenderValidatorRPC{
		logger:      logger,
		dbValidator: validator.NewDBValidator(logger, aggsenderValidator),
	}
}

// gomng:nolll
// curl -X POST http://localhost:5576/ -H "Content-Type: application/json" -d '{"method":"aggsender-validator_validateDB", "params":["aggsender.sqlite"], "id":1}'
//
//nolint:lll
func (b *AggsenderValidatorRPC) ValidateDB(dbPath string) (interface{}, rpc.Error) {
	b.logger.Infof("Validating Aggsender DB at path: %s", dbPath)
	return b.dbValidator.ValidateDB(dbPath)
}
