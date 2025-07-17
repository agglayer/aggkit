package validator

import (
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// HashCertificateToSign is the hash of the certificate that the validator will sign
// before returning result to the aggsender
func HashCertificateToSign(cert *agglayertypes.Certificate) common.Hash {
	globalIndexHashes := make([][]byte, len(cert.ImportedBridgeExits))
	for i, importedBridgeExit := range cert.ImportedBridgeExits {
		globalIndexHashes[i] = importedBridgeExit.GlobalIndex.Hash().Bytes()
	}
	networkID := aggkitcommon.Uint32ToBigEndianBytes(cert.NetworkID)
	height := aggkitcommon.Uint64ToBigEndianBytes(cert.Height)
	return crypto.Keccak256Hash(
		cert.NewLocalExitRoot.Bytes(),
		crypto.Keccak256Hash(globalIndexHashes...).Bytes(),
		networkID,
		height,
		cert.Metadata[:],
	)
}
