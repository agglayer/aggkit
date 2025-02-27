package grpc

import (
	"context"
	"time"

	agglayer "github.com/agglayer/aggkit/agglayer"
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
		l1InfoTreeMerkleProof agglayer.MerkleProof,
		gerLeaves map[common.Hash]*agglayer.ClaimFromMainnnet,
		importedBridgeExits []*agglayer.ImportedBridgeExit,
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
	l1InfoTreeMerkleProof agglayer.MerkleProof,
	gerLeaves map[common.Hash]*agglayer.ClaimFromMainnnet,
	importedBridgeExits []*agglayer.ImportedBridgeExit,
) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*TIMEOUT)
	defer cancel()

	convertedL1InfoTreeLeaf := &types.L1InfoTreeLeaf{
		Inner: &types.L1InfoTreeLeafInner{
			GlobalExitRoot: &types.FixedBytes32{Value: l1InfoTreeLeaf.GlobalExitRoot[:]},
			BlockHash:      &types.FixedBytes32{Value: l1InfoTreeLeaf.Hash[:]},
			Timestamp:      l1InfoTreeLeaf.Timestamp,
		},
		Mer:             &types.FixedBytes32{Value: l1InfoTreeLeaf.MainnetExitRoot[:]},
		Rer:             &types.FixedBytes32{Value: l1InfoTreeLeaf.RollupExitRoot[:]},
		L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
	}

	convertedMerkleProofSiblings := make([]*types.FixedBytes32, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProofSiblings[i] = &types.FixedBytes32{Value: l1InfoTreeMerkleProof.Proof[i][:]}
	}
	convertedMerkleProof := &types.MerkleProof{
		Root:     &types.FixedBytes32{Value: l1InfoTreeMerkleProof.Root[:]},
		Siblings: convertedMerkleProofSiblings,
	}

	convertedGerLeaves := make(map[string]*types.ClaimFromMainnet, 0)
	for k, v := range gerLeaves {
		convertedProofLeafMerSiblings := make([]*types.FixedBytes32, treeTypes.DefaultHeight)
		for i := 0; i < int(treeTypes.DefaultHeight); i++ {
			convertedProofLeafMerSiblings[i] = &types.FixedBytes32{Value: v.ProofLeafMER.Proof[i][:]}
		}
		convertedProofGerL1RootSiblings := make([]*types.FixedBytes32, treeTypes.DefaultHeight)
		for i := 0; i < int(treeTypes.DefaultHeight); i++ {
			convertedProofGerL1RootSiblings[i] = &types.FixedBytes32{Value: v.ProofLeafMER.Proof[i][:]}
		}
		convertedGerLeaves[k.String()] = &types.ClaimFromMainnet{
			ProofLeafMer: &types.MerkleProof{
				Root:     &types.FixedBytes32{Value: v.ProofLeafMER.Root[:]},
				Siblings: convertedProofLeafMerSiblings,
			},
			ProofGerL1Root: &types.MerkleProof{
				Root:     &types.FixedBytes32{Value: v.ProofGERToL1Root.Root[:]},
				Siblings: convertedProofGerL1RootSiblings,
			},
			L1Leaf: &types.L1InfoTreeLeaf{
				L1InfoTreeIndex: v.L1Leaf.L1InfoTreeIndex,
				Rer:             &types.FixedBytes32{Value: v.L1Leaf.RollupExitRoot[:]},
				Mer:             &types.FixedBytes32{Value: v.L1Leaf.MainnetExitRoot[:]},
				Inner: &types.L1InfoTreeLeafInner{
					GlobalExitRoot: &types.FixedBytes32{Value: v.L1Leaf.Inner.GlobalExitRoot[:]},
					BlockHash:      &types.FixedBytes32{Value: v.L1Leaf.Inner.BlockHash[:]},
					Timestamp:      v.L1Leaf.Inner.Timestamp,
				},
			},
		}
	}

	convertedImportedBridgeExits := make([]*types.ImportedBridgeExit, len(importedBridgeExits))
	for i, importedBridgeExit := range importedBridgeExits {
		convertedBridgeExit := &types.BridgeExit{
			LeafType: types.LeafType(importedBridgeExit.BridgeExit.LeafType),
			TokenInfo: &types.TokenInfo{
				OriginNetwork:      importedBridgeExit.BridgeExit.TokenInfo.OriginNetwork,
				OriginTokenAddress: &types.FixedBytes20{Value: importedBridgeExit.BridgeExit.TokenInfo.OriginTokenAddress[:]},
			},
			DestNetwork: importedBridgeExit.BridgeExit.DestinationNetwork,
			DestAddress: &types.FixedBytes20{Value: importedBridgeExit.BridgeExit.DestinationAddress[:]},
			Amount:      &types.FixedBytes32{Value: importedBridgeExit.BridgeExit.Amount.Bytes()},
			Metadata:    &types.FixedBytes32{Value: importedBridgeExit.BridgeExit.Metadata},
		}
		// TODO - How to convert global index into fixedbytes32 format. Currently it is decomposed form
		convertedGlobalIndex := &types.FixedBytes32{}
		convertedImportedBridgeExits[i] = &types.ImportedBridgeExit{
			BridgeExit:  convertedBridgeExit,
			GlobalIndex: convertedGlobalIndex,
		}
	}

	resp, err := c.client.GenerateAggchainProof(ctx, &types.GenerateAggchainProofRequest{
		StartBlock:            startBlock,
		MaxEndBlock:           maxEndBlock,
		L1InfoTreeRootHash:    &types.FixedBytes32{Value: l1InfoTreeRootHash.Bytes()},
		L1InfoTreeLeaf:        convertedL1InfoTreeLeaf,
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerLeaves:             convertedGerLeaves,
		ImportedBridgeExits:   convertedImportedBridgeExits,
	})
	if err != nil {
		return nil, err
	}

	return &types.AggchainProof{
		Proof:           resp.AggchainProof,
		StartBlock:      resp.StartBlock,
		EndBlock:        resp.EndBlock,
		LocalExitRoot:   common.Hash(resp.LocalExitRootHash.Value),
		CustomChainData: resp.CustomChainData,
	}, nil
}
