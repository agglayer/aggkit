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
		l1InfoTreeMerkleProof treeTypes.Proof,
		gerLeaves map[common.Hash]*types.GerLeaf,
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
	l1InfoTreeMerkleProof treeTypes.Proof,
	gerLeaves map[common.Hash]*types.GerLeaf,
	importedBridgeExits []*agglayer.ImportedBridgeExit,
) (*types.AggchainProof, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*TIMEOUT)
	defer cancel()

	convertedMerkleProof := make([][]byte, treeTypes.DefaultHeight)
	for i := 0; i < int(treeTypes.DefaultHeight); i++ {
		convertedMerkleProof[i] = l1InfoTreeMerkleProof[i].Bytes()
	}

	convertedGerLeaves := make(map[string]*types.GerLeaf)
	for k, v := range gerLeaves {
		convertedGerLeaves[k.String()] = v
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

	convertedImportedBridgeExits := make([]*types.ImportedBridgeExit, len(importedBridgeExits))
	for i, importedBridgeExit := range importedBridgeExits {
		convertedBridgeExit := &types.BridgeExit{
			LeafType: types.LeafType(importedBridgeExit.BridgeExit.LeafType),
			TokenInfo: &types.TokenInfo{
				OriginNetwork:      importedBridgeExit.BridgeExit.TokenInfo.OriginNetwork,
				OriginTokenAddress: importedBridgeExit.BridgeExit.TokenInfo.OriginTokenAddress.Bytes(),
			},
			DestinationNetwork: importedBridgeExit.BridgeExit.DestinationNetwork,
			DestinationAddress: importedBridgeExit.BridgeExit.DestinationAddress.Bytes(),
			Amount:             importedBridgeExit.BridgeExit.Amount.String(),
			IsMetadataHashed:   importedBridgeExit.BridgeExit.IsMetadataHashed,
			Metadata:           importedBridgeExit.BridgeExit.Metadata,
		}
		convertedGlobalIndex := &types.GlobalIndex{
			MainnetFlag: importedBridgeExit.GlobalIndex.MainnetFlag,
			RollupIndex: importedBridgeExit.GlobalIndex.RollupIndex,
			LeafIndex:   importedBridgeExit.GlobalIndex.LeafIndex,
		}
		convertedImportedBridgeExits[i] = &types.ImportedBridgeExit{
			BridgeExit:  convertedBridgeExit,
			GlobalIndex: convertedGlobalIndex,
		}
	}

	resp, err := c.client.GenerateAggchainProof(ctx, &types.GenerateAggchainProofRequest{
		StartBlock:            startBlock,
		MaxEndBlock:           maxEndBlock,
		L1InfoTreeRootHash:    l1InfoTreeRootHash.Bytes(),
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
		LocalExitRoot:   common.BytesToHash(resp.LocalExitRootHash),
		CustomChainData: resp.CustomChainData,
	}, nil
}
