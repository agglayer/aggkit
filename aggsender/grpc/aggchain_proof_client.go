package grpc

import (
	"context"
	"time"

	agglayerProtobuf "buf.build/gen/go/agglayer/agglayer/protocolbuffers/go/agglayer/protocol/types/v1"
	aggkitGrpc "buf.build/gen/go/agglayer/provers/grpc/go/aggkit/prover/v1/proverv1grpc"
	aggkitProtobuf "buf.build/gen/go/agglayer/provers/protocolbuffers/go/aggkit/prover/v1"
	agglayer "github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
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
	client aggkitGrpc.AggchainProofServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAggchainProofClient(serverAddr string) (*AggchainProofClient, error) {
	grpcClient, err := NewClient(serverAddr)
	if err != nil {
		return nil, err
	}
	return &AggchainProofClient{
		client: aggkitGrpc.NewAggchainProofServiceClient(grpcClient.conn),
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

	convertedL1InfoTreeLeaf := &agglayerProtobuf.L1InfoTreeLeafWithContext{
		Inner: &agglayerProtobuf.L1InfoTreeLeaf{
			GlobalExitRoot: &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeLeaf.GlobalExitRoot[:]},
			BlockHash:      &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeLeaf.Hash[:]},
			Timestamp:      l1InfoTreeLeaf.Timestamp,
		},
		Mer:             &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeLeaf.MainnetExitRoot[:]},
		Rer:             &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeLeaf.RollupExitRoot[:]},
		L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
	}

	convertedMerkleProofSiblings := make([]*agglayerProtobuf.FixedBytes32, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProofSiblings[i] = &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeMerkleProof.Proof[i][:]}
	}
	convertedMerkleProof := &agglayerProtobuf.MerkleProof{
		Root:     &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeMerkleProof.Root[:]},
		Siblings: convertedMerkleProofSiblings,
	}

	convertedGerLeaves := make(map[string]*agglayerProtobuf.ClaimFromMainnet, 0)
	for k, v := range gerLeaves {
		convertedProofLeafMerSiblings := make([]*agglayerProtobuf.FixedBytes32, treeTypes.DefaultHeight)
		for i := 0; i < int(treeTypes.DefaultHeight); i++ {
			convertedProofLeafMerSiblings[i] = &agglayerProtobuf.FixedBytes32{Value: v.ProofLeafMER.Proof[i][:]}
		}
		convertedProofGerL1RootSiblings := make([]*agglayerProtobuf.FixedBytes32, treeTypes.DefaultHeight)
		for i := 0; i < int(treeTypes.DefaultHeight); i++ {
			convertedProofGerL1RootSiblings[i] = &agglayerProtobuf.FixedBytes32{Value: v.ProofLeafMER.Proof[i][:]}
		}
		convertedGerLeaves[k.String()] = &agglayerProtobuf.ClaimFromMainnet{
			ProofLeafMer: &agglayerProtobuf.MerkleProof{
				Root:     &agglayerProtobuf.FixedBytes32{Value: v.ProofLeafMER.Root[:]},
				Siblings: convertedProofLeafMerSiblings,
			},
			ProofGerL1Root: &agglayerProtobuf.MerkleProof{
				Root:     &agglayerProtobuf.FixedBytes32{Value: v.ProofGERToL1Root.Root[:]},
				Siblings: convertedProofGerL1RootSiblings,
			},
			L1Leaf: &agglayerProtobuf.L1InfoTreeLeafWithContext{
				L1InfoTreeIndex: v.L1Leaf.L1InfoTreeIndex,
				Rer:             &agglayerProtobuf.FixedBytes32{Value: v.L1Leaf.RollupExitRoot[:]},
				Mer:             &agglayerProtobuf.FixedBytes32{Value: v.L1Leaf.MainnetExitRoot[:]},
				Inner: &agglayerProtobuf.L1InfoTreeLeaf{
					GlobalExitRoot: &agglayerProtobuf.FixedBytes32{Value: v.L1Leaf.Inner.GlobalExitRoot[:]},
					BlockHash:      &agglayerProtobuf.FixedBytes32{Value: v.L1Leaf.Inner.BlockHash[:]},
					Timestamp:      v.L1Leaf.Inner.Timestamp,
				},
			},
		}
	}

	convertedImportedBridgeExits := make([]*agglayerProtobuf.ImportedBridgeExit, len(importedBridgeExits))
	for i, importedBridgeExit := range importedBridgeExits {
		convertedBridgeExit := &agglayerProtobuf.BridgeExit{
			LeafType: agglayerProtobuf.LeafType(importedBridgeExit.BridgeExit.LeafType),
			TokenInfo: &agglayerProtobuf.TokenInfo{
				OriginNetwork: importedBridgeExit.BridgeExit.TokenInfo.OriginNetwork,
				OriginTokenAddress: &agglayerProtobuf.FixedBytes20{
					Value: importedBridgeExit.BridgeExit.TokenInfo.OriginTokenAddress[:],
				},
			},
			DestNetwork: importedBridgeExit.BridgeExit.DestinationNetwork,
			DestAddress: &agglayerProtobuf.FixedBytes20{Value: importedBridgeExit.BridgeExit.DestinationAddress[:]},
			Amount:      &agglayerProtobuf.FixedBytes32{Value: importedBridgeExit.BridgeExit.Amount.Bytes()},
			Metadata:    &agglayerProtobuf.FixedBytes32{Value: importedBridgeExit.BridgeExit.Metadata},
		}
		convertedGlobalIndex := &agglayerProtobuf.FixedBytes32{
			Value: bridgesync.GenerateGlobalIndex(
				importedBridgeExit.GlobalIndex.MainnetFlag,
				importedBridgeExit.GlobalIndex.RollupIndex,
				importedBridgeExit.GlobalIndex.LeafIndex,
			).Bytes(),
		}
		convertedImportedBridgeExits[i] = &agglayerProtobuf.ImportedBridgeExit{
			BridgeExit:  convertedBridgeExit,
			GlobalIndex: convertedGlobalIndex,
		}
	}

	resp, err := c.client.GenerateAggchainProof(ctx, &aggkitProtobuf.GenerateAggchainProofRequest{
		StartBlock:            startBlock,
		MaxEndBlock:           maxEndBlock,
		L1InfoTreeRootHash:    &agglayerProtobuf.FixedBytes32{Value: l1InfoTreeRootHash.Bytes()},
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
