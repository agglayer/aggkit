package flows

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
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
				MainnetExitRoot:    common.Hash{},
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
				MainnetExitRoot:    common.Hash{},
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

			flow := &baseFlow{}
			exit, err := flow.ConvertClaimToImportedBridgeExit(tt.claim)

			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedExit, exit)
			}
		})
	}
}

func TestGetBridgeExits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		bridges       []bridgesync.Bridge
		expectedExits []*agglayertypes.BridgeExit
	}{
		{
			name: "Single bridge",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
			},
			expectedExits: []*agglayertypes.BridgeExit{
				{
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
			},
		},
		{
			name: "Multiple bridges",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				{
					LeafType:           agglayertypes.LeafTypeMessage.Uint8(),
					OriginNetwork:      3,
					OriginAddress:      common.HexToAddress("0x789"),
					DestinationNetwork: 4,
					DestinationAddress: common.HexToAddress("0xabc"),
					Amount:             big.NewInt(200),
					Metadata:           []byte("data"),
				},
			},
			expectedExits: []*agglayertypes.BridgeExit{
				{
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
				{
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
			},
		},
		{
			name:          "No bridges",
			bridges:       []bridgesync.Bridge{},
			expectedExits: []*agglayertypes.BridgeExit{},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flow := &baseFlow{}
			exits := flow.getBridgeExits(tt.bridges)

			require.Equal(t, tt.expectedExits, exits)
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

			flow := &baseFlow{
				l1InfoTreeDataQuerier: mockL1InfoTreeQuery,
				log:                   log.WithFields("test", "unittest"),
			}
			exits, err := flow.getImportedBridgeExits(context.Background(), tt.claims, common.HexToHash("0x7891"))

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

func TestBuildCertificate(t *testing.T) {
	mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
	mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
	mockProof := generateTestProof(t)

	tests := []struct {
		name                string
		bridges             []bridgesync.Bridge
		claims              []bridgesync.Claim
		lastSentCertificate types.CertificateHeader
		fromBlock           uint64
		toBlock             uint64
		mockFn              func()
		expectedCert        *agglayertypes.Certificate
		expectedError       bool
	}{
		{
			name: "Valid certificate with bridges and claims",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					DepositCount:       1,
				},
			},
			claims: []bridgesync.Claim{
				{
					IsMessage:           false,
					OriginNetwork:       1,
					OriginAddress:       common.HexToAddress("0x1234"),
					DestinationNetwork:  2,
					DestinationAddress:  common.HexToAddress("0x4567"),
					Amount:              big.NewInt(111),
					Metadata:            []byte("metadata1"),
					GlobalIndex:         big.NewInt(1),
					GlobalExitRoot:      common.HexToHash("0x7891"),
					RollupExitRoot:      common.HexToHash("0xaaab"),
					MainnetExitRoot:     common.HexToHash("0xbbba"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
			},
			lastSentCertificate: types.CertificateHeader{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
				Status:           agglayertypes.Settled,
			},
			fromBlock: 0,
			toBlock:   10,
			expectedCert: &agglayertypes.Certificate{
				NetworkID:         1,
				PrevLocalExitRoot: common.HexToHash("0x123"),
				NewLocalExitRoot:  common.HexToHash("0x789"),
				Metadata:          types.NewCertificateMetadata(0, 10, 0, types.CertificateTypePP.ToInt()).ToHash(),
				BridgeExits: []*agglayertypes.BridgeExit{
					{
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
				},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{
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
							RollupIndex: 0,
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
				Height: 2,
			},
			mockFn: func() {
				mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1))
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).Return(common.HexToHash("0x789"), nil)
				mockL1InfoTreeQuerier.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex:   1,
					Timestamp:         123456789,
					PreviousBlockHash: common.HexToHash("0xabc"),
					GlobalExitRoot:    common.HexToHash("0x7891"),
				}, mockProof, nil)
			},
			expectedError: false,
		},
		{
			name:    "No bridges or claims",
			bridges: []bridgesync.Bridge{},
			claims:  []bridgesync.Claim{},
			lastSentCertificate: types.CertificateHeader{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
			},
			expectedCert:  nil,
			expectedError: true,
		},
		{
			name: "Error getting imported bridge exits",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					DepositCount:       1,
				},
			},
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x1234"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x4567"),
					Amount:             big.NewInt(111),
					Metadata:           []byte("metadata1"),
					GlobalIndex:        new(big.Int).SetBytes([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}),
					GlobalExitRoot:     common.HexToHash("0x7891"),
					RollupExitRoot:     common.HexToHash("0xaaab"),
					MainnetExitRoot:    common.HexToHash("0xbbba"),
					ProofLocalExitRoot: mockProof,
				},
			},
			lastSentCertificate: types.CertificateHeader{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
			},
			mockFn: func() {
				mockL1InfoTreeQuerier.EXPECT().GetProofForGER(mock.Anything, mock.Anything, mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex:   1,
					Timestamp:         123456789,
					PreviousBlockHash: common.HexToHash("0xabc"),
					GlobalExitRoot:    common.HexToHash("0x7891"),
				}, mockProof, nil)
			},
			expectedCert:  nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			mockL1InfoTreeQuerier.ExpectedCalls = nil
			mockL2BridgeQuerier.ExpectedCalls = nil

			if tt.mockFn != nil {
				tt.mockFn()
			}

			flow := &baseFlow{
				l2BridgeQuerier:       mockL2BridgeQuerier,
				l1InfoTreeDataQuerier: mockL1InfoTreeQuerier,
				log:                   log.WithFields("test", "unittest"),
			}

			certParam := &types.CertificateBuildParams{
				ToBlock:                        tt.toBlock,
				Bridges:                        tt.bridges,
				Claims:                         tt.claims,
				CertificateType:                types.CertificateTypePP,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x7891"),
			}
			cert, err := flow.BuildCertificate(context.Background(), certParam, &tt.lastSentCertificate, false)

			if tt.expectedError {
				require.Error(t, err)
				require.Nil(t, cert)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, cert)
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

