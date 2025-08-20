package validator

import (
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// HashCertificateToSign is the hash of the certificate that the validator will sign
// before returning result to the aggsender
func HashCertificateToSign(cert *agglayertypes.Certificate) (common.Hash, error) {
	if err := cert.Validate(); err != nil {
		return common.Hash{}, err
	}
	return crypto.Keccak256Hash(
		cert.CertificateID().Bytes(),
		aggkitcommon.Uint32ToLittleEndianBytes(cert.L1InfoTreeLeafCount)), nil
}
