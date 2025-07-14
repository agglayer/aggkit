package validator

import (
	"encoding/binary"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

func HashCertificateToSign(cert *agglayertypes.Certificate) common.Hash {
	globalIndexHashes := make([][]byte, len(cert.ImportedBridgeExits))
	for i, importedBridgeExit := range cert.ImportedBridgeExits {
		globalIndexHashes[i] = importedBridgeExit.GlobalIndex.Hash().Bytes()
	}
	networkID := make([]byte, 4)
	binary.BigEndian.PutUint32(networkID, cert.NetworkID)
	height := make([]byte, 8)
	binary.BigEndian.PutUint64(height, cert.Height)
	return crypto.Keccak256Hash(
		cert.NewLocalExitRoot.Bytes(),
		crypto.Keccak256Hash(globalIndexHashes...).Bytes(),
		networkID,
		height,
		cert.Metadata[:],
	)
}
