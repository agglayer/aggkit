package aggchainproofclient

import (
	"errors"
	"testing"

	agglayerInteropTypesV1Proto "buf.build/gen/go/agglayer/interop/protocolbuffers/go/agglayer/interop/types/v1"
	aggkitProverV1Proto "buf.build/gen/go/agglayer/provers/protocolbuffers/go/aggkit/prover/v1"
	agglayer "github.com/agglayer/aggkit/agglayer/types"
	aggkitProverMocks "github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestGenerateAggchainProof_Success(t *testing.T) {
	mockClient := aggkitProverMocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedResponse := &aggkitProverV1Proto.GenerateAggchainProofResponse{
		AggchainProof: &agglayerInteropTypesV1Proto.AggchainProof{
			AggchainParams: &agglayerInteropTypesV1Proto.FixedBytes32{
				Value: common.HexToHash("0x1").Bytes(),
			},
			Context: map[string][]byte{
				"key1": []byte("value1"),
			},
			Proof: &agglayerInteropTypesV1Proto.AggchainProof_Sp1Stark{
				Sp1Stark: &agglayerInteropTypesV1Proto.SP1StarkProof{
					Version: "0.1",
					Proof:   []byte("dummy-proof"),
					Vkey:    []byte("dummy-vkey"),
				},
			},
		},
		LastProvenBlock:   100,
		EndBlock:          200,
		LocalExitRootHash: &agglayerInteropTypesV1Proto.FixedBytes32{Value: common.Hash{}.Bytes()},
		CustomChainData:   []byte{},
	}

	mockClient.On("GenerateAggchainProof", mock.Anything, mock.Anything).Return(expectedResponse, nil)

	request := &types.AggchainProofRequest{
		LastProvenBlock:    100,
		RequestedEndBlock:  200,
		L1InfoTreeRootHash: common.Hash{},
		L1InfoTreeLeaf:     l1infotreesync.L1InfoTreeLeaf{},
		L1InfoTreeMerkleProof: agglayer.MerkleProof{
			Root:  common.Hash{},
			Proof: [32]common.Hash{},
		},
		GERLeavesWithBlockNumber:           nil,
		ImportedBridgeExitsWithBlockNumber: nil,
	}

	result, err := client.GenerateAggchainProof(request)

	assert.NoError(t, err)
	assert.Equal(t, []byte("dummy-proof"), result.SP1StarkProof.Proof)
	assert.Equal(t, uint64(100), result.LastProvenBlock)
	assert.Equal(t, uint64(200), result.EndBlock)
	assert.Equal(t, common.Hash{}, result.LocalExitRoot)
	assert.Equal(t, []byte{}, result.CustomChainData)
	assert.Equal(t, map[string][]byte{"key1": []byte("value1")}, result.Context)
	assert.Equal(t, common.HexToHash("0x1").Bytes(), result.AggchainParams.Bytes())
	mockClient.AssertExpectations(t)
}

func TestGenerateAggchainProof_Error(t *testing.T) {
	mockClient := aggkitProverMocks.NewAggchainProofServiceClient(t)
	client := &AggchainProofClient{client: mockClient}

	expectedError := errors.New("Generate error")

	mockClient.On("GenerateAggchainProof", mock.Anything, mock.Anything).Return((*aggkitProverV1Proto.GenerateAggchainProofResponse)(nil), expectedError)

	request := &types.AggchainProofRequest{
		LastProvenBlock:    300,
		RequestedEndBlock:  400,
		L1InfoTreeRootHash: common.BytesToHash([]byte("0x")),
		L1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
			BlockNumber: 1,
			Hash:        common.HexToHash("0x2"),
		},
		L1InfoTreeMerkleProof: agglayer.MerkleProof{
			Root:  common.HexToHash("0x3"),
			Proof: [32]common.Hash{common.HexToHash("0x4")},
		},
		GERLeavesWithBlockNumber: map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber{
			common.HexToHash("0x5"): {
				BlockNumber: 1,
				ProvenInsertedGERLeaf: agglayer.ProvenInsertedGER{
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
		},
		ImportedBridgeExitsWithBlockNumber: []*agglayer.ImportedBridgeExitWithBlockNumber{
			{
				BlockNumber: 1,
				ImportedBridgeExit: &agglayer.ImportedBridgeExit{
					BridgeExit: &agglayer.BridgeExit{
						LeafType:           1,
						DestinationNetwork: 1,
						DestinationAddress: common.HexToAddress("0x1"),
						Amount:             common.Big1,
						Metadata:           []byte("metadata"),
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
					ClaimData: &agglayer.ClaimFromMainnnet{
						ProofLeafMER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x3"),
							Proof: tree.EmptyProof,
						},
						ProofGERToL1Root: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x4"),
							Proof: tree.EmptyProof,
						},
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0x5"),
							MainnetExitRoot: common.HexToHash("0x6"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7"),
								BlockHash:      common.HexToHash("0x8"),
								Timestamp:      1,
							},
						},
					},
				},
			},
			{
				BlockNumber: 2,
				ImportedBridgeExit: &agglayer.ImportedBridgeExit{
					BridgeExit: &agglayer.BridgeExit{
						LeafType:           1,
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x11"),
						Amount:             common.Big2,
						Metadata:           []byte("metadata2"),
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x22"),
						},
					},
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 11,
						LeafIndex:   11,
					},
					ClaimData: &agglayer.ClaimFromRollup{
						ProofLeafLER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x33"),
							Proof: tree.EmptyProof,
						},
						ProofGERToL1Root: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x44"),
							Proof: tree.EmptyProof,
						},
						ProofLERToRER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x555"),
							Proof: tree.EmptyProof,
						},
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 11,
							RollupExitRoot:  common.HexToHash("0x55"),
							MainnetExitRoot: common.HexToHash("0x66"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x77"),
								BlockHash:      common.HexToHash("0x88"),
								Timestamp:      11,
							},
						},
					},
				},
			},
		},
	}

	result, err := client.GenerateAggchainProof(request)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "Generate error", err.Error())
	mockClient.AssertExpectations(t)
}
