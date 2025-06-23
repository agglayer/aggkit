package converters

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConvertClaimToImportedBridgeExit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		claim         bridgesync.Claim
		expectedError bool
		expectedExit  *agglayertypes.ImportedBridgeExit
	}{
		{
			name: "Asset claim",
			claim: bridgesync.Claim{
				IsMessage:          false,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x123"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x456"),
				Amount:             big.NewInt(100),
				Metadata:           []byte("metadata"),
				GlobalIndex:        big.NewInt(1),
			},
			expectedError: false,
			expectedExit: &agglayertypes.ImportedBridgeExit{
				BridgeExit: &agglayertypes.BridgeExit{
					LeafType: agglayertypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           crypto.Keccak256([]byte("metadata")),
				},
				GlobalIndex: &agglayertypes.GlobalIndex{
					MainnetFlag: false,
					RollupIndex: 0,
					LeafIndex:   1,
				},
			},
		},
		{
			name: "Message claim",
			claim: bridgesync.Claim{
				IsMessage:          true,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x123"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x456"),
				Amount:             big.NewInt(100),
				Metadata:           []byte("metadata"),
				GlobalIndex:        big.NewInt(2),
			},
			expectedError: false,
			expectedExit: &agglayertypes.ImportedBridgeExit{
				BridgeExit: &agglayertypes.BridgeExit{
					LeafType: agglayertypes.LeafTypeMessage,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           crypto.Keccak256([]byte("metadata")),
				},
				GlobalIndex: &agglayertypes.GlobalIndex{
					MainnetFlag: false,
					RollupIndex: 0,
					LeafIndex:   2,
				},
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			converter := &ImportedBridgeExitConverter{}
			exit, err := converter.ConvertToImportedBridgeExitWithoutClaimData(tt.claim)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedExit, exit)
			}
		})
	}
}

