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
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
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
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, errors.New("some error"))
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
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:                  0,
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					CertificateID:           common.HexToHash("0x1"),
					CertType:                types.CertificateTypeFEP,
				},
					&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 1,
						EndBlock:        10,
					}, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
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
				LastSentCertificate: &types.CertificateHeader{
					FromBlock:               1,
					ToBlock:                 10,
					Status:                  agglayertypes.InError,
					FinalizedL1InfoTreeRoot: &finalizedL1Root,
					CertificateID:           common.HexToHash("0x1"),
					CertType:                types.CertificateTypeFEP,
				},
				CertificateType: types.CertificateTypeFEP,
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
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:        0,
					FromBlock:     1,
					ToBlock:       10,
					Status:        agglayertypes.InError,
					CertificateID: common.HexToHash("0x1"),
					CertType:      types.CertificateTypeFEP,
				}, nil, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
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
				mockProverClient.EXPECT().GenerateAggchainProof(context.Background(), types.NewAggchainProofRequest(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}})).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 0, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				CertificateType: types.CertificateTypeFEP,
				FromBlock:       1,
				ToBlock:         10,
				RetryCount:      1,
				LastSentCertificate: &types.CertificateHeader{
					FromBlock:     1,
					ToBlock:       10,
					Status:        agglayertypes.InError,
					CertificateID: common.HexToHash("0x1"),
					CertType:      types.CertificateTypeFEP,
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
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil).Once()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{
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
				mockProverClient.EXPECT().GenerateAggchainProof(context.Background(), types.NewAggchainProofRequest(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}})).Return(nil, errors.New("some error"))
			},
			expectedError: "error fetching aggchain proof (optimisticMode: false) for lastProvenBlock: 0, maxEndBlock: 10. Err: some error.",
		},
		{
			name: "error fetching aggchain proof for new certificate - no proofs built yet",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockProverClient *mocks.AggchainProofClientInterface,
				mockL1InfoDataQuery *mocks.L1InfoTreeDataQuerier,
				mockGERQuerier *mocks.GERQuerier) {
				l1Header := &gethtypes.Header{Number: big.NewInt(10)}
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil).Once()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(10)).Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
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

				wrappedErr := fmt.Errorf("wrapped error: %w", errNoProofBuiltYet)

				mockProverClient.EXPECT().GenerateAggchainProof(context.Background(), types.NewAggchainProofRequest(uint64(0), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{})).Return(nil, wrappedErr)
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
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil).Once()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{{
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
				mockProverClient.EXPECT().GenerateAggchainProof(context.Background(), types.NewAggchainProofRequest(uint64(5), uint64(10),
					common.HexToHash("0x1"), l1infotreesync.L1InfoTreeLeaf{
						BlockNumber: l1Header.Number.Uint64(),
						Hash:        common.HexToHash("0x2"),
					},
					agglayertypes.MerkleProof{
						Root:  common.HexToHash("0x1"),
						Proof: treetypes.Proof{},
					}, make(map[common.Hash]*agglayertypes.ProvenInsertedGERWithBlockNumber, 0),
					[]*agglayertypes.ImportedBridgeExitWithBlockNumber{{ImportedBridgeExit: ibe1}})).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 6, EndBlock: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  6,
				ToBlock:    10,
				RetryCount: 0,
				LastSentCertificate: &types.CertificateHeader{
					ToBlock: 5,
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
					LastProvenBlock: 6,
					EndBlock:        10,
				},
				CreatedAt:       uint32(time.Now().UTC().Unix()),
				CertificateType: types.CertificateTypeFEP,
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
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(&types.CertificateHeader{ToBlock: 5}, nil).Once()
				mockL2BridgeQuerier.On("GetLastProcessedBlock", ctx).Return(uint64(10), nil)
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(6), uint64(10)).Return(
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
				mockProverClient.EXPECT().GenerateAggchainProof(context.Background(), types.NewAggchainProofRequest(uint64(5), uint64(10),
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
					})).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{Proof: []byte("some-proof")}, LastProvenBlock: 6, EndBlock: 8}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             8,
				RetryCount:          0,
				L1InfoTreeLeafCount: 11,
				LastSentCertificate: &types.CertificateHeader{
					ToBlock: 5,
				},
				Bridges: []bridgesync.Bridge{{BlockNum: 6}},
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
				CreatedAt:       uint32(time.Now().UTC().Unix()),
				CertificateType: types.CertificateTypeFEP,
			},
		},
	}

	for _, tca := range testCases {
		tc := tca
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofClient := mocks.NewAggchainProofClientInterface(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockGERQuerier := mocks.NewGERQuerier(t)
			mockOptimistic := mocks.NewOptimisticModeQuerier(t)
			mockL1InfoTreeDataQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			mockLERQuerier := mocks.NewLERQuerier(t)
			mockSigner := mocks.NewSigner(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams")
			flowBase := NewBaseFlow(
				logger,
				mockL2BridgeQuerier,
				mockStorage,
				mockL1InfoTreeDataQuerier,
				mockLERQuerier,
				NewBaseFlowConfigDefault())

			aggchainFlow := NewAggchainProverFlow(
				logger,
				flowBase,
				NewAggchainProverFlowConfigDefault(),
				mockAggchainProofClient,
				mockStorage,
				mockL1InfoTreeDataQuerier,
				mockL2BridgeQuerier,
				mockGERQuerier,
				nil,
				mockSigner,
				mockOptimistic,
				nil,
			)
			mockOptimistic.EXPECT().IsOptimisticModeOn().Return(false, nil).Maybe()
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
							Metadata:           crypto.Keccak256([]byte("metadata")),
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
							Metadata:           crypto.Keccak256([]byte("metadata")),
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
		name                string
		fromBlock           uint64
		startL2Block        uint64
		expectedResult      uint64
		lastSentCertificate *types.CertificateHeader
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
		{
			name:         "lastSentCertificate settled on PP",
			fromBlock:    10,
			startL2Block: 50,
			lastSentCertificate: &types.CertificateHeader{
				FromBlock: 10,
				ToBlock:   20,
				Status:    agglayertypes.Settled,
			},
			expectedResult: 50,
		},
		{
			name:         "lastSentCertificate settled on PP on the fence",
			fromBlock:    10,
			startL2Block: 50,
			lastSentCertificate: &types.CertificateHeader{
				FromBlock: 10,
				ToBlock:   50,
				Status:    agglayertypes.Settled,
			},
			expectedResult: 50,
		},
		{
			name:                "lastSentCertificate settled on PP on the fence. Case 2",
			fromBlock:           50,
			startL2Block:        50,
			lastSentCertificate: nil,
			expectedResult:      50,
		},
		{
			name:                "lastSentCertificate settled on PP on the fence. Case 3",
			fromBlock:           51,
			startL2Block:        50,
			lastSentCertificate: nil,
			expectedResult:      50,
		},
		{
			name:                "lastSentCertificate settled on PP on the fence. Case 4",
			fromBlock:           52,
			startL2Block:        50,
			lastSentCertificate: nil,
			expectedResult:      51,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams")

			flowBase := NewBaseFlow(
				logger,
				nil, // l2BridgeQuerier
				nil, // sotrage
				nil, // l1InfoTreeDataQuerier,
				nil, // lerQuerier
				NewBaseFlowConfig(0, tc.startL2Block),
			)
			flow := NewAggchainProverFlow(
				logger,
				flowBase,
				NewAggchainProverFlowConfigDefault(),
				nil, // mockAggchainProofClient
				nil, // mockStorage
				nil, // mockL1InfoTreeDataQuerier
				nil, // mockL2BridgeQuerier
				nil, // mockGERQuerier
				nil, // mockOptimistic
				nil, // mockSigner
				nil, // optimisticModeQuerier
				nil, // optimisticSigner
			)

			result := flow.getLastProvenBlock(tc.fromBlock, tc.lastSentCertificate)
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
		mockFn         func(*mocks.BridgeQuerier, *mocks.LERQuerier, *mocks.Signer)
		buildParams    *types.CertificateBuildParams
		expectedError  string
		expectedResult *agglayertypes.Certificate
	}{
		{
			name: "error building certificate",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier, mockLERQuerier *mocks.LERQuerier, mockSigner *mocks.Signer) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(emptyLER, nil)
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
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier, mockLERQuerier *mocks.LERQuerier, mockSigner *mocks.Signer) {
				mockL2BridgeQuerier.EXPECT().OriginNetwork().Return(uint32(1))
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123"))
				mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil)
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(emptyLER, nil)
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{},
				Claims:                         []bridgesync.Claim{},
				CreatedAt:                      uint32(createdAt.Unix()),
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				CertificateType:                types.CertificateTypeFEP,
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
				NewLocalExitRoot:    emptyLER,
				CustomChainData:     []byte("some-data"),
				Metadata:            types.NewCertificateMetadata(1, 9, uint32(createdAt.Unix()), types.CertificateTypeFEP.ToInt()).ToHash(),
				BridgeExits:         []*agglayertypes.BridgeExit{},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
				PrevLocalExitRoot:   emptyLER,
				L1InfoTreeLeafCount: 0,
				AggchainData: &agglayertypes.AggchainDataProof{
					Proof:          []byte("some-proof"),
					Version:        "0.1",
					Vkey:           []byte("some-vkey"),
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
					Signature: []byte("signature"),
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_BuildCertificate")
			mockSigner := mocks.NewSigner(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockLERQuerier := mocks.NewLERQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeQuerier, mockLERQuerier, mockSigner)
			}
			flowBase := NewBaseFlow(
				logger,
				mockL2BridgeQuerier,
				nil, // mockStorage
				nil, // mockL1InfoTreeDataQuerier
				mockLERQuerier,
				NewBaseFlowConfigDefault(),
			)
			aggchainFlow := NewAggchainProverFlow(
				logger,
				flowBase,
				NewAggchainProverFlowConfigDefault(),
				nil, // mockAggchainProofClient
				nil, // mockStorage
				nil, // mockL1InfoTreeDataQuerier
				mockL2BridgeQuerier,
				nil, // mockGERQuerier
				nil, // mockOptimistic
				mockSigner,
				nil, // optimisticModeQuerier
				nil, // optimisticSigner
			)

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
	logger := log.WithFields("flowManager", "Test_AggchainProverFlow_CheckInitialStatus")
	flowBase := NewBaseFlow(
		logger,
		nil, // l2BridgeQuerier
		nil, // sotrage
		nil, // l1InfoTreeDataQuerier
		nil, // lerQuerier
		NewBaseFlowConfig(0, 1234),
	)
	sut := NewAggchainProverFlow(
		logger,
		flowBase,
		NewAggchainProverFlowConfigDefault(),
		nil, // mockAggchainProofClient
		mockStorage,
		nil, // mockL1InfoTreeDataQuerier
		nil, // mockL2BridgeQuerier
		nil, // mockGERQuerier
		nil, // mockOptimistic
		nil, // mockSigner
		nil, // optimisticModeQuerier
		nil, // optimisticSigner
	)

	exampleError := fmt.Errorf("some error")
	testCases := []struct {
		name                        string
		cert                        *types.CertificateHeader
		requireNoFEPBlockGap        bool
		getLastSentCertificateError error
		expectedError               bool
	}{
		{
			name:                        "error getting last sent certificate",
			cert:                        nil,
			requireNoFEPBlockGap:        true,
			getLastSentCertificateError: exampleError,
			expectedError:               true,
		},
		{
			name:                 "no last sent certificate on storage",
			cert:                 nil,
			requireNoFEPBlockGap: true,
			expectedError:        false,
		},
		{
			name:          "last cert after upgrade L2 block (startL2Block) that is OK",
			cert:          &types.CertificateHeader{ToBlock: 4000},
			expectedError: false,
		},
		{
			name:          "last cert is immediately before upgrade L2 block (startL2Block) that is OK",
			cert:          &types.CertificateHeader{ToBlock: 1233},
			expectedError: false,
		},
		{
			name: "last cert after upgrade L2 block (startL2Block) that is OK",
			cert: &types.CertificateHeader{
				ToBlock: 4000,
			},
			requireNoFEPBlockGap: true,
			expectedError:        false,
		},
		{
			name: "last cert is immediately before upgrade L2 block (startL2Block) that is OK",
			cert: &types.CertificateHeader{
				ToBlock: 1233,
			},
			requireNoFEPBlockGap: true,
			expectedError:        false,
		},
		{
			name: "last cert is 2 block below upgrade L2 block (startL2Block) so it's a gap of block 1233. Error",
			cert: &types.CertificateHeader{
				ToBlock: 1232,
			},
			requireNoFEPBlockGap: true,
			expectedError:        true,
		},
		{
			name: "there are a gap, but bypass error because requireNoFEPBlockGap is false",
			cert: &types.CertificateHeader{
				ToBlock: 1232,
			},
			requireNoFEPBlockGap: false,
			expectedError:        false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockStorage.EXPECT().GetLastSentCertificateHeader().Return(tc.cert, tc.getLastSentCertificateError).Once()
			sut.requireNoFEPBlockGap = tc.requireNoFEPBlockGap
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
		mockFn        func(mockEthClient *aggkittypesmocks.BaseEthereumClienter)
		expectedBlock uint64
		expectedError string
	}{
		{
			name: "error creating sovereign rollup caller",
			mockFn: func(mockEthClient *aggkittypesmocks.BaseEthereumClienter) {
				mockEthClient.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow",
		},
		{
			name: "ok fetching starting block number",
			mockFn: func(mockEthClient *aggkittypesmocks.BaseEthereumClienter) {
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

			mockEthClient := aggkittypesmocks.NewBaseEthereumClienter(t)

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
