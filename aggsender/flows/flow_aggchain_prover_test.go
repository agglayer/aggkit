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

	ctx := context.Background()

	testCases := []struct {
		name         string
		startL2Block uint64
		mockFn       func(*mocks.AggSenderStorage,
			*mocks.BridgeQuerier,
			*mocks.CertificateBuilder,
			*mocks.CertificateBuildVerifier,
			*mocks.AggchainProofQuerier,
			*mocks.OptimisticModeQuerier)
		expectedBuildParams *types.CertificateBuildParams
		expectedError       string
	}{
		{
			name: "error getting last sent certificate",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow - error getting last sent certificate from storage: some error",
		},
		{
			name: "error getting cert type",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow - error getting certificate type to generate: getCertificateTypeToGenerate - error getting optimistic mode: some error",
		},
		{
			name: "resend InError certificate - no proof in db",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x1234"),
					FromBlock:        1,
					ToBlock:          100,
					Status:           agglayertypes.InError,
					CertType:         types.CertificateTypeFEP,
				}, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(100)).Return(nil, nil, nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(100), mock.Anything).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof: []byte("some-proof"),
					},
					AggchainParams: common.HexToHash("0x2"),
					EndBlock:       100,
				}, &treetypes.Root{Hash: common.HexToHash("0x3"), Index: 10}, nil).Once()
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        100,
				RetryCount:                     1,
				CertificateType:                types.CertificateTypeFEP,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x3"),
				L1InfoTreeLeafCount:            11,
				LastSentCertificate: &types.CertificateHeader{
					Height:           0,
					NewLocalExitRoot: common.HexToHash("0x1234"),
					FromBlock:        1,
					ToBlock:          100,
					Status:           agglayertypes.InError,
					CertType:         types.CertificateTypeFEP,
				},
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof: []byte("some-proof"),
					},
					EndBlock:       100,
					AggchainParams: common.HexToHash("0x2"),
				},
			},
		},
		{
			name: "resend InError certificate - has proof in db",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					Height:              0,
					NewLocalExitRoot:    common.HexToHash("0x1234"),
					FromBlock:           1,
					ToBlock:             100,
					Status:              agglayertypes.InError,
					CertType:            types.CertificateTypeFEP,
					L1InfoTreeLeafCount: 11,
				}, &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof: []byte("some-proof"),
					},
					AggchainParams: common.HexToHash("0x2"),
					EndBlock:       100,
				}, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockL2BridgeQuerier.EXPECT().GetBridgesAndClaims(ctx, uint64(1), uint64(100)).Return(nil, nil, nil).Once()
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             100,
				RetryCount:          1,
				CertificateType:     types.CertificateTypeFEP,
				L1InfoTreeLeafCount: 11,
				LastSentCertificate: &types.CertificateHeader{
					Height:              0,
					NewLocalExitRoot:    common.HexToHash("0x1234"),
					FromBlock:           1,
					ToBlock:             100,
					Status:              agglayertypes.InError,
					CertType:            types.CertificateTypeFEP,
					L1InfoTreeLeafCount: 11,
				},
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof: []byte("some-proof"),
					},
					EndBlock:       100,
					AggchainParams: common.HexToHash("0x2"),
				},
			},
		},
		{
			name: "new certificate - no new blocks to prove",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(nil, certificatebuild.ErrNoNewBlocks).Once()
			},
		},
		{
			name: "new certificate - error building certificate",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(nil, errors.New("some error")).Once()
			},
			expectedError: "some error",
		},
		{
			name: "error verifying certificate build params",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock: 1,
					ToBlock:   100,
				}, nil).Once()
				mockCertVerifier.EXPECT().VerifyBuildParams(mock.Anything).Return(errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow - error verifying build params: some error",
		},
		{
			name:         "error verifying block gap",
			startL2Block: 100,
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(&types.CertificateHeader{
					FromBlock: 1,
					ToBlock:   50,
					Status:    agglayertypes.Settled,
					CertType:  types.CertificateTypePP,
				}, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock: 100,
					ToBlock:   200,
					LastSentCertificate: &types.CertificateHeader{
						FromBlock: 1,
						ToBlock:   50,
						Status:    agglayertypes.Settled,
						CertType:  types.CertificateTypePP,
					},
				}, nil).Once()
				mockCertVerifier.EXPECT().VerifyBuildParams(mock.Anything).Return(nil).Once()
			},
			expectedError: "aggchainProverFlow - error checking for block gaps",
		},
		{
			name: "success - new certificate",
			mockFn: func(
				mockStorage *mocks.AggSenderStorage,
				mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockCertBuilder *mocks.CertificateBuilder,
				mockCertVerifier *mocks.CertificateBuildVerifier,
				mockAggchainProofQuerier *mocks.AggchainProofQuerier,
				mockOptimisticQuerier *mocks.OptimisticModeQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeaderWithProofIfInError(ctx).Return(nil, nil, nil).Once()
				mockOptimisticQuerier.EXPECT().IsOptimisticModeOn().Return(false, nil).Once()
				mockCertBuilder.EXPECT().GetCertificateBuildParams(ctx, types.CertificateTypeFEP).Return(&types.CertificateBuildParams{
					FromBlock:       1,
					ToBlock:         200,
					CertificateType: types.CertificateTypeFEP,
				}, nil).Once()
				mockCertVerifier.EXPECT().VerifyBuildParams(mock.Anything).Return(nil).Once()
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(0), uint64(200), mock.Anything).Return(&types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					AggchainParams:  common.HexToHash("0x2"),
					CustomChainData: []byte("some-data"),
					EndBlock:        200,
				}, &treetypes.Root{Hash: common.HexToHash("0x3"), Index: 10}, nil).Once()
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        200,
				RetryCount:                     0,
				CertificateType:                types.CertificateTypeFEP,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x3"),
				L1InfoTreeLeafCount:            11,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					AggchainParams:  common.HexToHash("0x2"),
					CustomChainData: []byte("some-data"),
					EndBlock:        200,
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			logger := log.WithFields("flowManager", "Test_AggchainProverFlow_GetCertificateBuildParams")
			mockStorage := mocks.NewAggSenderStorage(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockCertBuilder := mocks.NewCertificateBuilder(t)
			mockCertVerifier := mocks.NewCertificateBuildVerifier(t)
			mockOptimisticQuerier := mocks.NewOptimisticModeQuerier(t)
			mockAggchainProofQuerier := mocks.NewAggchainProofQuerier(t)

			if tc.mockFn != nil {
				tc.mockFn(mockStorage, mockL2BridgeQuerier, mockCertBuilder, mockCertVerifier, mockAggchainProofQuerier, mockOptimisticQuerier)
			}

			flow := NewAggchainProverFlow(
				logger,
				NewAggchainProverFlowConfig(tc.startL2Block > 0, tc.startL2Block),
				mockStorage,
				mockL2BridgeQuerier,
				nil,                   // mockSigner
				mockOptimisticQuerier, // optimisticModeQuerier
				mockCertBuilder,
				mockCertVerifier,
				mockAggchainProofQuerier,
			)

			buildParams, err := flow.GetCertificateBuildParams(ctx)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBuildParams, buildParams)
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

			flow := NewAggchainProverFlow(
				logger,
				NewAggchainProverFlowConfig(false, tc.startL2Block),
				nil, // mockStorage
				nil, // mockL2BridgeQuerier
				nil, // mockSigner
				nil, // optimisticModeQuerier
				nil, // mockCertificateBuilder
				nil, // mockCertificateBuildVerifier
				nil, // mockAgghcainProofQuerier
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
		mockFn         func(*mocks.CertificateBuilder, *mocks.Signer)
		buildParams    *types.CertificateBuildParams
		expectedError  string
		expectedResult *agglayertypes.Certificate
	}{
		{
			name: "error building certificate",
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder, mockSigner *mocks.Signer) {
				mockCertBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, mock.Anything, true).Return(nil, errors.New("some error"))
			},
			buildParams:   &types.CertificateBuildParams{},
			expectedError: "aggchainProverFlow - error building certificate",
		},
		{
			name: "success building certificate",
			buildParams: &types.CertificateBuildParams{
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
					Signature:       []byte("signature"),
					CustomChainData: []byte("some-data"),
				},
			},
			mockFn: func(mockCertBuilder *mocks.CertificateBuilder, mockSigner *mocks.Signer) {
				mockCertBuilder.EXPECT().BuildCertificate(ctx, mock.Anything, mock.Anything, true).Return(&agglayertypes.Certificate{
					NetworkID:           1,
					Height:              0,
					NewLocalExitRoot:    certificatebuild.EmptyLER,
					Metadata:            types.NewCertificateMetadata(1, 9, uint32(createdAt.Unix()), types.CertificateTypeFEP.ToInt()).ToHash(),
					BridgeExits:         []*agglayertypes.BridgeExit{},
					ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
					PrevLocalExitRoot:   certificatebuild.EmptyLER,
					L1InfoTreeLeafCount: 0,
				}, nil)
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x123"))
				mockSigner.EXPECT().SignHash(mock.Anything, mock.Anything).Return([]byte("signature"), nil)
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
			mockCertBuilder := mocks.NewCertificateBuilder(t)
			if tc.mockFn != nil {
				tc.mockFn(mockCertBuilder, mockSigner)
			}

			aggchainFlow := NewAggchainProverFlow(
				logger,
				NewAggchainProverFlowConfigDefault(),
				nil, // mockStorage
				nil, // mockL2BridgeQuerier
				mockSigner,
				nil, // optimisticModeQuerier
				mockCertBuilder,
				nil, // mockCertificateBuildVerifier
				nil, // mockAggchainProofQuerier
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
	sut := NewAggchainProverFlow(
		logger,
		NewAggchainProverFlowConfig(false, 1234),
		mockStorage,
		nil, // mockL2BridgeQuerier
		nil, // mockSigner
		nil, // optimisticModeQuerier
		nil, // mockCertificateBuilder
		nil, // mockCertificateBuildVerifier
		nil, // mockAggchainProofQuerier
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
			sut.cfg.requireNoFEPBlockGap = tc.requireNoFEPBlockGap
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

//nolint:duplicate
func Test_AggchainProverFlow_SignCertificate(t *testing.T) {
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
				mockSigner.EXPECT().SignHash(ctx, mock.Anything).Return([]byte("mock_cert_signature"), nil)
				mockSigner.EXPECT().PublicAddress().Return(common.HexToAddress("0x1234"))
			},
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x4567"),
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: common.HexToHash("0x2"),
				},
			},
			expectedCert: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x4567"),
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: common.HexToHash("0x2"),
					Signature:      []byte("mock_cert_signature"),
				},
			},
		},
		{
			name: "error signing certificate",
			mockSignerFn: func(mockSigner *mocks.Signer) {
				mockSigner.EXPECT().SignHash(ctx, mock.Anything).Return(nil, errors.New("signing error"))
			},
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x4567"),
				AggchainData: &agglayertypes.AggchainDataProof{
					AggchainParams: common.HexToHash("0x2"),
				},
			},
			expectedError: "signing error",
		},
		{
			name: "error not aggchain proof",
			certificate: &agglayertypes.Certificate{
				NewLocalExitRoot: common.HexToHash("0x4567"),
				AggchainData: &agglayertypes.AggchainDataSignature{
					Signature: []byte("mock_cert_signature"),
				},
			},
			expectedError: "aggchainProverFlow - signCertificate - AggchainData is not of type AggchainDataProof",
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
			logger := log.WithFields("test", "Test_AggchainProverFlow_SignCertificate")

			aggchainProverFlow := &AggchainProverFlow{
				log:               logger,
				certificateSigner: mockSigner,
			}

			signedCert, err := aggchainProverFlow.signCertificate(ctx, tt.certificate)

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

func Test_AggchainProverFlow_AdjustBlockRange(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                  string
		requestedToBlock      uint64
		aggchainProverToBlock uint64
		buildParams           *types.CertificateBuildParams
		expectedBuildParams   *types.CertificateBuildParams
		expectedError         string
	}{
		{
			name:                  "no adjustment needed",
			requestedToBlock:      100,
			aggchainProverToBlock: 100,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 50,
				ToBlock:   100,
				Bridges:   []bridgesync.Bridge{{BlockNum: 50}},
				Claims:    []bridgesync.Claim{{BlockNum: 100}},
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock: 50,
				ToBlock:   100,
				Bridges:   []bridgesync.Bridge{{BlockNum: 50}},
				Claims:    []bridgesync.Claim{{BlockNum: 100}},
			},
			expectedError: "",
		},
		{
			name:                  "adjust to aggchain prover block - prover block is higher than requested block",
			requestedToBlock:      100,
			aggchainProverToBlock: 120,
			buildParams: &types.CertificateBuildParams{
				FromBlock: 50,
				ToBlock:   100,
				Bridges:   []bridgesync.Bridge{{BlockNum: 50}},
				Claims:    []bridgesync.Claim{{BlockNum: 120}},
			},
			expectedError: "invalid range",
		},
		{
			name:                  "adjust to aggchain prover block",
			requestedToBlock:      100,
			aggchainProverToBlock: 90,
			buildParams: &types.CertificateBuildParams{
				FromBlock:  50,
				ToBlock:    100,
				Bridges:    []bridgesync.Bridge{{BlockNum: 50}, {BlockNum: 99}},
				Claims:     []bridgesync.Claim{{BlockNum: 90}, {BlockNum: 100}},
				CreatedAt:  12345,
				RetryCount: 1,
				LastSentCertificate: &types.CertificateHeader{
					FromBlock: 20,
					ToBlock:   49,
					Status:    agglayertypes.Settled,
				},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0xabc"),
				L1InfoTreeLeafCount:            10,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
					Signature:       []byte("signature"),
					CustomChainData: []byte("some-data"),
				},
				CertificateType: types.CertificateTypeFEP,
				ExtraData:       "some-extra-data",
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock:  50,
				ToBlock:    90,
				Bridges:    []bridgesync.Bridge{{BlockNum: 50}},
				Claims:     []bridgesync.Claim{{BlockNum: 90}},
				CreatedAt:  12345,
				RetryCount: 1,
				LastSentCertificate: &types.CertificateHeader{
					FromBlock: 20,
					ToBlock:   49,
					Status:    agglayertypes.Settled,
				},
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0xabc"),
				L1InfoTreeLeafCount:            10,
				AggchainProof: &types.AggchainProof{
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
					AggchainParams: common.HexToHash("0x2"),
					Context: map[string][]byte{
						"key1": []byte("value1"),
					},
					Signature:       []byte("signature"),
					CustomChainData: []byte("some-data"),
				},
				CertificateType: types.CertificateTypeFEP,
				ExtraData:       "some-extra-data",
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			certBuildParams, err := adjustBlockRange(tc.buildParams, tc.requestedToBlock, tc.aggchainProverToBlock)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBuildParams, certBuildParams)
			}
		})
	}
}

