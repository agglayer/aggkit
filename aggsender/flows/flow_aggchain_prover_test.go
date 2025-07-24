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
	"github.com/agglayer/aggkit/aggsender/certificatebuild"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/query"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_AggchainProverFlow_GetCertificateBuildParams(t *testing.T) {
	t.Parallel()

	finalizedL1Root := common.HexToHash("0x1")
	ctx := t.Context()

	testCases := []struct {
		name   string
		mockFn func(*mocks.AggSenderStorage,
			*mocks.BridgeQuerier,
			*mocks.AggchainProofQuerier,
			*mocks.CommonCertParamsVerifier,
			*mocks.CommonCertParamsBuilder,
		)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, errors.New("some error"))
			},
			expectedError: "some error",
		},
		{
			name: "resend InError certificate - have aggchain proof in db",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
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
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				rer := common.HexToHash("0x1")
				mer := common.HexToHash("0x2")
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
						MainnetExitRoot: mer,
						RollupExitRoot:  rer,
					}}, nil)
				mockParamsVerifier.EXPECT().VerifyBuildParams(ctx, mock.Anything).Return(nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 0,
						EndBlock:        10,
					}, &treetypes.Root{Hash: common.HexToHash("0x1"), Index: 10}, nil)
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
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockParamsBuilder.EXPECT().GetCommonCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock:       1,
					ToBlock:         10,
					CertificateType: types.CertificateTypeFEP,
				}, nil).Once()
				mockParamsVerifier.EXPECT().VerifyBuildParams(ctx, mock.Anything).Return(nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(10), mock.Anything).
					Return(nil, nil, errors.New("some error"))
			},
			expectedError: "aggchainProverFlow - error generating aggchain proof: some error",
		},
		{
			name: "error fetching aggchain proof for new certificate - no proofs built yet",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockParamsBuilder.EXPECT().GetCommonCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock:       1,
					ToBlock:         10,
					CertificateType: types.CertificateTypeFEP,
				}, nil).Once()
				mockParamsVerifier.EXPECT().VerifyBuildParams(ctx, mock.Anything).Return(nil).Once()
				wrappedErr := fmt.Errorf("wrapped error: %w", query.ErrNoProofBuiltYet)
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(10), mock.Anything).
					Return(nil, nil, wrappedErr)
			},
			expectedError:  "",
			expectedParams: nil, // expecting no params to be returned since no proof was built
		},
		{
			name: "success fetching aggchain proof for new certificate",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockParamsBuilder.EXPECT().GetCommonCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock:       6,
					ToBlock:         10,
					CertificateType: types.CertificateTypeFEP,
					Bridges:         []bridgesync.Bridge{},
					Claims:          []bridgesync.Claim{},
					LastSentCertificate: &types.CertificateHeader{
						ToBlock: 5,
						Status:  agglayertypes.Settled,
					},
				}, nil).Once()
				mockParamsVerifier.EXPECT().VerifyBuildParams(ctx, mock.Anything).Return(nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(5), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 6,
						EndBlock:        10,
					}, &treetypes.Root{Hash: common.HexToHash("0x1"), Index: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:  6,
				ToBlock:    10,
				RetryCount: 0,
				LastSentCertificate: &types.CertificateHeader{
					ToBlock: 5,
					Status:  agglayertypes.Settled,
				},
				Bridges:                        []bridgesync.Bridge{},
				Claims:                         []bridgesync.Claim{},
				L1InfoTreeLeafCount:            11,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        10,
				},
				CertificateType: types.CertificateTypeFEP,
			},
		},
		{
			name: "success fetching aggchain proof for new certificate - aggchain prover returns smaller range",
			mockFn: func(mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockParamsVerifier *mocks.CommonCertParamsVerifier,
				mockParamsBuilder *mocks.CommonCertParamsBuilder) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{ToBlock: 5, Status: agglayertypes.Settled}, nil, nil).Once()
				mockParamsBuilder.EXPECT().GetCommonCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock:       6,
					ToBlock:         10,
					CertificateType: types.CertificateTypeFEP,
					Bridges:         []bridgesync.Bridge{{BlockNum: 6}, {BlockNum: 10}},
					Claims: []bridgesync.Claim{{
						BlockNum:        8,
						GlobalIndex:     big.NewInt(1),
						RollupExitRoot:  common.HexToHash("0x1"),
						MainnetExitRoot: common.HexToHash("0x2"),
					}},
					LastSentCertificate: &types.CertificateHeader{
						ToBlock: 5,
						Status:  agglayertypes.Settled,
					},
				}, nil).Once()
				mockParamsVerifier.EXPECT().VerifyBuildParams(ctx, mock.Anything).Return(nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(5), uint64(10), mock.Anything).
					Return(&types.AggchainProof{
						SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
						LastProvenBlock: 6,
						EndBlock:        8,
					}, &treetypes.Root{Hash: common.HexToHash("0x1"), Index: 10}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:           6,
				ToBlock:             8,
				RetryCount:          0,
				L1InfoTreeLeafCount: 11,
				LastSentCertificate: &types.CertificateHeader{
					ToBlock: 5,
					Status:  agglayertypes.Settled,
				},
				Bridges: []bridgesync.Bridge{{BlockNum: 6}},
				Claims: []bridgesync.Claim{{
					BlockNum:        8,
					GlobalIndex:     big.NewInt(1),
					RollupExitRoot:  common.HexToHash("0x1"),
					MainnetExitRoot: common.HexToHash("0x2"),
				}},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				AggchainProof: &types.AggchainProof{
					SP1StarkProof:   &types.SP1StarkProof{Proof: []byte("some-proof")},
					LastProvenBlock: 6,
					EndBlock:        8,
				},
				CertificateType: types.CertificateTypeFEP,
			},
		},
	}

	for _, tca := range testCases {
		tc := tca
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofQuerier := mocks.NewAggchainProofQuerier(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockOptimistic := mocks.NewOptimisticModeQuerier(t)
			mockSigner := mocks.NewSigner(t)
			mockParamsBuilder := mocks.NewCommonCertParamsBuilder(t)
			mockParamsVerifier := mocks.NewCommonCertParamsVerifier(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams")
			aggchainFlow := NewAggchainProverFlow(
				logger,
				NewAggchainProverFlowConfigDefault(),
				mockParamsBuilder,
				mockParamsVerifier,
				mockStorage,
				mockL2BridgeQuerier,
				nil, // l1Client
				mockSigner,
				mockOptimistic,
				mockAggchainProofQuerier,
			)
			mockOptimistic.EXPECT().IsOptimisticModeOn().Return(false, nil).Maybe()
			tc.mockFn(mockStorage, mockL2BridgeQuerier, mockAggchainProofQuerier, mockParamsVerifier, mockParamsBuilder)

			params, err := aggchainFlow.GetCertificateBuildParams(t.Context())
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, params)
			}

			mockStorage.AssertExpectations(t)
			mockL2BridgeQuerier.AssertExpectations(t)
			mockAggchainProofQuerier.AssertExpectations(t)
			mockParamsBuilder.AssertExpectations(t)
			mockParamsVerifier.AssertExpectations(t)
			mockSigner.AssertExpectations(t)
			mockOptimistic.AssertExpectations(t)
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

			flow := NewAggchainProverFlow(
				logger,
				NewAggchainProverFlowConfig(0, tc.startL2Block),
				nil, // mockParamsBuilder
				nil, // mockParamsVerifier
				nil, // mockStorage
				nil, // mockL2BridgeQuerier
				nil, // mockOptimistic
				nil, // mockSigner
				nil, // optimisticModeQuerier
				nil, // aggchainProofQuerier
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
		mockFn         func(*mocks.CommonCertParamsBuilder, *mocks.Signer)
		buildParams    *types.CertificateBuildParams
		expectedError  string
		expectedResult *agglayertypes.Certificate
	}{
		{
			name: "error building certificate",
			mockFn: func(mockParamsBuilder *mocks.CommonCertParamsBuilder, mockSigner *mocks.Signer) {
				mockParamsBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, mock.Anything, true).Return(nil, errors.New("build error")).Once()
			},
			buildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				Bridges:                        []bridgesync.Bridge{{}},
				Claims:                         []bridgesync.Claim{},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
			},
			expectedError: "aggchainProverFlow - error building certificate",
		},
		{
			name: "success building certificate",
			mockFn: func(mockParamsBuilder *mocks.CommonCertParamsBuilder, mockSigner *mocks.Signer) {
				mockParamsBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, mock.Anything, true).Return(&agglayertypes.Certificate{
					NetworkID:           1,
					Height:              0,
					NewLocalExitRoot:    certificatebuild.EmptyLER,
					Metadata:            types.NewCertificateMetadata(1, 9, uint32(createdAt.Unix()), types.CertificateTypeFEP.ToInt()).ToHash(),
					BridgeExits:         []*agglayertypes.BridgeExit{},
					ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
					PrevLocalExitRoot:   certificatebuild.EmptyLER,
					L1InfoTreeLeafCount: 0,
				}, nil).Once()
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123"))
				mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil)
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
				NewLocalExitRoot:    certificatebuild.EmptyLER,
				CustomChainData:     []byte("some-data"),
				Metadata:            types.NewCertificateMetadata(1, 9, uint32(createdAt.Unix()), types.CertificateTypeFEP.ToInt()).ToHash(),
				BridgeExits:         []*agglayertypes.BridgeExit{},
				ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
				PrevLocalExitRoot:   certificatebuild.EmptyLER,
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
			mockParamsBuilder := mocks.NewCommonCertParamsBuilder(t)
			if tc.mockFn != nil {
				tc.mockFn(mockParamsBuilder, mockSigner)
			}

			aggchainFlow := NewAggchainProverFlow(
				logger,
				NewAggchainProverFlowConfigDefault(),
				mockParamsBuilder,
				nil, // mockParamsVerifier
				nil, // mockStorage
				nil, // mockL2BridgeQuerier
				nil, // mockOptimistic
				mockSigner,
				nil, // optimisticModeQuerier
				nil, // aggchainProofQuerier
			)

			certificate, err := aggchainFlow.BuildCertificate(ctx, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.NotNil(t, certificate)
				require.Equal(t, tc.expectedResult, certificate)
			}

			mockSigner.AssertExpectations(t)
			mockParamsBuilder.AssertExpectations(t)
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

