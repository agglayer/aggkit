package grpc

import (
	"errors"
	"testing"

	agglayer "github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockAggchainProofServiceClient is a mock implementation of the AggchainProofServiceClient
type MockAggchainProofServiceClient struct {
	mock.Mock
}

func TestGenerateAggchainProof_Success(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedResponse := &types.GenerateAggchainProofResponse{
		AggchainProof:     []byte("dummy-proof"),
		StartBlock:        100,
		EndBlock:          200,
		LocalExitRootHash: &types.FixedBytes32{Value: common.Hash{}.Bytes()},
		CustomChainData:   []byte{},
	}

	mockClient.On("GenerateAggchainProof", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	result, err := client.GenerateAggchainProof(
		100,
		200,
		common.Hash{},
		l1infotreesync.L1InfoTreeLeaf{},
		agglayer.MerkleProof{Root: common.Hash{}, Proof: [32]common.Hash{}},
		nil,
		nil,
	)

	assert.NoError(t, err)
	assert.Equal(t, []byte("dummy-proof"), result.Proof)
	assert.Equal(t, uint64(100), result.StartBlock)
	assert.Equal(t, uint64(200), result.EndBlock)
	assert.Equal(t, common.Hash{}, result.LocalExitRoot)
	assert.Equal(t, []byte{}, result.CustomChainData)
	mockClient.AssertExpectations(t)
}

func TestGenerateAggchainProof_Error(t *testing.T) {
	mockClient := mocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedError := errors.New("Generate error")

	mockClient.On("GenerateAggchainProof", mock.Anything, mock.Anything).Return((*types.GenerateAggchainProofResponse)(nil), expectedError)

	result, err := client.GenerateAggchainProof(
		300,
		400,
		common.BytesToHash([]byte("0x")),
		l1infotreesync.L1InfoTreeLeaf{
			BlockNumber: 1,
			Hash:        common.HexToHash("0x2"),
		},
		agglayer.MerkleProof{
			Root:  common.HexToHash("0x3"),
			Proof: [32]common.Hash{common.HexToHash("0x4")},
		},
		map[common.Hash]*agglayer.ClaimFromMainnnet{
			common.HexToHash("0x5"): {
				ProofLeafMER: &agglayer.MerkleProof{
					Root:  common.HexToHash("0x6"),
					Proof: [32]common.Hash{common.HexToHash("0x7")},
				},
				ProofGERToL1Root: &agglayer.MerkleProof{
					Root:  common.HexToHash("0x8"),
					Proof: [32]common.Hash{common.HexToHash("0x9")},
				},
				L1Leaf: &agglayer.L1InfoTreeLeaf{
					Inner: &agglayer.L1InfoTreeLeafInner{
						GlobalExitRoot: common.HexToHash("0xa"),
						BlockHash:      common.HexToHash("0xb"),
						Timestamp:      1,
					},
					L1InfoTreeIndex: 4,
					MainnetExitRoot: common.HexToHash("0xb"),
					RollupExitRoot:  common.HexToHash("0xc"),
				},
			},
		},
		[]*agglayer.ImportedBridgeExit{
			{
				BridgeExit: &agglayer.BridgeExit{
					LeafType:           1,
					DestinationNetwork: 1,
					DestinationAddress: common.HexToAddress("0x1"),
					Amount:             common.Big1,
					Metadata:           []byte("metadata"),
					IsMetadataHashed:   false,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x2"),
					},
				},
				GlobalIndex: &agglayer.GlobalIndex{
					MainnetFlag: true,
					RollupIndex: 1,
					LeafIndex:   1,
				},
			},
		},
	)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "Generate error", err.Error())
	mockClient.AssertExpectations(t)
}