//nolint:dupl
func TestGetImportedBridgeExits(t *testing.T) {
	t.Parallel()

	mockProof := generateTestProof(t)

	tests := []struct {
		name          string
		claims        []bridgesync.Claim
		mockFn        func(*mocks.L1InfoTreeDataQuerier)
		expectedError bool
		expectedExits []*agglayertypes.ImportedBridgeExit
	}{
		{
			name: "Single claim",
			claims: []bridgesync.Claim{
				{
					IsMessage:           false,
					OriginNetwork:       1,
					OriginAddress:       common.HexToAddress("0x1234"),
					DestinationNetwork:  2,
					DestinationAddress:  common.HexToAddress("0x4567"),
					Amount:              big.NewInt(111),
					Metadata:            []byte("metadata1"),
					GlobalIndex:         bridgesync.GenerateGlobalIndex(false, 1, 1),
					GlobalExitRoot:      common.HexToHash("0x7891"),
					RollupExitRoot:      common.HexToHash("0xaaab"),
					MainnetExitRoot:     common.HexToHash("0xbbba"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
			},
			mockFn: func(mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex:   1,
						Timestamp:         123456789,
						PreviousBlockHash: common.HexToHash("0xabc"),
						GlobalExitRoot:    common.HexToHash("0x7891"),
					}, mockProof, nil)
			},
			expectedError: false,
			expectedExits: []*agglayertypes.ImportedBridgeExit{
				{
					BridgeExit: &agglayertypes.BridgeExit{
						LeafType: agglayertypes.LeafTypeAsset,
						TokenInfo: &agglayertypes.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x1234"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x4567"),
						Amount:             big.NewInt(111),
						Metadata:           crypto.Keccak256([]byte("metadata1")),
					},
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 1,
						LeafIndex:   1,
					},
					ClaimData: &agglayertypes.ClaimFromRollup{
						L1Leaf: &agglayertypes.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0xaaab"),
							MainnetExitRoot: common.HexToHash("0xbbba"),
							Inner: &agglayertypes.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7891"),
								Timestamp:      123456789,
								BlockHash:      common.HexToHash("0xabc"),
							},
						},
						ProofLeafLER: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0xc52019815b51acf67a715cae6794a20083d63fd9af45783b7adf69123dae92c8"),
							Proof: mockProof,
						},
						ProofLERToRER: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0xaaab"),
							Proof: mockProof,
						},
						ProofGERToL1Root: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0x7891"),
							Proof: mockProof,
						},
					},
				},
			},
		},
		{
			name: "Multiple claims",
			claims: []bridgesync.Claim{
				{
					IsMessage:           false,
					OriginNetwork:       1,
					OriginAddress:       common.HexToAddress("0x123"),
					DestinationNetwork:  2,
					DestinationAddress:  common.HexToAddress("0x456"),
					Amount:              big.NewInt(100),
					Metadata:            []byte("metadata"),
					GlobalIndex:         big.NewInt(1),
					GlobalExitRoot:      common.HexToHash("0x7891"),
					RollupExitRoot:      common.HexToHash("0xaaa"),
					MainnetExitRoot:     common.HexToHash("0xbbb"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
				{
					IsMessage:           true,
					OriginNetwork:       3,
					OriginAddress:       common.HexToAddress("0x789"),
					DestinationNetwork:  4,
					DestinationAddress:  common.HexToAddress("0xabc"),
					Amount:              big.NewInt(200),
					Metadata:            []byte("data"),
					GlobalIndex:         bridgesync.GenerateGlobalIndex(true, 0, 2),
					GlobalExitRoot:      common.HexToHash("0x7891"),
					RollupExitRoot:      common.HexToHash("0xbbb"),
					MainnetExitRoot:     common.HexToHash("0xccc"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
			},
			mockFn: func(mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(
					&l1infotreesync.L1InfoTreeLeaf{
						L1InfoTreeIndex:   1,
						Timestamp:         123456789,
						PreviousBlockHash: common.HexToHash("0xabc"),
						GlobalExitRoot:    common.HexToHash("0x7891"),
					}, mockProof, nil)
			},
			expectedError: false,
			expectedExits: []*agglayertypes.ImportedBridgeExit{
				{
					BridgeExit: &agglayertypes.BridgeExit{
						LeafType: agglayertypes.LeafTypeAsset,
						TokenInfo: &agglayertypes.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x123"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x456"),
						Amount:             big.NewInt(100),
						Metadata:           crypto.Keccak256([]byte("metadata")),
					},
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 0,
						LeafIndex:   1,
					},
					ClaimData: &agglayertypes.ClaimFromRollup{
						L1Leaf: &agglayertypes.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0xaaa"),
							MainnetExitRoot: common.HexToHash("0xbbb"),
							Inner: &agglayertypes.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7891"),
								Timestamp:      123456789,
								BlockHash:      common.HexToHash("0xabc"),
							},
						},
						ProofLeafLER: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0x105e0f1144e57f6fb63f1dfc5083b1f59be3512be7cf5e63523779ad14a4d987"),
							Proof: mockProof,
						},
						ProofLERToRER: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0xaaa"),
							Proof: mockProof,
						},
						ProofGERToL1Root: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0x7891"),
							Proof: mockProof,
						},
					},
				},
				{
					BridgeExit: &agglayertypes.BridgeExit{
						LeafType: agglayertypes.LeafTypeMessage,
						TokenInfo: &agglayertypes.TokenInfo{
							OriginNetwork:      3,
							OriginTokenAddress: common.HexToAddress("0x789"),
						},
						DestinationNetwork: 4,
						DestinationAddress: common.HexToAddress("0xabc"),
						Amount:             big.NewInt(200),
						Metadata:           crypto.Keccak256([]byte("data")),
					},
					GlobalIndex: &agglayertypes.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 0,
						LeafIndex:   2,
					},
					ClaimData: &agglayertypes.ClaimFromMainnnet{
						L1Leaf: &agglayertypes.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0xbbb"),
							MainnetExitRoot: common.HexToHash("0xccc"),
							Inner: &agglayertypes.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7891"),
								Timestamp:      123456789,
								BlockHash:      common.HexToHash("0xabc"),
							},
						},
						ProofLeafMER: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0xccc"),
							Proof: mockProof,
						},
						ProofGERToL1Root: &agglayertypes.MerkleProof{
							Root:  common.HexToHash("0x7891"),
							Proof: mockProof,
						},
					},
				},
			},
		},
		{
			name:          "No claims",
			claims:        []bridgesync.Claim{},
			expectedError: false,
			expectedExits: []*agglayertypes.ImportedBridgeExit{},
		},
		{
			name: "error getting proof for GER",
			claims: []bridgesync.Claim{
				{
					IsMessage:           false,
					OriginNetwork:       11,
					OriginAddress:       common.HexToAddress("0x1234"),
					DestinationNetwork:  22,
					DestinationAddress:  common.HexToAddress("0x45678"),
					Amount:              big.NewInt(1010),
					Metadata:            []byte("metadata"),
					GlobalIndex:         big.NewInt(11),
					GlobalExitRoot:      common.HexToHash("0x78912"),
					RollupExitRoot:      common.HexToHash("0xaaaa"),
					MainnetExitRoot:     common.HexToHash("0xbbbb"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
			},
			mockFn: func(mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(
					nil, treetypes.Proof{}, errors.New("error getting proof for GER"),
				)
			},
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeQuery := mocks.NewL1InfoTreeDataQuerier(t)
			if tt.mockFn != nil {
				tt.mockFn(mockL1InfoTreeQuery)
			}

			converter := NewImportedBridgeExitConverter(log.WithFields("test", tt.name), mockL1InfoTreeQuery)
			exits, err := converter.ConvertToImportedBridgeExits(context.Background(), tt.claims, common.HexToHash("0x7891"))

			if tt.expectedError {
				require.Error(t, err)
				require.Nil(t, exits)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedExits, exits)
			}
		})
	}
}

func generateTestProof(t *testing.T) treetypes.Proof {
	t.Helper()

	proof := treetypes.Proof{}

	for i := 0; i < int(treetypes.DefaultHeight) && i < 10; i++ {
		proof[i] = common.HexToHash(fmt.Sprintf("0x%d", i))
	}

	return proof
}
