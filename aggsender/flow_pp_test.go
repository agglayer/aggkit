package aggsender

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treeTypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConvertClaimToImportedBridgeExit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		claim         bridgesync.Claim
		expectedError bool
		expectedExit  *agglayer.ImportedBridgeExit
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
			expectedExit: &agglayer.ImportedBridgeExit{
				BridgeExit: &agglayer.BridgeExit{
					LeafType: agglayer.LeafTypeAsset,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				GlobalIndex: &agglayer.GlobalIndex{
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
			expectedExit: &agglayer.ImportedBridgeExit{
				BridgeExit: &agglayer.BridgeExit{
					LeafType: agglayer.LeafTypeMessage,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				GlobalIndex: &agglayer.GlobalIndex{
					MainnetFlag: false,
					RollupIndex: 0,
					LeafIndex:   2,
				},
			},
		},
		{
			name: "Invalid global index",
			claim: bridgesync.Claim{
				IsMessage:          false,
				OriginNetwork:      1,
				OriginAddress:      common.HexToAddress("0x123"),
				DestinationNetwork: 2,
				DestinationAddress: common.HexToAddress("0x456"),
				Amount:             big.NewInt(100),
				Metadata:           []byte("metadata"),
				GlobalIndex:        new(big.Int).SetBytes([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}),
			},
			expectedError: true,
			expectedExit:  nil,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flow := &baseFlow{}
			exit, err := flow.convertClaimToImportedBridgeExit(tt.claim)

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
		expectedExits []*agglayer.BridgeExit
	}{
		{
			name: "Single bridge",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayer.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
			},
			expectedExits: []*agglayer.BridgeExit{
				{
					LeafType: agglayer.LeafTypeAsset,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
			},
		},
		{
			name: "Multiple bridges",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayer.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				{
					LeafType:           agglayer.LeafTypeMessage.Uint8(),
					OriginNetwork:      3,
					OriginAddress:      common.HexToAddress("0x789"),
					DestinationNetwork: 4,
					DestinationAddress: common.HexToAddress("0xabc"),
					Amount:             big.NewInt(200),
					Metadata:           []byte("data"),
				},
			},
			expectedExits: []*agglayer.BridgeExit{
				{
					LeafType: agglayer.LeafTypeAsset,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				{
					LeafType: agglayer.LeafTypeMessage,
					TokenInfo: &agglayer.TokenInfo{
						OriginNetwork:      3,
						OriginTokenAddress: common.HexToAddress("0x789"),
					},
					DestinationNetwork: 4,
					DestinationAddress: common.HexToAddress("0xabc"),
					Amount:             big.NewInt(200),
					Metadata:           []byte("data"),
				},
			},
		},
		{
			name:          "No bridges",
			bridges:       []bridgesync.Bridge{},
			expectedExits: []*agglayer.BridgeExit{},
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

	mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
	mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
		L1InfoTreeIndex:   1,
		Timestamp:         123456789,
		PreviousBlockHash: common.HexToHash("0xabc"),
		GlobalExitRoot:    common.HexToHash("0x7891"),
	}, nil)
	mockL1InfoTreeSyncer.On("GetL1InfoTreeRootByIndex", mock.Anything, mock.Anything).Return(
		treeTypes.Root{Hash: common.HexToHash("0x7891")}, nil)
	mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", mock.Anything,
		mock.Anything, mock.Anything).Return(mockProof, nil)

	tests := []struct {
		name          string
		claims        []bridgesync.Claim
		expectedError bool
		expectedExits []*agglayer.ImportedBridgeExit
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
			expectedError: false,
			expectedExits: []*agglayer.ImportedBridgeExit{
				{
					BridgeExit: &agglayer.BridgeExit{
						LeafType: agglayer.LeafTypeAsset,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x1234"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x4567"),
						Amount:             big.NewInt(111),
						Metadata:           []byte("metadata1"),
					},
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 1,
						LeafIndex:   1,
					},
					ClaimData: &agglayer.ClaimFromRollup{
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0xaaab"),
							MainnetExitRoot: common.HexToHash("0xbbba"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7891"),
								Timestamp:      123456789,
								BlockHash:      common.HexToHash("0xabc"),
							},
						},
						ProofLeafLER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0xc52019815b51acf67a715cae6794a20083d63fd9af45783b7adf69123dae92c8"),
							Proof: mockProof,
						},
						ProofLERToRER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0xaaab"),
							Proof: mockProof,
						},
						ProofGERToL1Root: &agglayer.MerkleProof{
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
			expectedError: false,
			expectedExits: []*agglayer.ImportedBridgeExit{
				{
					BridgeExit: &agglayer.BridgeExit{
						LeafType: agglayer.LeafTypeAsset,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x123"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x456"),
						Amount:             big.NewInt(100),
						Metadata:           []byte("metadata"),
					},
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: false,
						RollupIndex: 0,
						LeafIndex:   1,
					},
					ClaimData: &agglayer.ClaimFromRollup{
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0xaaa"),
							MainnetExitRoot: common.HexToHash("0xbbb"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7891"),
								Timestamp:      123456789,
								BlockHash:      common.HexToHash("0xabc"),
							},
						},
						ProofLeafLER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x105e0f1144e57f6fb63f1dfc5083b1f59be3512be7cf5e63523779ad14a4d987"),
							Proof: mockProof,
						},
						ProofLERToRER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0xaaa"),
							Proof: mockProof,
						},
						ProofGERToL1Root: &agglayer.MerkleProof{
							Root:  common.HexToHash("0x7891"),
							Proof: mockProof,
						},
					},
				},
				{
					BridgeExit: &agglayer.BridgeExit{
						LeafType: agglayer.LeafTypeMessage,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      3,
							OriginTokenAddress: common.HexToAddress("0x789"),
						},
						DestinationNetwork: 4,
						DestinationAddress: common.HexToAddress("0xabc"),
						Amount:             big.NewInt(200),
						Metadata:           []byte("data"),
					},
					GlobalIndex: &agglayer.GlobalIndex{
						MainnetFlag: true,
						RollupIndex: 0,
						LeafIndex:   2,
					},
					ClaimData: &agglayer.ClaimFromMainnnet{
						L1Leaf: &agglayer.L1InfoTreeLeaf{
							L1InfoTreeIndex: 1,
							RollupExitRoot:  common.HexToHash("0xbbb"),
							MainnetExitRoot: common.HexToHash("0xccc"),
							Inner: &agglayer.L1InfoTreeLeafInner{
								GlobalExitRoot: common.HexToHash("0x7891"),
								Timestamp:      123456789,
								BlockHash:      common.HexToHash("0xabc"),
							},
						},
						ProofLeafMER: &agglayer.MerkleProof{
							Root:  common.HexToHash("0xccc"),
							Proof: mockProof,
						},
						ProofGERToL1Root: &agglayer.MerkleProof{
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
			expectedExits: []*agglayer.ImportedBridgeExit{},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			flow := &baseFlow{
				l1InfoTreeSyncer: mockL1InfoTreeSyncer,
				log:              log.WithFields("test", "unittest"),
			}
			exits, err := flow.getImportedBridgeExits(context.Background(), tt.claims)

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
	mockL2BridgeSyncer := mocks.NewL2BridgeSyncer(t)
	mockL1InfoTreeSyncer := mocks.NewL1InfoTreeSyncer(t)
	mockProof := generateTestProof(t)

	tests := []struct {
		name                    string
		bridges                 []bridgesync.Bridge
		claims                  []bridgesync.Claim
		lastSentCertificateInfo types.CertificateInfo
		fromBlock               uint64
		toBlock                 uint64
		mockFn                  func()
		expectedCert            *agglayer.Certificate
		expectedError           bool
	}{
		{
			name: "Valid certificate with bridges and claims",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayer.LeafTypeAsset.Uint8(),
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
			lastSentCertificateInfo: types.CertificateInfo{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
				Status:           agglayer.Settled,
			},
			fromBlock: 0,
			toBlock:   10,
			expectedCert: &agglayer.Certificate{
				NetworkID:         1,
				PrevLocalExitRoot: common.HexToHash("0x123"),
				NewLocalExitRoot:  common.HexToHash("0x789"),
				Metadata:          types.NewCertificateMetadata(0, 10, 0).ToHash(),
				BridgeExits: []*agglayer.BridgeExit{
					{
						LeafType: agglayer.LeafTypeAsset,
						TokenInfo: &agglayer.TokenInfo{
							OriginNetwork:      1,
							OriginTokenAddress: common.HexToAddress("0x123"),
						},
						DestinationNetwork: 2,
						DestinationAddress: common.HexToAddress("0x456"),
						Amount:             big.NewInt(100),
						Metadata:           []byte("metadata"),
					},
				},
				ImportedBridgeExits: []*agglayer.ImportedBridgeExit{
					{
						BridgeExit: &agglayer.BridgeExit{
							LeafType: agglayer.LeafTypeAsset,
							TokenInfo: &agglayer.TokenInfo{
								OriginNetwork:      1,
								OriginTokenAddress: common.HexToAddress("0x1234"),
							},
							DestinationNetwork: 2,
							DestinationAddress: common.HexToAddress("0x4567"),
							Amount:             big.NewInt(111),
							Metadata:           []byte("metadata1"),
						},
						GlobalIndex: &agglayer.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 0,
							LeafIndex:   1,
						},
						ClaimData: &agglayer.ClaimFromRollup{
							L1Leaf: &agglayer.L1InfoTreeLeaf{
								L1InfoTreeIndex: 1,
								RollupExitRoot:  common.HexToHash("0xaaab"),
								MainnetExitRoot: common.HexToHash("0xbbba"),
								Inner: &agglayer.L1InfoTreeLeafInner{
									GlobalExitRoot: common.HexToHash("0x7891"),
									Timestamp:      123456789,
									BlockHash:      common.HexToHash("0xabc"),
								},
							},
							ProofLeafLER: &agglayer.MerkleProof{
								Root:  common.HexToHash("0xc52019815b51acf67a715cae6794a20083d63fd9af45783b7adf69123dae92c8"),
								Proof: mockProof,
							},
							ProofLERToRER: &agglayer.MerkleProof{
								Root:  common.HexToHash("0xaaab"),
								Proof: mockProof,
							},
							ProofGERToL1Root: &agglayer.MerkleProof{
								Root:  common.HexToHash("0x7891"),
								Proof: mockProof,
							},
						},
					},
				},
				Height: 2,
			},
			mockFn: func() {
				mockL2BridgeSyncer.On("OriginNetwork").Return(uint32(1))
				mockL2BridgeSyncer.On("GetExitRootByIndex", mock.Anything, mock.Anything).Return(treeTypes.Root{Hash: common.HexToHash("0x789")}, nil)

				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex:   1,
					Timestamp:         123456789,
					PreviousBlockHash: common.HexToHash("0xabc"),
					GlobalExitRoot:    common.HexToHash("0x7891"),
				}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeRootByIndex", mock.Anything, mock.Anything).Return(treeTypes.Root{Hash: common.HexToHash("0x7891")}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeMerkleProofFromIndexToRoot", mock.Anything, mock.Anything, mock.Anything).Return(mockProof, nil)
			},
			expectedError: false,
		},
		{
			name:    "No bridges or claims",
			bridges: []bridgesync.Bridge{},
			claims:  []bridgesync.Claim{},
			lastSentCertificateInfo: types.CertificateInfo{
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
					LeafType:           agglayer.LeafTypeAsset.Uint8(),
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
			lastSentCertificateInfo: types.CertificateInfo{
				NewLocalExitRoot: common.HexToHash("0x123"),
				Height:           1,
			},
			mockFn: func() {
				mockL1InfoTreeSyncer.On("GetInfoByGlobalExitRoot", mock.Anything).Return(&l1infotreesync.L1InfoTreeLeaf{
					L1InfoTreeIndex:   1,
					Timestamp:         123456789,
					PreviousBlockHash: common.HexToHash("0xabc"),
					GlobalExitRoot:    common.HexToHash("0x7891"),
				}, nil)
				mockL1InfoTreeSyncer.On("GetL1InfoTreeRootByIndex", mock.Anything, mock.Anything).Return(
					treeTypes.Root{Hash: common.HexToHash("0x7891")}, nil)
			},
			expectedCert:  nil,
			expectedError: true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			mockL1InfoTreeSyncer.ExpectedCalls = nil
			mockL2BridgeSyncer.ExpectedCalls = nil

			if tt.mockFn != nil {
				tt.mockFn()
			}

			flow := &baseFlow{
				l2Syncer:         mockL2BridgeSyncer,
				l1InfoTreeSyncer: mockL1InfoTreeSyncer,
				log:              log.WithFields("test", "unittest"),
			}

			certParam := &types.CertificateBuildParams{
				ToBlock: tt.toBlock,
				Bridges: tt.bridges,
				Claims:  tt.claims,
			}
			cert, err := flow.buildCertificate(context.Background(), certParam, &tt.lastSentCertificateInfo)

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

func generateTestProof(t *testing.T) treeTypes.Proof {
	t.Helper()

	proof := treeTypes.Proof{}

	for i := 0; i < int(treeTypes.DefaultHeight) && i < 10; i++ {
		proof[i] = common.HexToHash(fmt.Sprintf("0x%d", i))
	}

	return proof
}

func TestGetNextHeightAndPreviousLER(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                           string
		lastSentCertificateInfo        *types.CertificateInfo
		lastSettleCertificateInfoCall  bool
		lastSettleCertificateInfo      *types.CertificateInfo
		lastSettleCertificateInfoError error
		expectedHeight                 uint64
		expectedPreviousLER            common.Hash
		expectedError                  bool
	}{
		{
			name: "Normal case",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayer.Settled,
			},
			expectedHeight:      11,
			expectedPreviousLER: common.HexToHash("0x123"),
		},
		{
			name:                    "First certificate",
			lastSentCertificateInfo: nil,
			expectedHeight:          0,
			expectedPreviousLER:     zeroLER,
		},
		{
			name: "First certificate error, with prevLER",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:                0,
				NewLocalExitRoot:      common.HexToHash("0x123"),
				Status:                agglayer.InError,
				PreviousLocalExitRoot: &ler1,
			},
			expectedHeight:      0,
			expectedPreviousLER: ler1,
		},
		{
			name: "First certificate error, no prevLER",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:           0,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayer.InError,
			},
			expectedHeight:      0,
			expectedPreviousLER: zeroLER,
		},
		{
			name: "n certificate error, prevLER",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:                10,
				NewLocalExitRoot:      common.HexToHash("0x123"),
				PreviousLocalExitRoot: &ler1,
				Status:                agglayer.InError,
			},
			expectedHeight:      10,
			expectedPreviousLER: ler1,
		},
		{
			name: "last cert not closed, error",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:                10,
				NewLocalExitRoot:      common.HexToHash("0x123"),
				PreviousLocalExitRoot: &ler1,
				Status:                agglayer.Pending,
			},
			expectedHeight:      10,
			expectedPreviousLER: ler1,
			expectedError:       true,
		},
		{
			name: "Previous certificate in error, no prevLER",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayer.InError,
			},
			lastSettleCertificateInfo: &types.CertificateInfo{
				Height:           9,
				NewLocalExitRoot: common.HexToHash("0x3456"),
				Status:           agglayer.Settled,
			},
			expectedHeight:      10,
			expectedPreviousLER: common.HexToHash("0x3456"),
		},
		{
			name: "Previous certificate in error, no prevLER. Error getting previous cert",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayer.InError,
			},
			lastSettleCertificateInfo:      nil,
			lastSettleCertificateInfoError: errors.New("error getting last settle certificate"),
			expectedError:                  true,
		},
		{
			name: "Previous certificate in error, no prevLER. prev cert not available on storage",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayer.InError,
			},
			lastSettleCertificateInfoCall:  true,
			lastSettleCertificateInfo:      nil,
			lastSettleCertificateInfoError: nil,
			expectedError:                  true,
		},
		{
			name: "Previous certificate in error, no prevLER. prev cert not available on storage",
			lastSentCertificateInfo: &types.CertificateInfo{
				Height:           10,
				NewLocalExitRoot: common.HexToHash("0x123"),
				Status:           agglayer.InError,
			},
			lastSettleCertificateInfo: &types.CertificateInfo{
				Height:           9,
				NewLocalExitRoot: common.HexToHash("0x3456"),
				Status:           agglayer.InError,
			},
			lastSettleCertificateInfoError: nil,
			expectedError:                  true,
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			storageMock := mocks.NewAggSenderStorage(t)
			flow := &baseFlow{log: log.WithFields("aggsender-test", "getNextHeightAndPreviousLER"), storage: storageMock}
			if tt.lastSettleCertificateInfoCall || tt.lastSettleCertificateInfo != nil || tt.lastSettleCertificateInfoError != nil {
				storageMock.EXPECT().GetCertificateByHeight(mock.Anything).Return(tt.lastSettleCertificateInfo, tt.lastSettleCertificateInfoError).Once()
			}

			height, previousLER, err := flow.getNextHeightAndPreviousLER(tt.lastSentCertificateInfo)
			if tt.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedHeight, height)
				require.Equal(t, tt.expectedPreviousLER, previousLER)
			}
		})
	}
}

