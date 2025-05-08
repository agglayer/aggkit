package flows

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/aggchainfep"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	finalizedL1Root := common.HexToHash("0x1")

	ibe1 := &agglayertypes.ImportedBridgeExit{
		BridgeExit: &agglayertypes.BridgeExit{
			LeafType:  0,
			TokenInfo: &agglayertypes.TokenInfo{},
		},
		GlobalIndex: &agglayertypes.GlobalIndex{
			LeafIndex: 1,
		},
	}

	ibe2 := &agglayertypes.ImportedBridgeExit{
		BridgeExit: &agglayertypes.BridgeExit{
			LeafType:  0,
			TokenInfo: &agglayertypes.TokenInfo{},
		},
		GlobalIndex: &agglayertypes.GlobalIndex{
			LeafIndex: 2,
		},
	}

	testCases := []struct {
		name   string
		mockFn func(*mocks.AggSenderStorage,
			*mocks.BridgeQuerier,
			*mocks.AggchainProofClientInterface,
			*mocks.L1InfoTreeDataQuerier,
			*mocks.GERQuerier,
		)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				mockStorage.EXPECT().GetLastSentCertificate().Return(nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate - have aggchain proof in db",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					AggchainProof: &types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 1,
						EndBlock:        10,
					},
				}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10), true).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  1,
				ToBlock:    10,
				RetryCount: 1,
				Bridges:    []bridgesync.Bridge{{}},
				Claims: []bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 1,
					EndBlock:        10,
				},
				LastSentCertificate: &types.CertificateInfo{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					AggchainProof: &types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 1,
						EndBlock:        10,
					},
				},
			},
		},
		{
			name: "resend InError certificate - no aggchain proof in db",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayertypes.InError,
				}, nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10), true).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockGERQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 10,
				}, uint64(1), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}}).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 0, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  1,
				ToBlock:    10,
				RetryCount: 1,
				LastSentCertificate: &types.CertificateInfo{
					FromBlock: 1,
					ToBlock:   10,
					Status:    agglayertypes.InError,
				},
				Bridges:             []bridgesync.Bridge{{}},
				L1InfoTreeLeafCount: 11,
				Claims: []bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 0,
					EndBlock:        10,
				},
			},
		},
		{
			name: "error fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(nil, nil).Twice()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10), true).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
					{
						GlobalIndex:     big.NewInt(1),
						GlobalExitRoot:  ger,
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockGERQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 10,
				}, uint64(1), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}}).Return(nil, errors.New("some error"))
			},
			expectedError: "error fetching aggchain proof for lastProvenBlock: 0, maxEndBlock: 10: some error",
		},
		{
			name: "error fetching aggchain proof for new certificate - no proofs built yet",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(nil, nil).Twice()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10), true).Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockGERQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 10,
				}, uint64(1), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{}).Return(nil, *errNoProofBuiltYet)
			},
			expectedError:  "",
			expectedParams: nil, // expecting no params to be returned since no proof was built
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10), true).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					GlobalExitRoot:  ger,
					MainnetExitRoot: mer,
					RollupExitRoot:  rer,
				}}, nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockGERQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 10,
				}, uint64(6), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(5), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}}).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 6, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             10,
				RetryCount:          0,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{}},
				L1InfoTreeLeafCount: 11,
				Claims: []bridgesync.Claim{{
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        10,
				},
				CreatedAt: uint32(time.Now().UTC().Unix()),
			},
		},
		{
			name: "success fetching aggchain proof for new certificate - aggchain prover returns smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
				ger := calculateGER(mer, rer)
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificate().Return(&types.CertificateInfo{ToBlock: 5}, nil).Twice()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10), true).Return(
					[]bridgesync.Bridge{{BlockNum: 6}, {BlockNum: 10}},
					[]bridgesync.Claim{
						{BlockNum: 8, GlobalIndex: big.NewInt(1), GlobalExitRoot: ger, MainnetExitRoot: mer, RollupExitRoot: rer},
						{BlockNum: 9, GlobalIndex: big.NewInt(2), GlobalExitRoot: ger, MainnetExitRoot: mer, RollupExitRoot: rer}},
					nil)
				mockL1InfoDataQuery.EXPECT().GetFinalizedL1InfoTreeData(ctx).Return(
					treetypes.Proof{},
					&l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					&treetypes.Root{
						Hash:  common.HexToHash("0x1"),
						Index: 10,
					},
					nil,
				)
				mockL1InfoDataQuery.EXPECT().CheckIfClaimsArePartOfFinalizedL1InfoTree(mock.Anything, mock.Anything).Return(nil)
				mockGERQuerier.EXPECT().GetInjectedGERsProofs(ctx, &treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 10,
				}, uint64(6), uint64(10)).Return(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber{}, nil)
				mockProverClient.EXPECT().GenerateAggchainProof(uint64(5), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{
						{ImportedBridgeExit: ibe1, BlockNumber: 8},
						{ImportedBridgeExit: ibe2, BlockNumber: 9},
					}).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 6, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             8,
				RetryCount:          0,
				L1InfoTreeLeafCount: 11,
				LastSentCertificate: &types.CertificateInfo{ToBlock: 5},
				Bridges:             []bridgesync.Bridge{{BlockNum: 6}},
				Claims: []bridgesync.Claim{{
					BlockNum:        8,
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
					GlobalExitRoot:  calculateGER(common.HexToHash("0x2"), common.HexToHash("0x1")),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        8,
				},
				CreatedAt: uint32(time.Now().UTC().Unix()),
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockGERQuerier := mocks.NewGERQuerier(t)
			mockL1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)

			aggchainFlow := NewAggchainProverFlow(
				log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams"),
				0, true, 0,
				mockAggchainProofClient,
				mockStorage,
				mockL1InfoTreeDataQuerier,
				mockL2BridgeQuerier,
				mockGERQuerier,
				nil,
			)

			tc.mockFn(mockStorage, mockL2BridgeQuerier, mockAggchainProofClient, mockL1InfoTreeDataQuerier, mockGERQuerier)

			params, err := aggchainFlow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockStorage.AssertExpectations(t)
			mockL2BridgeQuerier.AssertExpectations(t)
			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockL1InfoTreeDataQuerier.AssertExpectations(t)
			mockAggchainProofClient.AssertExpectations(t)
		})
	}
}

