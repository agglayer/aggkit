package aggsenderrpc

import (
	"encoding/json"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// HashCertificateForDebugAuth serializes the certificate to JSON and returns its Keccak256 hash.
// Used to produce the message digest that the caller signs when using the debug send certificate endpoint.
func HashCertificateForDebugAuth(cert *agglayertypes.Certificate) (common.Hash, error) {
	data, err := json.Marshal(cert)
	if err != nil {
		return common.Hash{}, fmt.Errorf("HashCertificateForDebugAuth: marshal error: %w", err)
	}
	return crypto.Keccak256Hash(data), nil
}