func TestGetBridgesAndClaims(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name            string
		fromBlock       uint64
		toBlock         uint64
		mockFn          func(*mocks.L2BridgeSyncer)
		expectedBridges []bridgesync.Bridge
		expectedClaims  []bridgesync.Claim
		expectedError   string
	}{
		{
			name:      "error getting bridges",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name:      "no bridges consumed",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{}, nil)
			},
			expectedBridges: nil,
			expectedClaims:  nil,
		},
		{
			name:      "error getting claims",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name:      "no claims consumed",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{}, nil)
			},
			expectedBridges: []bridgesync.Bridge{{}},
			expectedClaims:  []bridgesync.Claim{},
		},
		{
			name:      "success",
			fromBlock: 1,
			toBlock:   10,
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(1), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
			},
			expectedBridges: []bridgesync.Bridge{{}},
			expectedClaims:  []bridgesync.Claim{{}},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			fm := &baseFlow{
				l2Syncer: mockL2Syncer,
				log:      log.WithFields("flowManager", "TestGetBridgesAndClaims"),
			}

			tc.mockFn(mockL2Syncer)

			bridges, claims, err := fm.getBridgesAndClaims(ctx, tc.fromBlock, tc.toBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBridges, bridges)
				require.Equal(t, tc.expectedClaims, claims)
			}
		})
	}
}

