package types

import (
	"testing"

	agglayer "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// createTestImportedBridgeExitWithBlockNumber creates a test ImportedBridgeExitWithBlockNumber for testing
func createTestImportedBridgeExitWithBlockNumber(t *testing.T) []*agglayer.ImportedBridgeExitWithBlockNumber {
	t.Helper()

	return []*agglayer.ImportedBridgeExitWithBlockNumber{
		{
			BlockNumber: 170,
			ImportedBridgeExit: &agglayer.ImportedBridgeExit{
				BridgeExit: &agglayer.BridgeExit{
					LeafType: agglayer.LeafTypeAsset,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0xffffffffffffffffffffffffffffffffffffffff"),
					Amount:             nil,
					Metadata:           []byte{0x01, 0x02, 0x03},
				},
				ClaimData: &agglayer.ClaimFromMainnet{
					ProofLeafMER: &agglayer.MerkleProof{
						Root:  common.HexToHash("0x1010101010101010"),
						Proof: [32]common.Hash{common.HexToHash("0x2020202020202020")},
					},
					ProofGERToL1Root: &agglayer.MerkleProof{
						Root:  common.HexToHash("0x3030303030303030"),
						Proof: [32]common.Hash{common.HexToHash("0x4040404040404040")},
					},
					L1Leaf: &agglayer.L1InfoTreeLeaf{
						L1InfoTreeIndex: 7,
						RollupExitRoot:  common.HexToHash("0x5050505050505050"),
						MainnetExitRoot: common.HexToHash("0x6060606060606060"),
						Inner: &agglayer.L1InfoTreeLeafInner{
							GlobalExitRoot: common.HexToHash("0x7070707070707070"),
							BlockHash:      common.HexToHash("0x8080808080808080"),
							Timestamp:      1234567892,
						},
					},
				},
				GlobalIndex: &agglayer.GlobalIndex{
					MainnetFlag: true,
					RollupIndex: 1,
					LeafIndex:   10,
				},
			},
		},
	}
}

func TestNewAggchainProofRequest(t *testing.T) {
	tests := []struct {
		name                                       string
		lastProvenBlock                            uint64
		requestedEndBlock                          uint64
		l1InfoTreeRootHash                         common.Hash
		l1InfoTreeLeaf                             l1infotreesync.L1InfoTreeLeaf
		l1InfoTreeMerkleProof                      agglayer.MerkleProof
		gerLeavesWithBlockNumber                   map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber
		importedBridgeExitsWithBlockNumber         []*agglayer.ImportedBridgeExitWithBlockNumber
		removedGers                                []*agglayer.RemovedGER
		unclaims                                   []*agglayer.Unclaim
		expectedLastProvenBlock                    uint64
		expectedRequestedEndBlock                  uint64
		expectedL1InfoTreeRootHash                 common.Hash
		expectedL1InfoTreeLeaf                     l1infotreesync.L1InfoTreeLeaf
		expectedL1InfoTreeMerkleProof              agglayer.MerkleProof
		expectedGERLeavesWithBlockNumber           map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber
		expectedImportedBridgeExitsWithBlockNumber []*agglayer.ImportedBridgeExitWithBlockNumber
		expectedRemovedGers                        []*agglayer.RemovedGER
		expectedUnclaims                           []*agglayer.Unclaim
	}{
		{
			name:                                       "EmptyRequest",
			lastProvenBlock:                            0,
			requestedEndBlock:                          0,
			l1InfoTreeRootHash:                         common.Hash{},
			l1InfoTreeLeaf:                             l1infotreesync.L1InfoTreeLeaf{},
			l1InfoTreeMerkleProof:                      agglayer.MerkleProof{},
			gerLeavesWithBlockNumber:                   nil,
			importedBridgeExitsWithBlockNumber:         nil,
			removedGers:                                nil,
			unclaims:                                   nil,
			expectedLastProvenBlock:                    0,
			expectedRequestedEndBlock:                  0,
			expectedL1InfoTreeRootHash:                 common.Hash{},
			expectedL1InfoTreeLeaf:                     l1infotreesync.L1InfoTreeLeaf{},
			expectedL1InfoTreeMerkleProof:              agglayer.MerkleProof{},
			expectedGERLeavesWithBlockNumber:           nil,
			expectedImportedBridgeExitsWithBlockNumber: nil,
			expectedRemovedGers:                        nil,
			expectedUnclaims:                           nil,
		},
		{
			name:               "BasicRequest",
			lastProvenBlock:    100,
			requestedEndBlock:  200,
			l1InfoTreeRootHash: common.HexToHash("0x1234567890abcdef"),
			l1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
				BlockNumber:       150,
				BlockPosition:     1,
				L1InfoTreeIndex:   5,
				PreviousBlockHash: common.HexToHash("0xabcdef1234567890"),
				Timestamp:         1234567890,
				MainnetExitRoot:   common.HexToHash("0x1111111111111111"),
				RollupExitRoot:    common.HexToHash("0x2222222222222222"),
				GlobalExitRoot:    common.HexToHash("0x3333333333333333"),
				Hash:              common.HexToHash("0x4444444444444444"),
			},
			l1InfoTreeMerkleProof: agglayer.MerkleProof{
				Root:  common.HexToHash("0x5555555555555555"),
				Proof: [32]common.Hash{common.HexToHash("0x6666666666666666")},
			},
			gerLeavesWithBlockNumber: map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber{
				common.HexToHash("0x7777777777777777"): {
					BlockNumber: 160,
					ProvenInsertedGERLeaf: agglayer.ProvenInsertedGER{
						ProofGERToL1Root: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x8888888888888888"),
							Proof: [32]common.Hash{common.HexToHash("0x9999999999999999")},
						},
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 6,
							RollupExitRoot:  common.HexToHash("0xaaaaaaaaaaaaaaaa"),
							MainnetExitRoot: common.HexToHash("0xbbbbbbbbbbbbbbbb"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0xcccccccccccccccc"),
								BlockHash:      common.HexToHash("0xdddddddddddddddd"),
								Timestamp:      1234567891,
							},
						},
					},
					BlockIndex: 2,
				},
			},
			importedBridgeExitsWithBlockNumber: createTestImportedBridgeExitWithBlockNumber(t),
			removedGers: []*agglayer.RemovedGER{
				{
					GlobalExitRoot: common.HexToHash("0x9090909090909090"),
					BlockNumber:    180,
					BlockIndex:     3,
				},
			},
			unclaims: []*agglayer.Unclaim{
				{
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 2,
						LeafIndex:   20,
					},
					BlockNumber: 190,
					BlockIndex:  4,
				},
			},
			expectedLastProvenBlock:    100,
			expectedRequestedEndBlock:  200,
			expectedL1InfoTreeRootHash: common.HexToHash("0x1234567890abcdef"),
			expectedL1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
				BlockNumber:       150,
				BlockPosition:     1,
				L1InfoTreeIndex:   5,
				PreviousBlockHash: common.HexToHash("0xabcdef1234567890"),
				Timestamp:         1234567890,
				MainnetExitRoot:   common.HexToHash("0x1111111111111111"),
				RollupExitRoot:    common.HexToHash("0x2222222222222222"),
				GlobalExitRoot:    common.HexToHash("0x3333333333333333"),
				Hash:              common.HexToHash("0x4444444444444444"),
			},
			expectedL1InfoTreeMerkleProof: agglayer.MerkleProof{
				Root:  common.HexToHash("0x5555555555555555"),
				Proof: [32]common.Hash{common.HexToHash("0x6666666666666666")},
			},
			expectedGERLeavesWithBlockNumber: map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber{
				common.HexToHash("0x7777777777777777"): {
					BlockNumber: 160,
					ProvenInsertedGERLeaf: agglayer.ProvenInsertedGER{
						ProofGERToL1Root: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x8888888888888888"),
							Proof: [32]common.Hash{common.HexToHash("0x9999999999999999")},
						},
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 6,
							RollupExitRoot:  common.HexToHash("0xaaaaaaaaaaaaaaaa"),
							MainnetExitRoot: common.HexToHash("0xbbbbbbbbbbbbbbbb"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0xcccccccccccccccc"),
								BlockHash:      common.HexToHash("0xdddddddddddddddd"),
								Timestamp:      1234567891,
							},
						},
					},
					BlockIndex: 2,
				},
			},
			expectedImportedBridgeExitsWithBlockNumber: createTestImportedBridgeExitWithBlockNumber(t),
			expectedRemovedGers: []*agglayer.RemovedGER{
				{
					GlobalExitRoot: common.HexToHash("0x9090909090909090"),
					BlockNumber:    180,
					BlockIndex:     3,
				},
			},
			expectedUnclaims: []*agglayer.Unclaim{
				{
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 2,
						LeafIndex:   20,
					},
					BlockNumber: 190,
					BlockIndex:  4,
				},
			},
		},
		{
			name:               "EmptySlicesAndMaps",
			lastProvenBlock:    50,
			requestedEndBlock:  100,
			l1InfoTreeRootHash: common.HexToHash("0x0000000000000001"),
			l1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
				BlockNumber: 75,
			},
			l1InfoTreeMerkleProof: agglayer.MerkleProof{
				Root: common.HexToHash("0x0000000000000002"),
			},
			gerLeavesWithBlockNumber:           make(map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber),
			importedBridgeExitsWithBlockNumber: make([]*agglayer.ImportedBridgeExitWithBlockNumber, 0),
			removedGers:                        make([]*agglayer.RemovedGER, 0),
			unclaims:                           make([]*agglayer.Unclaim, 0),
			expectedLastProvenBlock:            50,
			expectedRequestedEndBlock:          100,
			expectedL1InfoTreeRootHash:         common.HexToHash("0x0000000000000001"),
			expectedL1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
				BlockNumber: 75,
			},
			expectedL1InfoTreeMerkleProof: agglayer.MerkleProof{
				Root: common.HexToHash("0x0000000000000002"),
			},
			expectedGERLeavesWithBlockNumber:           make(map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber),
			expectedImportedBridgeExitsWithBlockNumber: make([]*agglayer.ImportedBridgeExitWithBlockNumber, 0),
			expectedRemovedGers:                        make([]*agglayer.RemovedGER, 0),
			expectedUnclaims:                           make([]*agglayer.Unclaim, 0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := NewAggchainProofRequest(
				tt.lastProvenBlock,
				tt.requestedEndBlock,
				tt.l1InfoTreeRootHash,
				tt.l1InfoTreeLeaf,
				tt.l1InfoTreeMerkleProof,
				tt.gerLeavesWithBlockNumber,
				tt.importedBridgeExitsWithBlockNumber,
				tt.removedGers,
				tt.unclaims,
			)

			require.NotNil(t, req)
			require.Equal(t, tt.expectedLastProvenBlock, req.LastProvenBlock)
			require.Equal(t, tt.expectedRequestedEndBlock, req.RequestedEndBlock)
			require.Equal(t, tt.expectedL1InfoTreeRootHash, req.L1InfoTreeRootHash)
			require.Equal(t, tt.expectedL1InfoTreeLeaf, req.L1InfoTreeLeaf)
			require.Equal(t, tt.expectedL1InfoTreeMerkleProof, req.L1InfoTreeMerkleProof)
			require.Equal(t, tt.expectedGERLeavesWithBlockNumber, req.GERLeavesWithBlockNumber)
			require.Equal(t, tt.expectedImportedBridgeExitsWithBlockNumber, req.ImportedBridgeExitsWithBlockNumber)
			require.Equal(t, tt.expectedRemovedGers, req.RemovedGers)
			require.Equal(t, tt.expectedUnclaims, req.Unclaims)
		})
	}
}

