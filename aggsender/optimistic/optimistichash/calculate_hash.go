package optimistichash

import (
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// This file calculates the hash of OptimisticSignatureData
// that is required to generate the signature for the optimistic proof.

type OptimisticSignatureData struct {
	AggregationProofPublicValuesHash common.Hash
	NewLocalExitRoot                 common.Hash
	CommitImportedBridgeExits        common.Hash
}

// // Hash calculates the hash of the OptimisticSignatureData.
func (o *OptimisticSignatureData) Hash() common.Hash {
	return crypto.Keccak256Hash(
		o.AggregationProofPublicValuesHash.Bytes(),
		o.NewLocalExitRoot.Bytes(),
		o.CommitImportedBridgeExits.Bytes(),
	)
}

func (o *OptimisticSignatureData) String() string {
	return fmt.Sprintf(
		"OptimisticSignatureData{AggregationProofPublicValuesHash: %s, NewLocalExitRoot: %s, CommitImportedBridgeExits: %s}",
		o.AggregationProofPublicValuesHash.Hex(),
		o.NewLocalExitRoot.Hex(),
		o.CommitImportedBridgeExits.Hex(),
	)
}
