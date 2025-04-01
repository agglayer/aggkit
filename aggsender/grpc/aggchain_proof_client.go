package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agglayerInteropTypesV1Proto "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	aggkitProverV1Grpc "buf.build/gen/go/agglayer/provers/grpc/go/aggkit/prover/v1/proverv1grpc"
	aggkitProverV1Proto "buf.build/gen/go/agglayer/provers/protocolbuffers/go/aggkit/prover/v1"
	agglayer "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitCommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// AggchainProofClientInterface defines an interface for aggchain proof client
type AggchainProofClientInterface interface {
	GenerateAggchainProof(
		lastProvenBlock uint64,
		requestedEndBlock uint64,
		l1InfoTreeRootHash common.Hash,
		l1InfoTreeLeaf l1infotreesync.L1InfoTreeLeaf,
		l1InfoTreeMerkleProof agglayer.MerkleProof,
		gerLeavesWithBlockNumber map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber,
		importedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber,
	) (*types.AggchainProof, error)
}

// AggchainProofClient provides an implementation for the AggchainProofClient interface
type AggchainProofClient struct {
	client aggkitProverV1Grpc.AggchainProofServiceClient

	generateAggchainProofTimeout time.Duration
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAggchainProofClient(serverAddr string,
	generateProofTimeout time.Duration) (*AggchainProofClient, error) {
	addr := strings.TrimPrefix(serverAddr, "http://")
	grpcClient, err := aggkitCommon.NewClient(addr)
	if err != nil {
		return nil, err
	}
	return &AggchainProofClient{
		generateAggchainProofTimeout: generateProofTimeout,
		client:                       aggkitProverV1Grpc.NewAggchainProofServiceClient(grpcClient.Conn()),
	}, nil
}

func (c *AggchainProofClient) GenerateAggchainProof(
	lastProvenBlock uint64,
	requestedEndBlock uint64,
	l1InfoTreeRootHash common.Hash,
	l1InfoTreeLeaf l1infotreesync.L1InfoTreeLeaf,
	l1InfoTreeMerkleProof agglayer.MerkleProof,
	gerLeavesWithBlockNumber map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber,
	importedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber,
) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.generateAggchainProofTimeout)
	defer cancel()

	convertedL1InfoTreeLeaf := &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
		Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
			GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.GlobalExitRoot[:]},
			BlockHash:      &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.PreviousBlockHash[:]},
			Timestamp:      l1InfoTreeLeaf.Timestamp,
		},
		Mer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.MainnetExitRoot[:]},
		Rer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeLeaf.RollupExitRoot[:]},
		L1InfoTreeIndex: l1InfoTreeLeaf.L1InfoTreeIndex,
	}

	convertedMerkleProofSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, treetypes.DefaultHeight)
	for i := 0; i < int(treetypes.DefaultHeight); i++ {
		convertedMerkleProofSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeMerkleProof.Proof[i][:]}
	}
	convertedMerkleProof := &agglayerInteropTypesV1Proto.MerkleProof{
		Root:     &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeMerkleProof.Root[:]},
		Siblings: convertedMerkleProofSiblings,
	}

	convertedGerLeaves := make(map[string]*aggkitProverV1Proto.ProvenInsertedGERWithBlockNumber, 0)
	for k, v := range gerLeavesWithBlockNumber {
		convertedProofGerL1RootSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, treetypes.DefaultHeight)
		for i := 0; i < int(treetypes.DefaultHeight); i++ {
			convertedProofGerL1RootSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: v.ProvenInsertedGERLeaf.ProofGERToL1Root.Proof[i][:],
			}
		}
		convertedGerLeaves[k.String()] = &aggkitProverV1Proto.ProvenInsertedGERWithBlockNumber{
			BlockNumber: v.BlockNumber,
			ProvenInsertedGer: &aggkitProverV1Proto.ProvenInsertedGER{
				ProofGerL1Root: &agglayerInteropTypesV1Proto.MerkleProof{
					Root:     &agglayerInteropTypesV1Proto.FixedBytes32{Value: v.ProvenInsertedGERLeaf.ProofGERToL1Root.Root[:]},
					Siblings: convertedProofGerL1RootSiblings,
				},
				L1Leaf: &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
					L1InfoTreeIndex: v.ProvenInsertedGERLeaf.L1Leaf.L1InfoTreeIndex,
					Rer: &agglayerInteropTypesV1Proto.FixedBytes32{
						Value: v.ProvenInsertedGERLeaf.L1Leaf.RollupExitRoot[:],
					},
					Mer: &agglayerInteropTypesV1Proto.FixedBytes32{
						Value: v.ProvenInsertedGERLeaf.L1Leaf.MainnetExitRoot[:],
					},
					Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
						GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{
							Value: v.ProvenInsertedGERLeaf.L1Leaf.Inner.GlobalExitRoot[:],
						},
						BlockHash: &agglayerInteropTypesV1Proto.FixedBytes32{
							Value: v.ProvenInsertedGERLeaf.L1Leaf.Inner.BlockHash[:],
						},
						Timestamp: v.ProvenInsertedGERLeaf.L1Leaf.Inner.Timestamp,
					},
				},
			},
		}
	}

	convertedImportedBridgeExitsWithBlockNumber := make([]*aggkitProverV1Proto.ImportedBridgeExitWithBlockNumber,
		len(importedBridgeExitsWithBlockNumber))
	for i, importedBridgeExitWithBlockNumber := range importedBridgeExitsWithBlockNumber {
		convertedImportedBridgeExit := &agglayerInteropTypesV1Proto.ImportedBridgeExit{
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

		if importedBridgeExitWithBlockNumber.ImportedBridgeExit.ClaimData != nil {
			switch c := importedBridgeExitWithBlockNumber.ImportedBridgeExit.ClaimData.(type) {
			case *agglayer.ClaimFromMainnnet:
				convertedImportedBridgeExit.Claim = &agglayerInteropTypesV1Proto.ImportedBridgeExit_Mainnet{
					Mainnet: &agglayerInteropTypesV1Proto.ClaimFromMainnet{
						ProofLeafMer: &agglayerInteropTypesV1Proto.MerkleProof{
							Root: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.ProofLeafMER.Root.Bytes(),
							},
							Siblings: convertToProtoSiblings(c.ProofLeafMER.Proof),
						},
						ProofGerL1Root: &agglayerInteropTypesV1Proto.MerkleProof{
							Root: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.ProofGERToL1Root.Root.Bytes(),
							},
							Siblings: convertToProtoSiblings(c.ProofGERToL1Root.Proof),
						},
						L1Leaf: &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
							L1InfoTreeIndex: c.L1Leaf.L1InfoTreeIndex,
							Rer: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.L1Leaf.RollupExitRoot.Bytes(),
							},
							Mer: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.L1Leaf.MainnetExitRoot.Bytes(),
							},
							Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
								GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{
									Value: c.L1Leaf.Inner.GlobalExitRoot.Bytes(),
								},
								BlockHash: &agglayerInteropTypesV1Proto.FixedBytes32{
									Value: c.L1Leaf.Inner.BlockHash.Bytes(),
								},
								Timestamp: c.L1Leaf.Inner.Timestamp,
							},
						},
					},
				}
			case *agglayer.ClaimFromRollup:
				convertedImportedBridgeExit.Claim = &agglayerInteropTypesV1Proto.ImportedBridgeExit_Rollup{
					Rollup: &agglayerInteropTypesV1Proto.ClaimFromRollup{
						ProofLeafLer: &agglayerInteropTypesV1Proto.MerkleProof{
							Root: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.ProofLeafLER.Root.Bytes(),
							},
							Siblings: convertToProtoSiblings(c.ProofLeafLER.Proof),
						},
						ProofLerRer: &agglayerInteropTypesV1Proto.MerkleProof{
							Root: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.ProofLERToRER.Root.Bytes(),
							},
							Siblings: convertToProtoSiblings(c.ProofLERToRER.Proof),
						},
						ProofGerL1Root: &agglayerInteropTypesV1Proto.MerkleProof{
							Root: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.ProofGERToL1Root.Root.Bytes(),
							},
							Siblings: convertToProtoSiblings(c.ProofGERToL1Root.Proof),
						},
						L1Leaf: &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
							L1InfoTreeIndex: c.L1Leaf.L1InfoTreeIndex,
							Rer: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.L1Leaf.RollupExitRoot.Bytes(),
							},
							Mer: &agglayerInteropTypesV1Proto.FixedBytes32{
								Value: c.L1Leaf.MainnetExitRoot.Bytes(),
							},
							Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
								GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{
									Value: c.L1Leaf.Inner.GlobalExitRoot.Bytes(),
								},
								BlockHash: &agglayerInteropTypesV1Proto.FixedBytes32{
									Value: c.L1Leaf.Inner.BlockHash.Bytes(),
								},
								Timestamp: c.L1Leaf.Inner.Timestamp,
							},
						},
					},
				}
			default:
				return nil, fmt.Errorf("unsupported claim data type: %T", c)
			}
		}

		convertedImportedBridgeExitsWithBlockNumber[i] = &aggkitProverV1Proto.ImportedBridgeExitWithBlockNumber{
			ImportedBridgeExit: convertedImportedBridgeExit,
			BlockNumber:        importedBridgeExitWithBlockNumber.BlockNumber,
		}
	}

	request := &aggkitProverV1Proto.GenerateAggchainProofRequest{
		LastProvenBlock:       lastProvenBlock,
		RequestedEndBlock:     requestedEndBlock,
		L1InfoTreeRootHash:    &agglayerInteropTypesV1Proto.FixedBytes32{Value: l1InfoTreeRootHash.Bytes()},
		L1InfoTreeLeaf:        convertedL1InfoTreeLeaf,
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerLeaves:             convertedGerLeaves,
		ImportedBridgeExits:   convertedImportedBridgeExitsWithBlockNumber,
	}

	// this is just to log the request to log for debugging purposes
	raw, err := json.Marshal(request)
	if err == nil {
		log.Debug("GenerateAggchainProof inputs:")
		log.Debug(string(raw))
	} else {
		log.Errorf("Failed to marshal GenerateAggchainProof request: %v", err)
	}

	resp, err := c.client.GenerateAggchainProof(ctx, request)
	if err != nil {
		return nil, err
	}

	return &types.AggchainProof{
		Proof:           resp.AggchainProof,
		LastProvenBlock: resp.LastProvenBlock,
		EndBlock:        resp.EndBlock,
		LocalExitRoot:   common.Hash(resp.LocalExitRootHash.Value),
		CustomChainData: resp.CustomChainData,
	}, nil
}

// convertToProtoSiblings converts a slice of hashes to a slice of proto fixed bytes 32
func convertToProtoSiblings(siblings treetypes.Proof) []*agglayerInteropTypesV1Proto.FixedBytes32 {
	protoSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, len(siblings))

	for i, sibling := range siblings {
		protoSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{
			Value: sibling.Bytes(),
		}
	}

	return protoSiblings
}