func Test_AggchainProverFlow_GenerateProof(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name                   string
		mockFn                 func(*mocks.AggchainProofQuerier)
		certificateBuildParams *types.CertificateBuildParams
		lastProvenBlock        uint64
		expectedError          string
		expectedBuildParams    *types.CertificateBuildParams
	}{
		{
			name:            "error getting aggchain proof",
			lastProvenBlock: 100,
			certificateBuildParams: &types.CertificateBuildParams{
				FromBlock: 101,
				ToBlock:   200,
			},
			mockFn: func(mockAggchainProofQuerier *mocks.AggchainProofQuerier) {
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(100), uint64(200), mock.Anything).Return(nil, nil, errors.New("some error")).Once()
			},
			expectedError: "aggchainProverFlow - error generating aggchain proof: some error",
		},
		{
			name:            "no proof built yet",
			lastProvenBlock: 100,
			certificateBuildParams: &types.CertificateBuildParams{
				FromBlock: 101,
				ToBlock:   200,
			},
			mockFn: func(mockAggchainProofQuerier *mocks.AggchainProofQuerier) {
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(100), uint64(200), mock.Anything).Return(nil, nil, errNoProofBuiltYet).Once()
			},
		},
		{
			name:            "successfully generates proof for requested range",
			lastProvenBlock: 100,
			certificateBuildParams: &types.CertificateBuildParams{
				FromBlock: 101,
				ToBlock:   200,
			},
			mockFn: func(mockAggchainProofQuerier *mocks.AggchainProofQuerier) {
				mockAggchainProofQuerier.EXPECT().GenerateAggchainProof(ctx, uint64(100), uint64(200), mock.Anything).Return(
					&types.AggchainProof{
						LastProvenBlock: 100,
						EndBlock:        200,
						SP1StarkProof: &types.SP1StarkProof{
							Proof:   []byte("some-proof"),
							Version: "0.1",
							Vkey:    []byte("some-vkey"),
						},
					},
					&treetypes.Root{Hash: common.HexToHash("0x1234"), Index: 10},
					nil).Once()
			},
			expectedBuildParams: &types.CertificateBuildParams{
				FromBlock:                      101,
				ToBlock:                        200,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1234"),
				L1InfoTreeLeafCount:            11, // 10 + 1 because we include the last proven block
				AggchainProof: &types.AggchainProof{
					LastProvenBlock: 100,
					EndBlock:        200,
					SP1StarkProof: &types.SP1StarkProof{
						Proof:   []byte("some-proof"),
						Version: "0.1",
						Vkey:    []byte("some-vkey"),
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockAggchainProofQuerier := mocks.NewAggchainProofQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockAggchainProofQuerier)
			}

			logger := log.WithFields("test", "Test_AggchainProverFlow_GenerateProof")
			aggchainProverFlow := &AggchainProverFlow{
				log:                  logger,
				aggchainProofQuerier: mockAggchainProofQuerier,
			}

			buildParams, err := aggchainProverFlow.generateProof(ctx, tc.lastProvenBlock, tc.certificateBuildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedBuildParams, buildParams)
			}
		})
	}
}
