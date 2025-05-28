package grpc

import (
	"context"
	"errors"
	"strings"
	"time"

	agglayerInteropTypesV1Proto "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	aggkitProverV1Grpc "buf.build/gen/go/agglayer/provers/grpc/go/aggkit/prover/v1/proverv1grpc"
	aggkitProverV1Proto "buf.build/gen/go/agglayer/provers/protocolbuffers/go/aggkit/prover/v1"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

var errProofNotSP1Stark = errors.New("aggchain proof is not SP1Stark")

// AggchainProofClient provides an implementation for the AggchainProofClient interface
type AggchainProofClient struct {
	client aggkitProverV1Grpc.AggchainProofServiceClient

	generateAggchainProofTimeout time.Duration
}

// NewAggchainProofClient initializes a new AggchainProof instance
func NewAggchainProofClient(serverAddr string,
	generateProofTimeout time.Duration, useTLS bool) (*AggchainProofClient, error) {
	addr := strings.TrimPrefix(serverAddr, "http://")
	grpcClient, err := aggkitcommon.NewClient(addr, useTLS)
	if err != nil {
		return nil, err
	}
	return &AggchainProofClient{
		generateAggchainProofTimeout: generateProofTimeout,
		client:                       aggkitProverV1Grpc.NewAggchainProofServiceClient(grpcClient.Conn()),
	}, nil
}

func (c *AggchainProofClient) GenerateAggchainProof(req *types.AggchainProofRequest) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.generateAggchainProofTimeout)
	defer cancel()
	request := convertAggchainProofRequestToGrpcRequest(req)
	resp, err := c.client.GenerateAggchainProof(ctx, request)
	if err != nil {
		return nil, aggkitcommon.RepackGRPCErrorWithDetails(err)
	}

	proof, ok := resp.AggchainProof.Proof.(*agglayerInteropTypesV1Proto.AggchainProof_Sp1Stark)
	if !ok {
		return nil, errProofNotSP1Stark
	}

	return &types.AggchainProof{
		SP1StarkProof: &types.SP1StarkProof{
			Proof:   proof.Sp1Stark.Proof,
			Vkey:    proof.Sp1Stark.Vkey,
			Version: proof.Sp1Stark.Version,
		},
		LastProvenBlock: resp.LastProvenBlock,
		EndBlock:        resp.EndBlock,
		LocalExitRoot:   common.BytesToHash(resp.LocalExitRootHash.Value),
		CustomChainData: resp.CustomChainData,
		AggchainParams:  common.BytesToHash(resp.AggchainProof.AggchainParams.Value),
		Context:         resp.AggchainProof.Context,
	}, nil
}

func (c *AggchainProofClient) GenerateOptimisticAggchainProof(req *types.AggchainProofRequest, signature []byte) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.generateAggchainProofTimeout)
	defer cancel()
	request := &aggkitProverV1Proto.GenerateOptimisticAggchainProofRequest{
		AggchainProofRequest: convertAggchainProofRequestToGrpcRequest(req),
	}
	resp, err := c.client.GenerateOptimisticAggchainProof(ctx, request)
	if err != nil {
		return nil, aggkitcommon.RepackGRPCErrorWithDetails(err)
	}

	proof, ok := resp.AggchainProof.Proof.(*agglayerInteropTypesV1Proto.AggchainProof_Sp1Stark)
	if !ok {
		return nil, errProofNotSP1Stark
	}

	return &types.AggchainProof{
		SP1StarkProof: &types.SP1StarkProof{
			Proof:   proof.Sp1Stark.Proof,
			Vkey:    proof.Sp1Stark.Vkey,
			Version: proof.Sp1Stark.Version,
		},
		LastProvenBlock: request.AggchainProofRequest.LastProvenBlock,
		EndBlock:        request.AggchainProofRequest.RequestedEndBlock,
		LocalExitRoot:   common.BytesToHash(resp.LocalExitRootHash.Value),
		CustomChainData: resp.CustomChainData,
		AggchainParams:  common.BytesToHash(resp.AggchainProof.AggchainParams.Value),
		Context:         resp.AggchainProof.Context,
	}, nil

}