func TestGetImportedBridgeExitsForProver(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name          string
		claims        []bridgesync.Claim
		expectedExits []*agglayertypes.ImportedBridgeExitWithBlockNumber
		expectedError string
	}{
		{
			name: "error getting imported bridge exits",
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        new(big.Int).SetBytes([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}),
				},
			},
			expectedError: "aggchainProverFlow - error converting claim to imported bridge exit",
		},
		{
			name: "success",
			claims: []bridgesync.Claim{
				{
					IsMessage:          false,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        big.NewInt(1),
					BlockNum:           1,
				},
				{
					IsMessage:          true,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
					GlobalIndex:        big.NewInt(2),
					BlockNum:           2,
				},
			},
			expectedExits: []*agglayertypes.ImportedBridgeExitWithBlockNumber{
				{
					ImportedBridgeExit: &agglayertypes.ImportedBridgeExit{
						BridgeExit: &agglayertypes.BridgeExit{
							LeafType: agglayertypes.LeafTypeAsset,
							TokenInfo: &agglayertypes.TokenInfo{
								OriginNetwork:      1,
								OriginTokenAddress: common.HexToAddress("0x123"),
							},
							DestinationNetwork: 2,
							DestinationAddress: common.HexToAddress("0x456"),
							Amount:             big.NewInt(100),
							Metadata:           []byte("metadata"),
						},
						GlobalIndex: &agglayertypes.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 0,
							LeafIndex:   1,
						},
					},
					BlockNumber: 1,
				},
				{
					ImportedBridgeExit: &agglayertypes.ImportedBridgeExit{
						BridgeExit: &agglayertypes.BridgeExit{
							LeafType: agglayertypes.LeafTypeMessage,
							TokenInfo: &agglayertypes.TokenInfo{
								OriginNetwork:      1,
								OriginTokenAddress: common.HexToAddress("0x123"),
							},
							DestinationNetwork: 2,
							DestinationAddress: common.HexToAddress("0x456"),
							Amount:             big.NewInt(100),
							Metadata:           []byte("metadata"),
						},
						GlobalIndex: &agglayertypes.GlobalIndex{
							MainnetFlag: false,
							RollupIndex: 0,
							LeafIndex:   2,
						},
					},
					BlockNumber: 2,
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow := &AggchainProverFlow{
				baseFlow: &baseFlow{
					log: log.WithFields("flowManager", "TestGetImportedBridgeExitsForProver"),
				},
			}

			exits, err := flow.getImportedBridgeExitsForProver(tc.claims)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedExits, exits)
			}
		})
	}
}