func TestAggchainProofRequest_String(t *testing.T) {
	tests := []struct {
		name     string
		request  *AggchainProofRequest
		expected string
	}{
		{
			name:     "NilRequest",
			request:  nil,
			expected: "",
		},
		{
			name: "RequestWithData",
			request: &AggchainProofRequest{
				LastProvenBlock:    100,
				RequestedEndBlock:  200,
				L1InfoTreeRootHash: common.HexToHash("0x1234567890abcdef"),
				L1InfoTreeLeaf: l1infotreesync.L1InfoTreeLeaf{
					BlockNumber:       150,
					BlockPosition:     1,
					L1InfoTreeIndex:   5,
					PreviousBlockHash: common.HexToHash("0xabcdef1234567890"),
					Timestamp:         1234567890,
					MainnetExitRoot:   common.HexToHash("0x1111111111111111"),
					RollupExitRoot:    common.HexToHash("0x2222222222222222"),
					GlobalExitRoot:    common.HexToHash("0x3333333333333333"),
					Hash:              common.HexToHash("0x4444444444444444"),
				},
				L1InfoTreeMerkleProof: agglayer.MerkleProof{
					Root:  common.HexToHash("0x5555555555555555"),
					Proof: [32]common.Hash{common.HexToHash("0x6666666666666666")},
				},
				GERLeavesWithBlockNumber: map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber{
					common.HexToHash("0x7777777777777777"): {
						BlockNumber: 160,
						ProvenInsertedGERLeaf: agglayer.ProvenInsertedGER{
							ProofGERToL1Root: &agglayer.MerkleProof{
								Root:  common.HexToHash("0x8888888888888888"),
								Proof: [32]common.Hash{common.HexToHash("0x9999999999999999")},
							},
							L1Leaf: &agglayer.L1InfoTreeLeaf{
								L1InfoTreeIndex: 6,
								RollupExitRoot:  common.HexToHash("0xaaaaaaaaaaaaaaaa"),
								MainnetExitRoot: common.HexToHash("0xbbbbbbbbbbbbbbbb"),
								Inner: &agglayer.L1InfoTreeLeafInner{
									GlobalExitRoot: common.HexToHash("0xcccccccccccccccc"),
									BlockHash:      common.HexToHash("0xdddddddddddddddd"),
									Timestamp:      1234567891,
								},
							},
						},
						BlockIndex: 2,
					},
				},
				ImportedBridgeExitsWithBlockNumber: createTestImportedBridgeExitWithBlockNumber(t),
				RemovedGers: []*agglayer.RemovedGER{
					{
						GlobalExitRoot: common.HexToHash("0x9090909090909090"),
						BlockNumber:    180,
						BlockIndex:     3,
					},
				},
				Unclaims: []*agglayer.Unclaim{
					{
						GlobalIndex: &agglayer.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 2,
							LeafIndex:   20,
						},
						BlockNumber: 190,
						BlockIndex:  4,
					},
				},
			},
			expected: `AggchainProofRequest{
	lastProvenBlock: 100,
	toBlock: 200,
	root.Hash: 0x0000000000000000000000000000000000000000000000001234567890abcdef,
	*leaf: {BlockNumber:150 BlockPosition:1 L1InfoTreeIndex:5 PreviousBlockHash:0x000000000000000000000000000000000000000000000000abcdef1234567890 Timestamp:1234567890 MainnetExitRoot:0x0000000000000000000000000000000000000000000000001111111111111111 RollupExitRoot:0x0000000000000000000000000000000000000000000000002222222222222222 GlobalExitRoot:0x0000000000000000000000000000000000000000000000003333333333333333 Hash:0x0000000000000000000000000000000000000000000000004444444444444444},
	agglayertypes.MerkleProof{
		Root:  0x0000000000000000000000000000000000000000000000005555555555555555,
		Proof: [0x0000000000000000000000000000000000000000000000006666666666666666 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000],
	},
	injectedGERsProofs: map[0x0000000000000000000000000000000000000000000000007777777777777777:0x140001b3380],
	importedBridgeExits: [BlockNumber: 170, ImportedBridgeExit: BridgeExit: LeafType: Transfer, DestinationNetwork: 2, DestinationAddress: 0xFFfFfFffFFfffFFfFFfFFFFFffFFFffffFfFFFfF, Amount: <nil>, Metadata: 010203, TokenInfo: OriginNetwork: 1, OriginTokenAddress: 0xEeeeeEeeeEeEeeEeEeEeeEEEeeeeEeeeeeeeEEeE, GlobalIndex: MainnetFlag: true, RollupIndex: 1, LeafIndex: 10ClaimData: ProofLeafMER: Root: 0x0000000000000000000000000000000000000000000000001010101010101010, Proof: [0x0000000000000000000000000000000000000000000000002020202020202020 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000], ProofGERToL1Root: Root: 0x0000000000000000000000000000000000000000000000003030303030303030, Proof: [0x0000000000000000000000000000000000000000000000004040404040404040 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000 0x0000000000000000000000000000000000000000000000000000000000000000], L1Leaf: L1InfoTreeIndex: 7, RollupExitRoot: 0x0000000000000000000000000000000000000000000000005050505050505050, MainnetExitRoot: 0x0000000000000000000000000000000000000000000000006060606060606060, Inner: GlobalExitRoot: 0x0000000000000000000000000000000000000000000000007070707070707070, BlockHash: 0x0000000000000000000000000000000000000000000000008080808080808080, Timestamp: 1234567892],
	removedGers: [0x140001ef530],
	unclaims: [0x1400000f308],
	}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result string
			if tt.request != nil {
				result = tt.request.String()
			}

			// For the complex test case, we need to check that the string contains the expected parts
			// rather than exact match due to pointer addresses in the output
			if tt.name == "RequestWithData" {
				require.Contains(t, result, "lastProvenBlock: 100")
				require.Contains(t, result, "toBlock: 200")
				require.Contains(t, result, "root.Hash: 0x0000000000000000000000000000000000000000000000001234567890abcdef")
				require.Contains(t, result, "BlockNumber:150")
				require.Contains(t, result, "L1InfoTreeIndex:5")
				require.Contains(t, result, "Root:  0x0000000000000000000000000000000000000000000000005555555555555555,")
				require.Contains(t, result, "injectedGERsProofs: map[")
				require.Contains(t, result, "importedBridgeExits: [")
				require.Contains(t, result, "removedGers: [")
				require.Contains(t, result, "unclaims: [")
			} else {
				require.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestAggchainProofRequest_String_EdgeCases(t *testing.T) {
	t.Run("EmptyImportedBridgeExits", func(t *testing.T) {
		req := &AggchainProofRequest{
			LastProvenBlock:                    100,
			RequestedEndBlock:                  200,
			L1InfoTreeRootHash:                 common.HexToHash("0x1234567890abcdef"),
			L1InfoTreeLeaf:                     l1infotreesync.L1InfoTreeLeaf{},
			L1InfoTreeMerkleProof:              agglayer.MerkleProof{},
			GERLeavesWithBlockNumber:           make(map[common.Hash]*agglayer.ProvenInsertedGERWithBlockNumber),
			ImportedBridgeExitsWithBlockNumber: make([]*agglayer.ImportedBridgeExitWithBlockNumber, 0),
			RemovedGers:                        make([]*agglayer.RemovedGER, 0),
			Unclaims:                           make([]*agglayer.Unclaim, 0),
		}

		result := req.String()
		require.Contains(t, result, "lastProvenBlock: 100")
		require.Contains(t, result, "toBlock: 200")
		require.Contains(t, result, "importedBridgeExits: []")
		require.Contains(t, result, "removedGers: []")
		require.Contains(t, result, "unclaims: []")
	})

	t.Run("NilImportedBridgeExits", func(t *testing.T) {
		req := &AggchainProofRequest{
			LastProvenBlock:                    100,
			RequestedEndBlock:                  200,
			L1InfoTreeRootHash:                 common.HexToHash("0x1234567890abcdef"),
			L1InfoTreeLeaf:                     l1infotreesync.L1InfoTreeLeaf{},
			L1InfoTreeMerkleProof:              agglayer.MerkleProof{},
			GERLeavesWithBlockNumber:           nil,
			ImportedBridgeExitsWithBlockNumber: nil,
			RemovedGers:                        nil,
			Unclaims:                           nil,
		}

		result := req.String()
		require.Contains(t, result, "lastProvenBlock: 100")
		require.Contains(t, result, "toBlock: 200")
		require.Contains(t, result, "importedBridgeExits: []")
		require.Contains(t, result, "removedGers: []")
		require.Contains(t, result, "unclaims: []")
	})
}
