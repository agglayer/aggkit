package grpc

import (
	"context"
	"time"

	agglayerInteropTypesV1Proto "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	aggkitProverV1Grpc "buf.build/gen/go/agglayer/provers/grpc/go/aggkit/prover/v1/proverv1grpc"
	aggkitProverV1Proto "buf.build/gen/go/agglayer/provers/protocolbuffers/go/aggkit/prover/v1"
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
		gerLeavesWithBlockNumber map[common.Hash]*agglayer.InsertedGERWithBlockNumber,
		importedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber,
	) (*types.AggchainProof, error)
}

// AggchainProofClient provides an implementation for the AggchainProofClient interface
type AggchainProofClient struct {
	client aggkitProverV1Grpc.AggchainProofServiceClient
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAggchainProofClient(serverAddr string) (*AggchainProofClient, error) {
	grpcClient, err := NewClient(serverAddr)
	if err != nil {
		return nil, err
	}
	return &AggchainProofClient{
		client: aggkitProverV1Grpc.NewAggchainProofServiceClient(grpcClient.conn),
	}, nil
}

func (c *AggchainProofClient) GenerateAggchainProof(
	startBlock uint64,
	maxEndBlock uint64,
	l1InfoTreeRootHash common.Hash,
	l1InfoTreeLeaf l1infotreesync.L1InfoTreeLeaf,
	l1InfoTreeMerkleProof agglayer.MerkleProof,
	gerLeavesWithBlockNumber map[common.Hash]*agglayer.InsertedGERWithBlockNumber,
	importedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber,
) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*TIMEOUT)
	defer cancel()

	convertedL1InfoTreeLeaf := &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
		Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
			GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.GlobalExitRoot[:]},
			BlockHash:      &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.Hash[:]},
			Timestamp:      l1InfoTreeLeaf.Timestamp,
		},
		Mer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.MainnetExitRoot[:]},
		Rer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.RollupExitRoot[:]},
		L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
	}

	convertedMerkleProofSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProofSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeMerkleProof.Proof[i][:]}
	}
	convertedMerkleProof := &agglayerInteropTypesV1Proto.MerkleProof{
		Root:     &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeMerkleProof.Root[:]},
		Siblings: convertedMerkleProofSiblings,
	}

	convertedGerLeaves := make(map[string]*aggkitProverV1Proto.InsertedGERWithBlockNumber, 0)
	for k, v := range gerLeavesWithBlockNumber {
		convertedProofGerL1RootSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, treeTypes.DefaultHeight)
		for i := 0; i < int(treeTypes.DefaultHeight); i++ {
			convertedProofGerL1RootSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: v.InsertedGerLeaf.ProofGERToL1Root.Proof[i][:],
			}
		}
		convertedGerLeaves[k.String()] = &aggkitProverV1Proto.InsertedGERWithBlockNumber{
			BlockNumber: v.BlockNumber,
			InsertedGerLeaf: &aggkitProverV1Proto.InsertedGER{
				ProofGerL1Root: &agglayerInteropTypesV1Proto.MerkleProof{
					Root:     &agglayerInteropTypesV1Proto.FixedBytes32{Value: v.InsertedGerLeaf.ProofGERToL1Root.Root[:]},
					Siblings: convertedProofGerL1RootSiblings,
				},
				L1Leaf: &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
					L1InfoTreeIndex: v.InsertedGerLeaf.L1Leaf.L1InfoTreeIndex,
					Rer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: v.InsertedGerLeaf.L1Leaf.RollupExitRoot[:]},
					Mer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: v.InsertedGerLeaf.L1Leaf.MainnetExitRoot[:]},
					Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
						GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{
							Value: v.InsertedGerLeaf.L1Leaf.Inner.GlobalExitRoot[:],
						},
						BlockHash: &agglayerInteropTypesV1Proto.FixedBytes32{
							Value: v.InsertedGerLeaf.L1Leaf.Inner.BlockHash[:],
						},
						Timestamp: v.InsertedGerLeaf.L1Leaf.Inner.Timestamp,
					},
				},
			},
		}
	}

	convertedImportedBridgeExitsWithBlockNumber := make([]*aggkitProverV1Proto.ImportedBridgeExitWithBlockNumber,
		len(importedBridgeExitsWithBlockNumber))
	for i, importedBridgeExitWithBlockNumber := range importedBridgeExitsWithBlockNumber {
		convertedBridgeExit := &agglayerInteropTypesV1Proto.ImportedBridgeExit{
			BridgeExit: &agglayerInteropTypesV1Proto.BridgeExit{
				LeafType: agglayerInteropTypesV1Proto.LeafType(
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.LeafType),
				TokenInfo: &agglayerInteropTypesV1Proto.TokenInfo{
					OriginNetwork: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.TokenInfo.OriginNetwork,
					OriginTokenAddress: &agglayerInteropTypesV1Proto.FixedBytes20{
						Value: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.TokenInfo.OriginTokenAddress[:],
					},
				},
				DestNetwork: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.DestinationNetwork,
				DestAddress: &agglayerInteropTypesV1Proto.FixedBytes20{
					Value: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.DestinationAddress[:],
				},
				Amount: &agglayerInteropTypesV1Proto.FixedBytes32{
					Value: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.Amount.Bytes(),
				},
				Metadata: &agglayerInteropTypesV1Proto.FixedBytes32{
					Value: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.Metadata,
				},
			},
			GlobalIndex: &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: bridgesync.GenerateGlobalIndex(
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.GlobalIndex.MainnetFlag,
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.GlobalIndex.RollupIndex,
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.GlobalIndex.LeafIndex,
				).Bytes(),
			},
		}
		convertedImportedBridgeExitsWithBlockNumber[i] = &aggkitProverV1Proto.ImportedBridgeExitWithBlockNumber{
			ImportedBridgeExit: convertedBridgeExit,
			BlockNumber:        importedBridgeExitWithBlockNumber.BlockNumber,
		}
	}

	resp, err := c.client.GenerateAggchainProof(ctx, &aggkitProverV1Proto.GenerateAggchainProofRequest{
		StartBlock:            startBlock,
		MaxEndBlock:           maxEndBlock,
		L1InfoTreeRootHash:    &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeRootHash.Bytes()},
		L1InfoTreeLeaf:        convertedL1InfoTreeLeaf,
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerLeaves:             convertedGerLeaves,
		ImportedBridgeExits:   convertedImportedBridgeExitsWithBlockNumber,
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
