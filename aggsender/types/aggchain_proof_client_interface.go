package types

import (
	"context"
	"fmt"

	agglayer "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
)

// AggchainProofClientInterface defines an interface for aggchain proof client
type AggchainProofClientInterface interface {
	GenerateAggchainProof(ctx context.Context, req *AggchainProofRequest) (*AggchainProof, error)
	GenerateOptimisticAggchainProof(req *AggchainProofRequest, signature []byte) (*AggchainProof, error)
}

type AggchainProofRequest struct {
	LastProvenBlock                    uint64
	RequestedEndBlock                  uint64
	L1InfoTreeRootHash                 common.Hash
	L1InfoTreeLeaf                     l1infotreesync.L1InfoTreeLeaf
	L1InfoTreeMerkleProof              agglayer.MerkleProof
	GERLeavesWithBlockNumber           map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber
	ImportedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber
}

func NewAggchainProofRequest(
	lastProvenBlock, requestedEndBlock uint64,
	l1InfoTreeRootHash common.Hash,
	l1InfoTreeLeaf l1infotreesync.L1InfoTreeLeaf,
	l1InfoTreeMerkleProof agglayer.MerkleProof,
	gerLeavesWithBlockNumber map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber,
	importedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber,
) *AggchainProofRequest {
	return &AggchainProofRequest{
		LastProvenBlock:                    lastProvenBlock,
		RequestedEndBlock:                  requestedEndBlock,
		L1InfoTreeRootHash:                 l1InfoTreeRootHash,
		L1InfoTreeLeaf:                     l1InfoTreeLeaf,
		L1InfoTreeMerkleProof:              l1InfoTreeMerkleProof,
		GERLeavesWithBlockNumber:           gerLeavesWithBlockNumber,
		ImportedBridgeExitsWithBlockNumber: importedBridgeExitsWithBlockNumber,
	}
}

func (r *AggchainProofRequest) String() string {
	var importedBridgeStr string
	for _, ib := range r.ImportedBridgeExitsWithBlockNumber {
		importedBridgeStr += fmt.Sprintf("%s, ", ib.String())
	}

	return fmt.Sprintf(`AggchainProofRequest{
	lastProvenBlock: %d,
	toBlock: %d,
	root.Hash: %s,
	*leaf: %+v,
	agglayertypes.MerkleProof{
		Root:  %s,
		Proof: %+v,	
	},
	injectedGERsProofs: %+v,
	importedBridgeExits: %+v
	}`,
		r.LastProvenBlock,
		r.RequestedEndBlock,
		r.L1InfoTreeRootHash.String(),
		r.L1InfoTreeLeaf,
		r.L1InfoTreeMerkleProof.Root.String(),
		r.L1InfoTreeMerkleProof.Proof,
		r.GERLeavesWithBlockNumber,
		r.ImportedBridgeExitsWithBlockNumber,
	)
}
