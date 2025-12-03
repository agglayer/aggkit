package flows

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func timeNowUTCForTest() uint32 {
	return uint32(12345)
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
					LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
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
				BridgeExits: []*agglayertypes.BridgeExit{
					{
						LeafType: bridgetypes.LeafTypeAsset,
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
							LeafType: bridgetypes.LeafTypeAsset,
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
					LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
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
				cfg:                   NewBaseFlowConfigDefault(),
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
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), errors.New("some error"))
			},
			expectedError: "error getting last processed block from l2: some error",
		},
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
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
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
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
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
			},
			expectedParams: nil,
		},
		{
			name:               "no bridges when forceOneBridgeExit is true",
			forceOneBridgeExit: true,
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{{GlobalExitRoot: common.HexToHash("0x1")}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(common.HexToHash("0x1"), uint32(1)).Return(true, nil).Once()
			},
			expectedParams: nil,
		},
		{
			name:               "error checking if GER finalized",
			forceOneBridgeExit: true,
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{{GlobalExitRoot: common.HexToHash("0x1"), BlockNum: 10}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(common.HexToHash("0x1"), uint32(1)).Return(false, errors.New("some error")).Once()
			},
			expectedParams: nil,
			expectedError:  "error checking if GER 0x0000000000000000000000000000000000000000000000000000000000000001 is finalized: some error",
		},
		{
			name:               "GER not finalized - adjust certificate build params",
			forceOneBridgeExit: false,
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				rer1 := common.HexToHash("0x1")
				mer1 := common.HexToHash("0x2")
				ger1 := l1infotreesync.CalculateGER(mer1, rer1)
				rer2 := common.HexToHash("0x3")
				mer2 := common.HexToHash("0x4")
				ger2 := l1infotreesync.CalculateGER(mer2, rer2)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{
					{
						BlockNum:        9,
						GlobalExitRoot:  ger1,
						RollupExitRoot:  rer1,
						MainnetExitRoot: mer1,
					},
					{
						BlockNum:        10,
						GlobalExitRoot:  ger2,
						RollupExitRoot:  rer2,
						MainnetExitRoot: mer2,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 1}, nil, nil)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(1)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(1)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             9,
				RetryCount:          0,
				L1InfoTreeLeafCount: 1,
				CertificateType:     types.CertificateTypePP,
				LastSentCertificate: &types.CertificateHeader{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{},
				Claims: []bridgesync.Claim{
					{
						BlockNum:        9,
						RollupExitRoot:  common.HexToHash("0x1"),
						MainnetExitRoot: common.HexToHash("0x2"),
						GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
					},
				},
				Unclaims:                       []bridgetypes.Unclaim{},
				CreatedAt:                      timeNowUTCForTest(),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
			},
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
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{
					{
						BlockNum:        1,
						GlobalExitRoot:  ger,
						RollupExitRoot:  rer,
						MainnetExitRoot: mer,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 1}, nil, nil)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger, uint32(1)).Return(true, nil).Once()
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
						GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
					}},
				Unclaims:                       []bridgetypes.Unclaim{},
				CreatedAt:                      timeNowUTCForTest(),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x123"),
			},
		},
		{
			name: "error claim GER invalid",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return(
					[]bridgesync.Bridge{{}}, []bridgesync.Claim{{GlobalExitRoot: common.HexToHash("0x1")}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(common.HexToHash("0x1"), uint32(1)).Return(true, nil).Once()
			},
			expectedError: "GER mismatch",
		},
		{
			name: "error GetLatestFinalizedL1InfoRoot",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL1InfoTreeQuerier.On("GetLatestFinalizedL1InfoRoot", ctx).Return(nil, nil, errors.New("some error"))
			},
			expectedError: "error generating pre build params: error getting latest finalized L1 info root: some error",
		},
		{
			name: "success",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := l1infotreesync.CalculateGER(mer, rer)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(10), nil)
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
					{
						GlobalExitRoot:  ger,
						RollupExitRoot:  rer,
						MainnetExitRoot: mer,
					}}, nil)
				mockL2BridgeQuerier.EXPECT().GetUnsetClaimsForBlockRange(ctx, uint64(6), uint64(10)).Return([]bridgetypes.Unclaim{}, nil)
				mockL1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(ctx).Return(
					&treetypes.Root{Hash: common.HexToHash("0x123"), BlockNum: 10}, nil, nil)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger, uint32(1)).Return(true, nil).Once()
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
						GlobalExitRoot:  l1infotreesync.CalculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
					}},
				Unclaims:                       []bridgetypes.Unclaim{},
				CreatedAt:                      timeNowUTCForTest(),
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
			baseFlow := NewBaseFlow(logger, mockL2BridgeQuerier,
				mockStorage, mockL1InfoTreeQuerier, mockLERQuerier, NewBaseFlowConfigDefault())
			baseFlow.timeNowFunc = timeNowUTCForTest
			ppFlow := NewPPBuilderFlow(
				logger,
				baseFlow,
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

			baseFlow := &baseFlow{cfg: NewBaseFlowConfig(0, tt.startL2Block, false, true)}

			block, retryCount := baseFlow.getLastSentBlockAndRetryCount(tt.lastSentCertificate)

			require.Equal(t, tt.expectedBlock, block)
			require.Equal(t, tt.expectedRetryCount, retryCount)
		})
	}
}

func Test_PPFlow_CheckInitialStatus(t *testing.T) {
	sut := &PPBuilderFlow{}
	require.Nil(t, sut.CheckInitialStatus(context.TODO()))
}

func Test_PPFlow_UpdateAggchainData(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		certificate         *agglayertypes.Certificate
		multisig            *agglayertypes.Multisig
		expectedCertificate *agglayertypes.Certificate
	}{
		{
			name: "multisig nil - leaves certificate unchanged",
			certificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataSignature{
					Signature: []byte("orig_sig"),
				},
			},
			multisig: nil,
			expectedCertificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataSignature{
					Signature: []byte("orig_sig"),
				},
			},
		},
		{
			name:        "multisig provided - replaces AggchainData with multisig wrapper",
			certificate: &agglayertypes.Certificate{},
			multisig:    &agglayertypes.Multisig{},
			expectedCertificate: &agglayertypes.Certificate{
				AggchainData: &agglayertypes.AggchainDataMultisig{
					Multisig: &agglayertypes.Multisig{},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sut := &PPBuilderFlow{}

			err := sut.UpdateAggchainData(tc.certificate, tc.multisig)
			require.NoError(t, err)
			require.Equal(t, tc.expectedCertificate, tc.certificate)
		})
	}
}