func Test_AggchainProverFlow_getLastProvenBlock(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		fromBlock      uint64
		startL2Block   uint64
		expectedResult uint64
	}{
		{
			name:           "fromBlock is 0, return startL2Block",
			fromBlock:      0,
			startL2Block:   1,
			expectedResult: 1,
		},
		{
			name:           "fromBlock is 0, startL2Block is 0",
			fromBlock:      0,
			startL2Block:   0,
			expectedResult: 0,
		},
		{
			name:           "fromBlock is greater than 0",
			fromBlock:      10,
			startL2Block:   1,
			expectedResult: 9,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			flow := &AggchainProverFlow{
				baseFlow: &baseFlow{
					startL2Block: tc.startL2Block,
				},
			}

			result := flow.getLastProvenBlock(tc.fromBlock)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

func Test_AggchainProverFlow_BuildCertificate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	createdAt := time.Now().UTC()

	testCases := []struct {
		name           string
		mockFn         func(*mocks.BridgeQuerier)
		buildParams    *types.CertificateBuildParams
		expectedError  string
		expectedResult *agglayertypes.Certificate
	}{
		{
			name: "error building certificate",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, uint32(0)).Return(common.Hash{}, errors.New("some error"))
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []bridgesync.Claim{},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
			},
			expectedError: "error getting exit root by index",
		},
		{
			name: "success building certificate",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1))
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{},
				Claims:                         []bridgesync.Claim{},
				CreatedAt:                      uint32(createdAt.Unix()),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					LastProvenBlock: 1,
					EndBlock:        10,
					CustomChainData: []byte("some-data"),
					LocalExitRoot:   common.HexToHash("0x1"),
					AggchainParams:  common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
				},
			},
			expectedResult: &agglayertypes.Certificate{
				NetworkID:           1,
				Height:              0,
				NewLocalExitRoot:    zeroLER,
				CustomChainData:     []byte("some-data"),
				Metadata:            types.NewCertificateMetadata(1, 9, uint32(createdAt.Unix())).ToHash(),
				BridgeExits:         []*agglayertypes.BridgeExit{},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
				PrevLocalExitRoot:   zeroLER,
				L1InfoTreeLeafCount: 0,
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof:          []byte("some-proof"),
					Version:        "0.1",
					Vkey:           []byte("some-vkey"),
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeQuerier)
			}

			aggchainFlow := &AggchainProverFlow{
				baseFlow: &baseFlow{
					log:             log.WithFields("flowManager", "Test_AggchainProverFlow_BuildCertificate"),
					l2BridgeQuerier: mockL2BridgeQuerier,
				},
			}

			certificate, err := aggchainFlow.BuildCertificate(ctx, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, certificate)
				require.Equal(t, tc.expectedResult, certificate)
			}
		})
	}
}

func Test_AggchainProverFlow_CheckInitialStatus(t *testing.T) {
	mockStorage := mocks.NewAggSenderStorage(t)
	sut := &AggchainProverFlow{
		baseFlow: &baseFlow{
			log:          log.WithFields("flowManager", "Test_AggchainProverFlow_BuildCertificate"),
			storage:      mockStorage,
			startL2Block: 1234,
		},
	}
	exampleError := fmt.Errorf("some error")
	testCases := []struct {
		name                        string
		cert                        *types.CertificateInfo
		getLastSentCertificateError error
		expectedError               bool
	}{
		{
			name:                        "error getting last sent certificate",
			cert:                        nil,
			getLastSentCertificateError: exampleError,
			expectedError:               true,
		},
		{
			name:          "no last sent certificate on storage",
			cert:          nil,
			expectedError: false,
		},
		{
			name: "last cert after upgrade L2 block (startL2Block) that is OK",
			cert: &types.CertificateInfo{
				ToBlock: 4000,
			},
			expectedError: false,
		},
		{
			name: "last cert is immediately before upgrade L2 block (startL2Block) that is OK",
			cert: &types.CertificateInfo{
				ToBlock: 1233,
			},
			expectedError: false,
		},
		{
			name: "last cert is 2 block below upgrade L2 block (startL2Block) so it's a gap of block 1233. Error",
			cert: &types.CertificateInfo{
				ToBlock: 1232,
			},
			expectedError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage.EXPECT().GetLastSentCertificate().Return(tc.cert, tc.getLastSentCertificateError).Once()
			err := sut.CheckInitialStatus(context.TODO())
			if tc.expectedError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			mockStorage.AssertExpectations(t)
		})
	}
}

func getResponseContractCallStartingBlockNumber(returnValue int64) ([]byte, error) {
	expectedBlockNumber := big.NewInt(returnValue)
	parsedABI, err := abi.JSON(strings.NewReader(aggchainfep.AggchainfepABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}
	method := parsedABI.Methods["startingBlockNumber"]
	encodedReturnValue, err := method.Outputs.Pack(expectedBlockNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to pack method: %w", err)
	}
	return encodedReturnValue, nil
}

func Test_AggchainProverFlow_getL2StartBlock(t *testing.T) {
	t.Parallel()

	sovereignRollupAddr := common.HexToAddress("0x123")

	testCases := []struct {
		name          string
		mockFn        func(mockEthClient *mocks.EthClient)
		expectedBlock uint64
		expectedError string
	}{
		{
			name: "error creating sovereign rollup caller",
			mockFn: func(mockEthClient *mocks.EthClient) {
				mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow",
		},
		{
			name: "ok fetching starting block number",
			mockFn: func(mockEthClient *mocks.EthClient) {
				encodedReturnValue, err := getResponseContractCallStartingBlockNumber(12345)
				if err != nil {
					t.Fatalf("failed to pack method: %v", err)
				}
				mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(
					encodedReturnValue, nil)
			},
			expectedBlock: 12345,
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockEthClient := mocks.NewEthClient(t)

			tc.mockFn(mockEthClient)

			block, err := getL2StartBlock(sovereignRollupAddr, mockEthClient)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBlock, block)
			}

			mockEthClient.AssertExpectations(t)
		})
	}
}