func convertAggchainProofRequestToGrpcRequest(req *types.AggchainProofRequest) *aggkitProverV1Proto.GenerateAggchainProofRequest {
	convertedL1InfoTreeLeaf := &agglayerInteropTypesV1Proto.L1InfoTreeLeafWithContext{
		Inner: &agglayerInteropTypesV1Proto.L1InfoTreeLeaf{
			GlobalExitRoot: &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeLeaf.GlobalExitRoot[:]},
			BlockHash:      &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeLeaf.PreviousBlockHash[:]},
			Timestamp:      req.L1InfoTreeLeaf.Timestamp,
		},
		Mer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeLeaf.MainnetExitRoot[:]},
		Rer:             &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeLeaf.RollupExitRoot[:]},
		L1InfoTreeIndex: req.L1InfoTreeLeaf.L1InfoTreeIndex,
	}

	convertedMerkleProofSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, treetypes.DefaultHeight)
	for i := 0; i < int(treetypes.DefaultHeight); i++ {
		convertedMerkleProofSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeMerkleProof.Proof[i][:]}
	}
	convertedMerkleProof := &agglayerInteropTypesV1Proto.MerkleProof{
		Root:     &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeMerkleProof.Root[:]},
		Siblings: convertedMerkleProofSiblings,
	}

	convertedGerLeaves := make(map[string]*aggkitProverV1Proto.ProvenInsertedGERWithBlockNumber, 0)
	for k, v := range req.GERLeavesWithBlockNumber {
		convertedProofGerL1RootSiblings := make([]*agglayerInteropTypesV1Proto.FixedBytes32, treetypes.DefaultHeight)
		for i := 0; i < int(treetypes.DefaultHeight); i++ {
			convertedProofGerL1RootSiblings[i] = &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: v.ProvenInsertedGERLeaf.ProofGERToL1Root.Proof[i][:],
			}
		}
		convertedGerLeaves[k.String()] = &aggkitProverV1Proto.ProvenInsertedGERWithBlockNumber{
			BlockNumber: v.BlockNumber,
			BlockIndex:  uint64(v.BlockIndex),
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
		len(req.ImportedBridgeExitsWithBlockNumber))
	for i, importedBridgeExitWithBlockNumber := range req.ImportedBridgeExitsWithBlockNumber {
		convertedImportedBridgeExitsWithBlockNumber[i] = &aggkitProverV1Proto.ImportedBridgeExitWithBlockNumber{
			BlockNumber: importedBridgeExitWithBlockNumber.BlockNumber,
			GlobalIndex: &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: common.BigToHash(bridgesync.GenerateGlobalIndex(
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.GlobalIndex.MainnetFlag,
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.GlobalIndex.RollupIndex,
					importedBridgeExitWithBlockNumber.ImportedBridgeExit.GlobalIndex.LeafIndex,
				)).Bytes(),
			},
			BridgeExitHash: &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: importedBridgeExitWithBlockNumber.ImportedBridgeExit.BridgeExit.Hash().Bytes(),
			},
		}
	}

	request := &aggkitProverV1Proto.GenerateAggchainProofRequest{
		LastProvenBlock:       req.LastProvenBlock,
		RequestedEndBlock:     req.RequestedEndBlock,
		L1InfoTreeRootHash:    &agglayerInteropTypesV1Proto.FixedBytes32{Value: req.L1InfoTreeRootHash.Bytes()},
		L1InfoTreeLeaf:        convertedL1InfoTreeLeaf,
		L1InfoTreeMerkleProof: convertedMerkleProof,
		GerLeaves:             convertedGerLeaves,
		ImportedBridgeExits:   convertedImportedBridgeExitsWithBlockNumber,
	}

	resp, err := c.client.GenerateAggchainProof(ctx, request)
	if err != nil {
		return nil, aggkitcommon.RepackGRPCErrorWithDetails(err)
	}

	proof, ok := resp.AggchainProof.Proof.(*agglayerInteropTypesV1Proto.AggchainProof_Sp1Stark)
	if !ok {
		log.Errorf("aggchain proof is not SP1Stark: %+v", resp.AggchainProof.Proof)
		return nil, errProofNotSP1Stark
	}

	return &types.AggchainProof{
		SP1StarkProof: &types.SP1StarkProof{
			Proof:   proof.Sp1Stark.Proof,
			Vkey:    proof.Sp1Stark.Vkey,
			Version: proof.Sp1Stark.Version,
		},
		LastProvenBlock: resp.LastProvenBlock,
		EndBlock:        resp.EndBlock,
		LocalExitRoot:   common.BytesToHash(resp.LocalExitRootHash.Value),
		CustomChainData: resp.CustomChainData,
		AggchainParams:  common.BytesToHash(resp.AggchainProof.AggchainParams.Value),
		Context:         resp.AggchainProof.Context,
	}, nil
}