func Test_AggchainProverFlow_CheckInitialStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name                 string
		requireNoFEPBlockGap bool
		startL2Block         uint64
		mockFn               func(
			mockStorage *mocks.AggSenderStorage,
			mockBaseFlow *mocks.CommonCertParamsVerifier,
			mockL2BridgeSyncer *mocks.BridgeQuerier,
		)
		expectedError string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.CommonCertParamsVerifier,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, errors.New("db error")).Once()
			},
			expectedError: "aggchainProverFlow - error getting last sent certificate: db error",
		},
		{
			name:         "error waiting for syncer to catch up",
			startL2Block: 15,
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.CommonCertParamsVerifier,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				lastCert := &types.CertificateHeader{ToBlock: 10}
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(lastCert, nil).Once()
				mockL2BridgeSyncer.EXPECT().WaitForSyncerToCatchUp(ctx, uint64(15)).Return(errors.New("sync error")).Once()
			},
			expectedError: "aggchainProverFlow - error waiting for syncer to catch up: sync error",
		},
		{
			name:         "error verifying block range gaps - has bridge transactions in gap",
			startL2Block: 15,
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.CommonCertParamsVerifier,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				lastCert := &types.CertificateHeader{ToBlock: 10}
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(lastCert, nil).Once()
				mockL2BridgeSyncer.EXPECT().WaitForSyncerToCatchUp(ctx, uint64(15)).Return(nil).Once()
				mockBaseFlow.EXPECT().VerifyBlockRangeGaps(ctx, lastCert, uint64(15), uint64(15)).
					Return(errors.New("gap error")).Once()
			},
			expectedError: "aggchainProverFlow - error verifying block range gaps on startup",
		},
		{
			name:                 "success ",
			startL2Block:         11,
			requireNoFEPBlockGap: true,
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockBaseFlow *mocks.CommonCertParamsVerifier,
				mockL2BridgeSyncer *mocks.BridgeQuerier,
			) {
				lastCert := &types.CertificateHeader{ToBlock: 10}
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(lastCert, nil).Once()
				mockL2BridgeSyncer.EXPECT().WaitForSyncerToCatchUp(ctx, uint64(11)).Return(nil).Once()
				mockBaseFlow.EXPECT().VerifyBlockRangeGaps(ctx, lastCert, uint64(11), uint64(11)).
					Return(nil).Once()
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mockStorage := mocks.NewAggSenderStorage(t)
			mockParamsVerifier := mocks.NewCommonCertParamsVerifier(t)
			mockL2BridgeSyncer := mocks.NewBridgeQuerier(t)
			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_CheckInitialStatus")

			flow := &AggchainProverFlow{
				log:                  logger,
				storage:              mockStorage,
				commonParamsVerifier: mockParamsVerifier,
				l2BridgeQuerier:      mockL2BridgeSyncer,
				cfg:                  NewAggchainProverFlowConfig(0, tc.startL2Block),
			}

			tc.mockFn(mockStorage, mockParamsVerifier, mockL2BridgeSyncer)

			err := flow.CheckInitialStatus(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}

			mockStorage.AssertExpectations(t)
			mockParamsVerifier.AssertExpectations(t)
			mockL2BridgeSyncer.AssertExpectations(t)
		})
	}
}