func Test_PPFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.AggSenderStorage, *mocks.L2BridgeSyncer)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last processed block",
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(0), errors.New("some error"))
			},
			expectedError: "error getting last processed block from l2: some error",
		},
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "no new blocks to send a certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 10}, nil)
			},
			expectedParams: nil,
		},
		{
			name: "error getting bridges and claims",
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "success",
			mockFn: func(mockStorage *mocks.AggSenderStorage, mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockStorage.On("GetLastSentCertificate").Return(&types.CertificateInfo{ToBlock: 5}, nil)
				mockL2Syncer.On("GetBridgesPublished", ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, nil)
				mockL2Syncer.On("GetClaims", ctx, uint64(6), uint64(10)).Return([]bridgesync.Claim{{}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             10,
				RetryCount:          0,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{}},
				Claims:              []bridgesync.Claim{{}},
				CreatedAt:           uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			ppFlow := newPPFlow(log.WithFields("flowManager", "Test_PPFlow_GetCertificateBuildParams"), Config{}, mockStorage, nil, mockL2Syncer)

			tc.mockFn(mockStorage, mockL2Syncer)

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
		name                    string
		lastSentCertificateInfo *types.CertificateInfo
		expectedBlock           uint64
		expectedRetryCount      int
	}{
		{
			name:                    "No last sent certificate",
			lastSentCertificateInfo: nil,
			expectedBlock:           0,
			expectedRetryCount:      0,
		},
		{
			name: "Last sent certificate with no error",
			lastSentCertificateInfo: &types.CertificateInfo{
				ToBlock: 10,
				Status:  agglayer.Settled,
			},
			expectedBlock:      10,
			expectedRetryCount: 0,
		},
		{
			name: "Last sent certificate with error and non-zero FromBlock",
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  5,
				ToBlock:    10,
				Status:     agglayer.InError,
				RetryCount: 1,
			},
			expectedBlock:      4,
			expectedRetryCount: 2,
		},
		{
			name: "Last sent certificate with error and zero FromBlock",
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  0,
				ToBlock:    10,
				Status:     agglayer.InError,
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

			block, retryCount := getLastSentBlockAndRetryCount(tt.lastSentCertificateInfo)

			require.Equal(t, tt.expectedBlock, block)
			require.Equal(t, tt.expectedRetryCount, retryCount)
		})
	}
}
