package validator

import (
	"encoding/binary"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const (
	u32Size = 4
	u64Size = 8
)

// HashCertificateToSign is the hash of the certificate that the validator will sign
// before returning result to the aggsender
func HashCertificateToSign(cert *agglayertypes.Certificate) common.Hash {
	globalIndexHashes := make([][]byte, len(cert.ImportedBridgeExits))
	for i, importedBridgeExit := range cert.ImportedBridgeExits {
		globalIndexHashes[i] = importedBridgeExit.GlobalIndex.Hash().Bytes()
	}
	networkID := make([]byte, u32Size)
	binary.BigEndian.PutUint32(networkID, cert.NetworkID)
	height := make([]byte, u64Size)
	binary.BigEndian.PutUint64(height, cert.Height)
	return crypto.Keccak256Hash(
		cert.NewLocalExitRoot.Bytes(),
		crypto.Keccak256Hash(globalIndexHashes...).Bytes(),
		networkID,
		height,
		cert.Metadata[:],
	)
}
