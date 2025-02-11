package grpc

import (
	"context"
	"time"

	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

const TIMEOUT = 2

// AggchainProofClientInterface defines an interface for aggchain proof client
type AggchainProofClientInterface interface {
	GenerateAggchainProof(
		startBlock uint64,
		maxEndBlock uint64,
		l1InfoTreeRootHash common.Hash,
		l1InfoTreeLeaf l1infotreesync.L1InfoTreeLeaf,
		l1InfoTreeMerkleProof treeTypes.Proof,
		gerInclusionProofs map[common.Hash]treeTypes.Proof,
	) (*types.AggchainProof, error)
}

// AggchainProofClient provides an implementation for the AggchainProofClient interface
type AggchainProofClient struct {
	client types.AggchainProofServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAggchainProofClient(serverAddr string) (*AggchainProofClient, error) {
	grpcClient, err := NewClient(serverAddr)
	if err != nil {
		return nil, err
	}
	return &AggchainProofClient{
		client: types.NewAggchainProofServiceClient(grpcClient.conn),
	}, nil
}

func (c *AggchainProofClient) GenerateAggchainProof(
	startBlock uint64,
	maxEndBlock uint64,
	l1InfoTreeRootHash common.Hash,
	l1InfoTreeLeaf l1infotreesync.L1InfoTreeLeaf,
	l1InfoTreeMerkleProof treeTypes.Proof,
	gerInclusionProofs map[common.Hash]treeTypes.Proof,
) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*TIMEOUT)
	defer cancel()

	convertedMerkleProof := make([][]byte, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProof[i] = l1InfoTreeMerkleProof[i].Bytes()
	}

	convertedGerInclusionProofs := make(map[string]*types.InclusionProof)
	for k, v := range gerInclusionProofs {
		convertedProof := make([][]byte, treeTypes.DefaultHeight)
		for i := 0; i < int(treeTypes.DefaultHeight); i++ {
			convertedProof[i] = v[i].Bytes()
		}
		convertedGerInclusionProofs[k.String()] = &types.InclusionProof{
			Siblings: convertedProof,
		}
	}

	convertedL1InfoTreeLeaf := &types.L1InfoTreeLeaf{
		PreviousBlockHash:   l1InfoTreeLeaf.PreviousBlockHash.Bytes(),
		Timestamp:           l1InfoTreeLeaf.Timestamp,
		MainnetExitRootHash: l1InfoTreeLeaf.MainnetExitRoot.Bytes(),
		GlobalExitRootHash:  l1InfoTreeLeaf.GlobalExitRoot.Bytes(),
		RollupExitRootHash:  l1InfoTreeLeaf.RollupExitRoot.Bytes(),
		LeafHash:            l1InfoTreeLeaf.Hash.Bytes(),
		L1InfoTreeIndex:     l1InfoTreeLeaf.L1InfoTreeIndex,
	}

	resp, err := c.client.GenerateAggchainProof(ctx, &types.GenerateAggchainProofRequest{
		StartBlock:            startBlock,
		MaxEndBlock:           maxEndBlock,
		L1InfoTreeRootHash:    l1InfoTreeRootHash.Bytes(),
		L1InfoTreeLeaf:        convertedL1InfoTreeLeaf,
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerInclusionProofs:    convertedGerInclusionProofs,
	})
	if err != nil {
		return nil, err
	}

	return &types.AggchainProof{
		Proof:      resp.AggchainProof,
		StartBlock: resp.StartBlock,
		EndBlock:   resp.EndBlock,
	}, nil
}