func Test_PPFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name               string
		mockFn             func(*mocks.AggSenderStorage, *mocks.BridgeQuerier, *mocks.L1InfoTreeDataQuerier)
		forceOneBridgeExit bool
		expectedParams     *types.CertificateBuildParams
		expectedError      string
	}{
		{
			name: "error getting last processed block",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), errors.New("some error"))
			},
			expectedError: "error getting last processed block from l2: some error",
		},
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "no new blocks to send a certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 10}, nil)
			},
			expectedParams: nil,
		},
		{
			name: "error getting bridges and claims",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return(nil, nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "no bridges and claims",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
			expectedParams: nil,
		},
		{
			name:               "no bridges when forceOneBridgeExit is true",
			forceOneBridgeExit: true,
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{{}}, nil)
			},
			expectedParams: nil,
		},
		{
			name:               "no bridges when forceOneBridgeExit is false, but has claims",
			forceOneBridgeExit: false,
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{
					{
						BlockNum:        1,
						GlobalExitRoot:  ger,
						RollupExitRoot:  rer,
						MainnetExitRoot: mer,
					}}, nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 1}, nil, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             10,
				RetryCount:          0,
				L1InfoTreeLeafCount: 1,
				CertificateType:     types.CertificateTypePP,
				LastSentCertificate: &types.CertificateHeader{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{},
				Claims: []bridgesync.Claim{
					{
						BlockNum:        1,
						RollupExitRoot:  common.HexToHash("0x1"),
						MainnetExitRoot: common.HexToHash("0x2"),
						GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
					}},
				CreatedAt:                      uint32(time.Now().UTC().Unix()),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
			},
		},
		{
			name: "error claim GER invalid",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return(
					[]bridgesync.Bridge{{}}, []bridgesync.Claim{{GlobalExitRoot: common.HexToHash("0x1")}}, nil)
			},
			expectedError: "GER mismatch",
		},
		{
			name: "error GetLatestFinalizedL1InfoRoot",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
					{
						GlobalExitRoot:  ger,
						RollupExitRoot:  rer,
						MainnetExitRoot: mer,
					}}, nil)
				mockL1InfoTreeQuerier.On("GetLatestFinalizedL1InfoRoot", ctx).Return(nil, nil, errors.New("some error"))
			},
			expectedError: "ppFlow - error getting latest finalized L1 info root: some error",
		},
		{
			name: "success",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
					{
						GlobalExitRoot:  ger,
						RollupExitRoot:  rer,
						MainnetExitRoot: mer,
					}}, nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             10,
				RetryCount:          0,
				L1InfoTreeLeafCount: 1,
				CertificateType:     types.CertificateTypePP,
				LastSentCertificate: &types.CertificateHeader{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{}},
				Claims: []bridgesync.Claim{
					{
						RollupExitRoot:  common.HexToHash("0x1"),
						MainnetExitRoot: common.HexToHash("0x2"),
						GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
					}},
				CreatedAt:                      uint32(time.Now().UTC().Unix()),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			mockLERQuerier := mocks.NewLERQuerier(t)
			logger := log.WithFields("test", "Test_PPFlow_GetCertificateBuildParams")
			ppFlow := NewPPFlow(
				logger,
				NewBaseFlow(logger, mockL2BridgeQuerier,
					mockStorage, mockL1InfoTreeQuerier, mockLERQuerier, NewBaseFlowConfigDefault()),
				mockStorage, mockL1InfoTreeQuerier, mockL2BridgeQuerier, nil, tc.forceOneBridgeExit, 0)

			tc.mockFn(mockStorage, mockL2BridgeQuerier, mockL1InfoTreeQuerier)

			params, err := ppFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}
		})
	}
}

func TestGetLastSentBlockAndRetryCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		lastSentCertificate *types.CertificateHeader
		expectedBlock       uint64
		startL2Block        uint64
		expectedRetryCount  int
	}{
		{
			name:                "No last sent certificate, start block is 0",
			lastSentCertificate: nil,
			expectedBlock:       0,
			startL2Block:        0,
			expectedRetryCount:  0,
		},
		{
			name:                "No last sent certificate, start block is 1000",
			lastSentCertificate: nil,
			expectedBlock:       1000,
			startL2Block:        1000,
			expectedRetryCount:  0,
		},
		{
			name: "Last sent certificate with no error",
			lastSentCertificate: &types.CertificateHeader{
				ToBlock: 10,
				Status:  agglayertypes.Settled,
			},
			expectedBlock:      10,
			expectedRetryCount: 0,
		},
		{
			name: "Last sent certificate with error and non-zero FromBlock",
			lastSentCertificate: &types.CertificateHeader{
				FromBlock:  5,
				ToBlock:    10,
				Status:     agglayertypes.InError,
				RetryCount: 1,
			},
			expectedBlock:      4,
			expectedRetryCount: 2,
		},
		{
			name: "Last sent certificate with error and zero FromBlock",
			lastSentCertificate: &types.CertificateHeader{
				FromBlock:  0,
				ToBlock:    10,
				Status:     agglayertypes.InError,
				RetryCount: 1,
			},
			expectedBlock:      10,
			expectedRetryCount: 2,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			baseFlow := &baseFlow{cfg: NewBaseFlowConfig(0, tt.startL2Block, false)}

			block, retryCount := baseFlow.getLastSentBlockAndRetryCount(tt.lastSentCertificate)

			require.Equal(t, tt.expectedBlock, block)
			require.Equal(t, tt.expectedRetryCount, retryCount)
		})
	}
}

func Test_PPFlow_CheckInitialStatus(t *testing.T) {
	sut := &PPFlow{}
	require.Nil(t, sut.CheckInitialStatus(context.TODO()))
}

func Test_PPFlow_SignCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name          string
		mockSignerFn  func(*mocks.Signer)
		certificate   *agglayertypes.Certificate
		expectedCert  *agglayertypes.Certificate
		expectedError string
	}{
		{
			name: "successfully signs certificate",
			mockSignerFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(ctx, mock.Anything).Return([]byte("mock_signature"), nil)
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123"))
			},
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
			},
			expectedCert: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
				AggchainData: &agglayertypes.AggchainDataSignature{
					Signature: []byte("mock_signature"),
				},
			},
		},
		{
			name: "error signing certificate",
			mockSignerFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(ctx, mock.Anything).Return(nil, errors.New("signing error"))
			},
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x456"),
			},
			expectedError: "signing error",
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockSigner := mocks.NewSigner(t)
			if tt.mockSignerFn != nil {
				tt.mockSignerFn(mockSigner)
			}
			logger := log.WithFields("test", "Test_PPFlow_SignCertificate")
			flowBase := NewBaseFlow(
				logger,
				nil, // mockL2BridgeQuerier,
				nil, // mockStorage,
				nil, // mockL1InfoTreeDataQuerier,
				nil, // mockLERQuerier,
				NewBaseFlowConfigDefault())

			ppFlow := NewPPFlow(
				logger,
				flowBase,
				nil, // storage
				nil, // l1InfoTreeDataQuerier
				nil, // l2BridgeQuerier
				mockSigner,
				false, // forceOneBridgeExit
				0,     // maxL2BlockNumber
			)

			signedCert, err := ppFlow.signCertificate(ctx, tt.certificate)

			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				require.Nil(t, signedCert)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, signedCert)
			}
		})
	}
}
