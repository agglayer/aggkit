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
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_baseFlow_limitCertSize(t *testing.T) {
	tests := []struct {
		name          string
		maxCertSize   uint
		fullCert      *types.CertificateBuildParams
		expectedCert  *types.CertificateBuildParams
		expectedError string
	}{
		{
			name:        "certificate size within limit",
			maxCertSize: 1000,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
			},
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
			},
		},
		{
			name:        "certificate size exceeds limit - reducing with some bridges",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 9}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}},
			},
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   9,
				Bridges:   []bridgesync.Bridge{{BlockNum: 9}},
				Claims:    []bridgesync.Claim{},
				Unclaims:  []bridgesynctypes.Unclaim{},
			},
		},
		{
			name:        "certificate size exceeds limit - reducing to no bridges",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}},
			},
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   9,
				Bridges:   []bridgesync.Bridge{},
				Claims:    []bridgesync.Claim{},
				Unclaims:  []bridgesynctypes.Unclaim{},
			},
		},
		{
			name:        "certificate size exceeds limit with minimum blocks",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   2,
				Bridges:   []bridgesync.Bridge{{}},
			},
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   2,
				Bridges:   []bridgesync.Bridge{{}},
			},
		},
		{
			name:        "empty certificate allowed",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
		},
		{
			name:        "maxCertSize is 0 with bridges and claims",
			maxCertSize: 0,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
				Claims:    []bridgesync.Claim{{}, {}},
			},
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
				Claims:    []bridgesync.Claim{{}, {}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := NewBaseFlow(
				log.WithFields("test", t.Name()),
				nil,
				nil,
				nil,
				nil,
				NewBaseFlowConfig(tt.maxCertSize, 0, false, true, true))

			result, err := f.LimitCertSize(tt.fullCert)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedCert, result)
			}
		})
	}
}

func Test_baseFlow_getNewLocalExitRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		certParams      *types.CertificateBuildParams
		mockFn          func(mockL2BridgeQuerier *mocks.BridgeQuerier)
		previousLER     common.Hash
		expectedLER     common.Hash
		expectedError   string
		numberOfBridges int
	}{
		{
			name: "no bridges, return previous LER",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{},
			},
			previousLER: common.HexToHash("0x123"),
			expectedLER: common.HexToHash("0x123"),
		},
		{
			name: "exit root found, return new exit root",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{{}, {}},
				ToBlock: 10,
			},
			previousLER: common.HexToHash("0x123"),
			expectedLER: common.HexToHash("0x456"),
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.HexToHash("0x456"), nil)
			},
		},
		{
			name: "exit root not found, return previous LER",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{{}, {}},
				ToBlock: 10,
			},
			previousLER:   common.HexToHash("0x123"),
			expectedLER:   common.HexToHash("0x123"),
			expectedError: "not found",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.Hash{}, db.ErrNotFound)
			},
		},
		{
			name: "error fetching exit root, return error",
			certParams: &types.CertificateBuildParams{
				Bridges: []bridgesync.Bridge{{}, {}},
				ToBlock: 10,
			},
			previousLER:   common.HexToHash("0x123"),
			expectedLER:   common.Hash{},
			expectedError: "error getting exit root by index: 0. Error: unexpected error",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.Hash{}, errors.New("unexpected error"))
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tt.mockFn != nil {
				tt.mockFn(mockL2BridgeQuerier)
			}

			f := &baseFlow{
				l2BridgeQuerier: mockL2BridgeQuerier,
			}

			result, err := f.getNewLocalExitRoot(context.Background(), tt.certParams, tt.previousLER)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedLER, result)
			}
		})
	}
}
func Test_baseFlow_GetNewLocalExitRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		certParams       *types.CertificateBuildParams
		mockFn           func(mockL2BridgeQuerier *mocks.BridgeQuerier, mockStorage *mocks.AggSenderStorage)
		expectedLER      common.Hash
		expectedError    string
		getNextHeightErr error
		getNewLERMockErr error
	}{
		{
			name:          "certificate parameters are nil",
			certParams:    nil,
			expectedLER:   common.Hash{},
			expectedError: "certificate build parameters cannot be nil",
		},
		{
			name: "error getting next height and previous LER",
			certParams: &types.CertificateBuildParams{
				LastSentCertificate: &types.CertificateHeader{
					Status: agglayertypes.Pending,
				},
			},
			getNextHeightErr: errors.New("mock error"),
			expectedLER:      common.Hash{},
			expectedError:    "error getting next height and previous LER",
		},
		{
			name: "error getting new local exit root",
			certParams: &types.CertificateBuildParams{
				LastSentCertificate: &types.CertificateHeader{
					Status: agglayertypes.Settled,
				},
				Bridges: []bridgesync.Bridge{{}, {}},
			},
			getNewLERMockErr: errors.New("mock error"),
			expectedLER:      common.Hash{},
			expectedError:    "error getting new local exit root",
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier, mockStorage *mocks.AggSenderStorage) {
				mockL2BridgeQuerier.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(common.Hash{}, errors.New("mock error"))
			},
		},
		{
			name: "successfully get new local exit root",
			certParams: &types.CertificateBuildParams{
				LastSentCertificate: &types.CertificateHeader{
					Status: agglayertypes.Settled,
				},
			},
			expectedLER: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000000"),
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockStorage := mocks.NewAggSenderStorage(t)

			if tt.mockFn != nil {
				tt.mockFn(mockL2BridgeQuerier, mockStorage)
			}

			f := &baseFlow{
				l2BridgeQuerier: mockL2BridgeQuerier,
				storage:         mockStorage,
			}
			ctx := context.TODO()
			result, err := f.GetNewLocalExitRoot(ctx, tt.certParams)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedLER, result)
			}
		})
	}
}

func Test_baseFlow_getNextHeightAndPreviousLER(t *testing.T) {
	t.Parallel()

	previousLER := common.HexToHash("0x123")

	testCases := []struct {
		name           string
		lastSentCert   *types.CertificateHeader
		expectedHeight uint64
		expectedLER    common.Hash
		expectedError  string
		mockFn         func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage)
	}{
		{
			name:           "no last sent certificate - zero start LER",
			lastSentCert:   nil,
			expectedHeight: 0,
			expectedLER:    types.EmptyLER,
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
			},
		},
		{
			name:           "no last sent certificate - has start LER",
			lastSentCert:   nil,
			expectedHeight: 0,
			expectedLER:    common.HexToHash("0x1"),
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(common.HexToHash("0x1"), nil)
			},
		},
		{
			name:           "ler querier returns error",
			lastSentCert:   nil,
			expectedHeight: 0,
			expectedLER:    aggkitcommon.ZeroHash,
			expectedError:  "some error",
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(common.Hash{}, errors.New("some error"))
			},
		},
		{
			name: "last sent certificate is not Closed",
			lastSentCert: &types.CertificateHeader{
				Status: agglayertypes.Pending,
			},
			expectedHeight: 0,
			expectedLER:    common.Hash{},
			expectedError:  "is not closed",
		},
		{
			name: "last sent certificate is Settled",
			lastSentCert: &types.CertificateHeader{
				Status:           agglayertypes.Settled,
				Height:           2,
				NewLocalExitRoot: common.HexToHash("0x123"),
			},
			expectedHeight: 3,
			expectedLER:    common.HexToHash("0x123"),
		},
		{
			name: "last sent certificate is InError, has previous LER",
			lastSentCert: &types.CertificateHeader{
				Status:                agglayertypes.InError,
				Height:                5,
				PreviousLocalExitRoot: &previousLER,
				NewLocalExitRoot:      common.HexToHash("0x789"),
			},
			expectedHeight: 5,
			expectedLER:    previousLER,
		},
		{
			name: "first certificate InError",
			lastSentCert: &types.CertificateHeader{
				Status:                agglayertypes.InError,
				Height:                0,
				PreviousLocalExitRoot: nil,
				NewLocalExitRoot:      common.HexToHash("0x789"),
			},
			expectedHeight: 0,
			expectedLER:    types.EmptyLER,
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(types.EmptyLER, nil)
			},
		},
		{
			name: "error getting previously sent certificate",
			lastSentCert: &types.CertificateHeader{
				Status:           agglayertypes.InError,
				Height:           5,
				NewLocalExitRoot: common.HexToHash("0x789"),
			},
			expectedHeight: 0,
			expectedLER:    aggkitcommon.ZeroHash,
			expectedError:  "error getting last settled certificate: some error",
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(4)).
					Return(nil, errors.New("some error"))
			},
		},
		{
			name: "previously sent certificate not found",
			lastSentCert: &types.CertificateHeader{
				Status:           agglayertypes.InError,
				Height:           5,
				NewLocalExitRoot: common.HexToHash("0x789"),
			},
			expectedHeight: 0,
			expectedLER:    aggkitcommon.ZeroHash,
			expectedError:  "none settled certificate",
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(4)).
					Return(nil, nil)
			},
		},
		{
			name: "previously sent certificate is not Settled",
			lastSentCert: &types.CertificateHeader{
				Status:           agglayertypes.InError,
				Height:           5,
				NewLocalExitRoot: common.HexToHash("0x789"),
			},
			expectedHeight: 0,
			expectedLER:    aggkitcommon.ZeroHash,
			expectedError:  "is not settled",
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(4)).
					Return(&types.CertificateHeader{Status: agglayertypes.Pending}, nil)
			},
		},
		{
			name: "previously sent certificate is Settled",
			lastSentCert: &types.CertificateHeader{
				Status:           agglayertypes.InError,
				Height:           5,
				NewLocalExitRoot: common.HexToHash("0x789"),
			},
			expectedHeight: 5,
			expectedLER:    common.HexToHash("0x789"),
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockStorage.EXPECT().GetCertificateHeaderByHeight(uint64(4)).
					Return(&types.CertificateHeader{
						Status:           agglayertypes.Settled,
						NewLocalExitRoot: common.HexToHash("0x789"),
					}, nil)
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockLERQuerier := mocks.NewLERQuerier(t)
			mockStorage := mocks.NewAggSenderStorage(t)
			if tc.mockFn != nil {
				tc.mockFn(mockLERQuerier, mockStorage)
			}

			log := log.WithFields("test", t.Name())
			f := &baseFlow{
				lerQuerier: mockLERQuerier,
				storage:    mockStorage,
				log:        log,
			}

			height, ler, err := f.getNextHeightAndPreviousLER(tc.lastSentCert)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedHeight, height)
				require.Equal(t, tc.expectedLER, ler)
			}
		})
	}
}

func Test_baseFlow_VerifyBuildParams(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	testCases := []struct {
		name          string
		buildParams   *types.CertificateBuildParams
		mockFn        func(*mocks.BridgeQuerier)
		expectedError string
	}{
		{
			name: "invalid retry starting block",
			buildParams: &types.CertificateBuildParams{
				FromBlock:  10,
				ToBlock:    15,
				RetryCount: 1,
				LastSentCertificate: &types.CertificateHeader{
					Height:    1,
					Status:    agglayertypes.InError,
					FromBlock: 5,
					ToBlock:   10,
				},
			},
			expectedError: "retry certificate fromBlock 10 != last sent certificate fromBlock 5",
		},
		{
			name: "invalid claim GER",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Claims: []bridgesync.Claim{
					{GlobalExitRoot: common.HexToHash("0x123"), MainnetExitRoot: common.HexToHash("0x456"), RollupExitRoot: common.HexToHash("0x789")},
				},
			},
			expectedError: "GER mismatch",
		},
		{
			name: "success",
			buildParams: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
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

			log := log.WithFields("test", t.Name())
			f := &baseFlow{
				log:             log,
				l2BridgeQuerier: mockL2BridgeQuerier,
			}

			err := f.VerifyBuildParams(ctx, tc.buildParams)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_baseFlow_VerifyBlockRangeGaps(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	type args struct {
		lastSentCertificate *types.CertificateHeader
		newFromBlock        uint64
		newToBlock          uint64
	}
	tests := []struct {
		name            string
		args            args
		requireNoFEPGap bool
		mockFn          func(mockL2BridgeQuerier *mocks.BridgeQuerier)
		expectedError   string
	}{
		{
			name: "lastSentCertificate is nil",
			args: args{
				lastSentCertificate: nil,
				newFromBlock:        10,
				newToBlock:          20,
			},
		},
		{
			name: "no gap between certificates",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   20,
				},
				newFromBlock: 21,
				newToBlock:   30,
			},
		},
		{
			name: "gap exists but no bridges or claims in gap",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				// gap is [16,16]
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16), false).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
		},
		{
			name: "gap exists and bridges in gap returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16), false).
					Return([]bridgesync.Bridge{{}}, []bridgesync.Claim{}, nil)
			},
			expectedError: "there are new bridges or claims in the gap",
		},
		{
			name: "gap exists and claims in gap returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16), false).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{{}}, nil)
			},
			expectedError: "there are new bridges or claims in the gap",
		},
		{
			name: "gap exists, no bridges/claims, RequireNoFEPBlockGap true returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			requireNoFEPGap: true,
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16), false).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
			expectedError: "block gap detected",
		},
		{
			name: "gap exists, GetBridgesAndClaims returns error",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 10,
					ToBlock:   15,
				},
				newFromBlock: 17,
				newToBlock:   20,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(16), uint64(16), false).
					Return(nil, nil, errors.New("db error"))
			},
			expectedError: "error getting bridges and claims in the gap",
		},
		{
			name: "lastSentCertificate is InError, gap logic uses FromBlock-1",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.InError,
					FromBlock: 5,
					ToBlock:   10,
				},
				newFromBlock: 7,
				newToBlock:   10,
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				// lastSettledToBlock = 4, so gap is [5,6]
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(ctx, uint64(5), uint64(6), false).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
			},
		},
		{
			name: "lastSentCertificate is Settled, and SC.StartingBlockNumber is < that last Cert, so nothing to check",
			args: args{
				lastSentCertificate: &types.CertificateHeader{
					Status:    agglayertypes.Settled,
					FromBlock: 5,
					ToBlock:   10,
				},
				newFromBlock: 1,
				newToBlock:   1,
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			if tt.mockFn != nil {
				tt.mockFn(mockL2BridgeQuerier)
			}

			f := &baseFlow{
				l2BridgeQuerier: mockL2BridgeQuerier,
				cfg: BaseFlowConfig{
					RequireNoFEPBlockGap: tt.requireNoFEPGap,
				},
			}

			err := f.VerifyBlockRangeGaps(ctx, tt.args.lastSentCertificate, tt.args.newFromBlock, tt.args.newToBlock)
			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func Test_baseFlow_getImportedBridgeExits(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	rootFromWhichToProve := common.HexToHash("0x1234567890123456789012345678901234567890123456789012345678901234")

	mockProof := func() treetypes.Proof {
		proof := treetypes.Proof{}
		for i := 0; i < int(treetypes.DefaultHeight) && i < 10; i++ {
			proof[i] = common.HexToHash(fmt.Sprintf("0x%02x", i))
		}
		return proof
	}()

	tests := []struct {
		name             string
		claims           []bridgesync.Claim
		unclaims         []bridgesynctypes.Unclaim
		fullClaimsNeeded bool
		mockFn           func(*mocks.L1InfoTreeDataQuerier)
		expectedCount    int
		expectedError    string
	}{
		{
			name:             "no claims, no unclaims",
			claims:           []bridgesync.Claim{},
			unclaims:         []bridgesynctypes.Unclaim{},
			fullClaimsNeeded: false,
			expectedCount:    0,
			expectedError:    "",
		},
		{
			name: "claims without unclaims - FullClaimsNeeded false",
			claims: []bridgesync.Claim{
				{
					BlockNum:      1,
					BlockPos:      0,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 1,
					OriginAddress: common.HexToAddress("0x123"),
					Amount:        big.NewInt(1000),
				},
				{
					BlockNum:      2,
					BlockPos:      1,
					GlobalIndex:   big.NewInt(200),
					OriginNetwork: 2,
					OriginAddress: common.HexToAddress("0x456"),
					Amount:        big.NewInt(2000),
				},
			},
			unclaims:         []bridgesynctypes.Unclaim{},
			fullClaimsNeeded: false,
			expectedCount:    2,
			expectedError:    "",
		},
		{
			name: "claims without unclaims - FullClaimsNeeded true",
			claims: []bridgesync.Claim{
				{
					BlockNum:            1,
					BlockPos:            0,
					GlobalIndex:         big.NewInt(100),
					OriginNetwork:       1,
					OriginAddress:       common.HexToAddress("0x123"),
					Amount:              big.NewInt(1000),
					GlobalExitRoot:      common.HexToHash("0xger1"),
					RollupExitRoot:      common.HexToHash("0xrer1"),
					MainnetExitRoot:     common.HexToHash("0xmer1"),
					ProofLocalExitRoot:  mockProof,
					ProofRollupExitRoot: mockProof,
				},
			},
			unclaims:         []bridgesynctypes.Unclaim{},
			fullClaimsNeeded: true,
			mockFn: func(mockL1InfoTreeQuery *mocks.L1InfoTreeDataQuerier) {
				mockL1InfoTreeQuery.EXPECT().GetProofForGER(ctx, common.HexToHash("0xger1"), rootFromWhichToProve).
					Return(
						&l1infotreesync.L1InfoTreeLeaf{
							L1InfoTreeIndex:   1,
							Timestamp:         123456789,
							PreviousBlockHash: common.HexToHash("0xabc"),
							GlobalExitRoot:    common.HexToHash("0xger1"),
						}, mockProof, nil)
			},
			expectedCount: 1,
			expectedError: "",
		},
		{
			name: "claims with unclaims canceling some claims",
			claims: []bridgesync.Claim{
				{
					BlockNum:      1,
					BlockPos:      0,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 1,
					Amount:        big.NewInt(1000),
				},
				{
					BlockNum:      2,
					BlockPos:      1,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 2,
					Amount:        big.NewInt(2000),
				},
				{
					BlockNum:      3,
					BlockPos:      2,
					GlobalIndex:   big.NewInt(200),
					OriginNetwork: 3,
					Amount:        big.NewInt(3000),
				},
			},
			unclaims: []bridgesynctypes.Unclaim{
				{GlobalIndex: big.NewInt(100), BlockNumber: 10, LogIndex: 0},
			},
			fullClaimsNeeded: false,
			expectedCount:    2, // one claim with GlobalIndex 100 remains, plus the one with 200
			expectedError:    "",
		},
		{
			name: "claims with unclaims canceling all claims of same GlobalIndex",
			claims: []bridgesync.Claim{
				{
					BlockNum:      1,
					BlockPos:      0,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 1,
					Amount:        big.NewInt(1000),
				},
				{
					BlockNum:      2,
					BlockPos:      1,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 2,
					Amount:        big.NewInt(2000),
				},
				{
					BlockNum:      3,
					BlockPos:      2,
					GlobalIndex:   big.NewInt(200),
					OriginNetwork: 3,
					Amount:        big.NewInt(3000),
				},
			},
			unclaims: []bridgesynctypes.Unclaim{
				{GlobalIndex: big.NewInt(100), BlockNumber: 10, LogIndex: 0},
				{GlobalIndex: big.NewInt(100), BlockNumber: 11, LogIndex: 1},
			},
			fullClaimsNeeded: false,
			expectedCount:    1, // only the claim with GlobalIndex 200 remains
			expectedError:    "",
		},
		{
			name: "more unclaims than claims for a GlobalIndex",
			claims: []bridgesync.Claim{
				{
					BlockNum:      1,
					BlockPos:      0,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 1,
					Amount:        big.NewInt(1000),
				},
			},
			unclaims: []bridgesynctypes.Unclaim{
				{GlobalIndex: big.NewInt(100), BlockNumber: 10, LogIndex: 0},
				{GlobalIndex: big.NewInt(100), BlockNumber: 11, LogIndex: 1},
				{GlobalIndex: big.NewInt(100), BlockNumber: 12, LogIndex: 2},
			},
			fullClaimsNeeded: false,
			expectedCount:    0,
			expectedError:    "",
		},
		{
			name: "multiple GlobalIndices with mixed cancellation",
			claims: []bridgesync.Claim{
				{
					BlockNum:      1,
					BlockPos:      0,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 1,
					Amount:        big.NewInt(1000),
				},
				{
					BlockNum:      2,
					BlockPos:      1,
					GlobalIndex:   big.NewInt(100),
					OriginNetwork: 2,
					Amount:        big.NewInt(2000),
				},
				{
					BlockNum:      3,
					BlockPos:      2,
					GlobalIndex:   big.NewInt(200),
					OriginNetwork: 3,
					Amount:        big.NewInt(3000),
				},
				{
					BlockNum:      4,
					BlockPos:      3,
					GlobalIndex:   big.NewInt(200),
					OriginNetwork: 4,
					Amount:        big.NewInt(4000),
				},
			},
			unclaims: []bridgesynctypes.Unclaim{
				{GlobalIndex: big.NewInt(100), BlockNumber: 10, LogIndex: 0},
				{GlobalIndex: big.NewInt(200), BlockNumber: 11, LogIndex: 1},
			},
			fullClaimsNeeded: false,
			expectedCount:    2, // one claim with 100, one claim with 200
			expectedError:    "",
		},
		{
			name: "using GenerateGlobalIndex helper",
			claims: []bridgesync.Claim{
				{
					BlockNum:      1,
					BlockPos:      0,
					GlobalIndex:   bridgesync.GenerateGlobalIndex(false, 1, 1),
					OriginNetwork: 1,
					Amount:        big.NewInt(1000),
				},
				{
					BlockNum:      2,
					BlockPos:      1,
					GlobalIndex:   bridgesync.GenerateGlobalIndex(false, 1, 1),
					OriginNetwork: 2,
					Amount:        big.NewInt(2000),
				},
			},
			unclaims: []bridgesynctypes.Unclaim{
				{GlobalIndex: bridgesync.GenerateGlobalIndex(false, 1, 1), BlockNumber: 10, LogIndex: 0},
			},
			fullClaimsNeeded: false,
			expectedCount:    1, // one claim remains after unclaim
			expectedError:    "",
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

			f := &baseFlow{
				cfg: BaseFlowConfig{
					FullClaimsNeeded: tt.fullClaimsNeeded,
				},
				l1InfoTreeDataQuerier: mockL1InfoTreeQuery,
				log:                   log.WithFields("test", t.Name()),
			}

			result, err := f.getImportedBridgeExits(ctx, tt.claims, tt.unclaims, rootFromWhichToProve)

			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tt.expectedCount, len(result))

				// Verify that the filtered claims are correct by checking GlobalIndex
				if !tt.fullClaimsNeeded && len(tt.claims) > 0 {
					// Count remaining claims by GlobalIndex
					remainingByIndex := make(map[string]int)
					for _, claim := range tt.claims {
						key := claim.GlobalIndex.String()
						remainingByIndex[key]++
					}
					for _, unclaim := range tt.unclaims {
						key := unclaim.GlobalIndex.String()
						if remainingByIndex[key] > 0 {
							remainingByIndex[key]--
						}
					}

					// Verify that result has correct count per GlobalIndex
					resultByIndex := make(map[string]int)
					for _, ibe := range result {
						if ibe != nil && ibe.GlobalIndex != nil {
							// Reconstruct the original GlobalIndex string representation
							mainnetFlag := ibe.GlobalIndex.MainnetFlag
							rollupIndex := ibe.GlobalIndex.RollupIndex
							leafIndex := ibe.GlobalIndex.LeafIndex
							reconstructed := bridgesync.GenerateGlobalIndex(mainnetFlag, rollupIndex, leafIndex)
							key := reconstructed.String()
							resultByIndex[key]++
						}
					}

					// Verify total count matches
					totalExpected := 0
					for _, count := range remainingByIndex {
						totalExpected += count
					}
					require.Equal(t, totalExpected, len(result), "Total filtered claims should match expected count")
				}
			}
		})
	}
}

func Test_baseFlow_adjustCertificateIfNonFinalizedClaims_UnclaimValidation(t *testing.T) {
	t.Parallel()

	// Helper to create a GER hash
	ger1 := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	ger2 := common.HexToHash("0x2222222222222222222222222222222222222222222222222222222222222222")
	ger3 := common.HexToHash("0x3333333333333333333333333333333333333333333333333333333333333333")
	ger4 := common.HexToHash("0x4444444444444444444444444444444444444444444444444444444444444444")
	ger5 := common.HexToHash("0x5555555555555555555555555555555555555555555555555555555555555555")

	// Helper to create GlobalIndex
	globalIndex1 := big.NewInt(100)
	globalIndex2 := big.NewInt(200)
	globalIndex3 := big.NewInt(300)
	globalIndex4 := big.NewInt(400)
	globalIndex5 := big.NewInt(500)

	// Helper functions for common mock patterns
	mockTwoClaimsGER1NotOnL1GER2OnL1 := func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier, l1InfoTreeLeafCount uint32) {
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
	}

	mockThreeClaimsGER1GER2NotOnL1GER3OnL1 := func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier, l1InfoTreeLeafCount uint32) {
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger3, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger3).Return(true, nil).Once()
	}

	mockFourClaimsGER1GER2GER3NotOnL1GER4OnL1 := func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier, l1InfoTreeLeafCount uint32) {
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger3, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger3).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger4, l1InfoTreeLeafCount).Return(false, nil).Once()
		mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger4).Return(true, nil).Once()
	}

	tests := []struct {
		name            string
		certParams      *types.CertificateBuildParams
		mockFn          func(*mocks.L1InfoTreeDataQuerier)
		expectedError   string
		expectedToBlock uint64
	}{
		{
			name: "valid: unclaim appears before claim with GER that exists on L1",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 10, // Unclaim appears BEFORE claim at block 15
					},
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockTwoClaimsGER1NotOnL1GER2OnL1(mockL1InfoTreeQuerier, uint32(10))
			},
			expectedToBlock: 14, // Adjusted to block 14 (15 - 1)
		},
		{
			name: "valid: no unclaim found for previous claim with unfinalized GER that doesn't exist on L1 - cert cut at block 4",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{}, // No unclaim for first claim
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				// First claim (block 5) - unfinalized, doesn't exist on L1
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
				// Second claim (block 15) - unfinalized, exists on L1
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
			},
			expectedToBlock: 4, // Cut at block 4 (5 - 1) because C1 can't be included
		},
		{
			name: "valid: all claims have finalized GERs",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1,
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2,
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: both claims have finalized GERs (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(true, nil).Once()
			},
			expectedToBlock: 20, // No adjustment needed
		},
		{
			name: "valid: claim with unfinalized GER that exists on L1, but no previous claims with non-existent GERs",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Finalized
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims
				// First claim - finalized
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(true, nil).Once()
				// Second claim - unfinalized, exists on L1
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
			},
			expectedToBlock: 14, // Adjusted to block 14 (15 - 1)
		},
		{
			name: "valid: multiple claims with unfinalized GERs not existing on L1, missing unclaims - cert cut at block 4",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       10,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 12, // Has unclaim
					},
					// Missing unclaim for globalIndex2
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger3, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger3).Return(true, nil).Once()
			},
			expectedToBlock: 4, // Cut at block 4 (5 - 1) because block 5's unclaim at 12 requires including block 10 which is unfinalized
		},
		{
			name: "valid: multiple claims with unfinalized GERs not existing on L1, all have unclaims before earliest existent GER",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       10,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 8, // Before block 15
					},
					{
						GlobalIndex: globalIndex2,
						BlockNumber: 12, // Before block 15
					},
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockThreeClaimsGER1GER2NotOnL1GER3OnL1(mockL1InfoTreeQuerier, uint32(10))
			},
			expectedToBlock: 14, // Adjusted to block 14 (15 - 1)
		},
		{
			name: "valid: one unclaim appears after claim with GER that exists on L1 - cert cut at block 9",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       10,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 8, // Before block 15 - valid
					},
					{
						GlobalIndex: globalIndex2,
						BlockNumber: 18, // After block 15 - but block 15 can't be included
					},
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockThreeClaimsGER1GER2NotOnL1GER3OnL1(mockL1InfoTreeQuerier, uint32(10))
			},
			expectedToBlock: 9, // Cut at block 9 (10 - 1) because block 10's unclaim at 18 requires including block 15 which is unfinalized
		},
		{
			name: "valid: complex scenario with 5 claims - mix of finalized, unfinalized existing, and unfinalized not existing",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             50,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Finalized
					},
					{
						BlockNum:       10,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Finalized
					},
					{
						BlockNum:       20,
						GlobalIndex:    globalIndex4,
						GlobalExitRoot: ger4, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       25,
						GlobalIndex:    globalIndex5,
						GlobalExitRoot: ger5, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex2,
						BlockNumber: 12, // Before block 25
					},
					{
						GlobalIndex: globalIndex4,
						BlockNumber: 22, // Before block 25
					},
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger3, uint32(10)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger4, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger4).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger5, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger5).Return(true, nil).Once()
			},
			expectedToBlock: 24, // Adjusted to block 24 (25 - 1)
		},
		{
			name: "valid: multiple claims with unfinalized GERs not existing on L1, one missing unclaim - cert cut at block 4",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       8,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       12,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       20,
						GlobalIndex:    globalIndex4,
						GlobalExitRoot: ger4, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 15, // Has unclaim
					},
					{
						GlobalIndex: globalIndex3,
						BlockNumber: 18, // Has unclaim
					},
					// Missing unclaim for globalIndex2
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockFourClaimsGER1GER2GER3NotOnL1GER4OnL1(mockL1InfoTreeQuerier, uint32(10))
			},
			expectedToBlock: 4, // Cut at block 4 (5 - 1) because block 5's unclaim at 15 requires including block 8 which is unfinalized
		},
		{
			name: "valid: unclaims at exact boundary for multiple claims",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       10,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 14, // Exactly at boundary
					},
					{
						GlobalIndex: globalIndex2,
						BlockNumber: 14, // Exactly at boundary
					},
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockThreeClaimsGER1GER2NotOnL1GER3OnL1(mockL1InfoTreeQuerier, uint32(10))
			},
			expectedToBlock: 14, // Create cert till 14 as 5 and 10 have unclaims before 15 which is valid
		},
		{
			name: "valid: unclaim appears at or after block with GER that exists on L1 - cert cut at block 4",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 15, // Exactly at block 15, but block 15 can't be included
					},
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
			},
			expectedToBlock: 4, // Cut at block 4 (5 - 1) because block 5's unclaim at 15 requires including block 15 which is unfinalized
		},
		{
			name: "valid: multiple unfinalized GERs existing on L1, earliest one triggers adjustment",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             40,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Finalized
					},
					{
						BlockNum:       10,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, exists on L1 (earliest)
					},
					{
						BlockNum:       20,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, exists on L1
					},
					{
						BlockNum:       30,
						GlobalIndex:    globalIndex4,
						GlobalExitRoot: ger4, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
			},
			expectedToBlock: 9, // Adjusted to block 9 (10 - 1) - earliest unfinalized existent GER
		},
		{
			name: "valid: multiple claims without unclaims, cert cut at earliest problematic claim",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             30,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1, has unclaim
					},
					{
						BlockNum:       8,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, doesn't exist on L1, no unclaim (earliest problematic)
					},
					{
						BlockNum:       12,
						GlobalIndex:    globalIndex3,
						GlobalExitRoot: ger3, // Unfinalized, doesn't exist on L1, no unclaim
					},
					{
						BlockNum:       20,
						GlobalIndex:    globalIndex4,
						GlobalExitRoot: ger4, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{
					{
						GlobalIndex: globalIndex1,
						BlockNumber: 7, // C1 has unclaim
					},
					// C2 and C3 have no unclaims
				},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				mockFourClaimsGER1GER2GER3NotOnL1GER4OnL1(mockL1InfoTreeQuerier, uint32(10))
			},
			expectedToBlock: 7, // Cut at block 7 (8 - 1) because C2 can't be included
		},
		{
			name: "invalid: claim at start block without unclaim, later claim with GER on L1 - cannot create cert",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       1, // At start block
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1, // Unfinalized, doesn't exist on L1, no unclaim
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2, // Unfinalized, exists on L1
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{}, // No unclaim for C1
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(true, nil).Once()
			},
			expectedError: "cannot create certificate: claim at block 1 (start block 1) cannot be included and no valid blocks before it",
		},
		{
			name: "invalid: error checking if GER is finalized",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1,
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(false, errors.New("some error")).Once()
			},
			expectedError: "error checking if GER",
		},
		{
			name: "invalid: error checking if GER exists on L1",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1,
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger1).Return(false, errors.New("some error")).Once()
			},
			expectedError: "error checking if GER",
		},
		{
			name: "invalid: error checking if GER is finalized for second claim",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1,
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2,
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, errors.New("some error")).Once()
			},
			expectedError: "error checking if GER",
		},
		{
			name: "invalid: error checking if GER exists on L1 for second claim",
			certParams: &types.CertificateBuildParams{
				FromBlock:           1,
				ToBlock:             20,
				L1InfoTreeLeafCount: 10,
				Claims: []bridgesync.Claim{
					{
						BlockNum:       5,
						GlobalIndex:    globalIndex1,
						GlobalExitRoot: ger1,
					},
					{
						BlockNum:       15,
						GlobalIndex:    globalIndex2,
						GlobalExitRoot: ger2,
					},
				},
				Unclaims: []bridgesynctypes.Unclaim{},
			},
			mockFn: func(mockL1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier) {
				// First pass: check all claims (cached results will be reused in subsequent calls)
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger1, uint32(10)).Return(true, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().IsGERFinalized(ger2, uint32(10)).Return(false, nil).Once()
				mockL1InfoTreeQuerier.EXPECT().DoesGERExistsOnL1(ger2).Return(false, errors.New("some error")).Once()
			},
			expectedError: "error checking if GER",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			if tc.mockFn != nil {
				tc.mockFn(mockL1InfoTreeQuerier)
			}

			f := &baseFlow{
				l1InfoTreeDataQuerier: mockL1InfoTreeQuerier,
				log:                   log.WithFields("test", t.Name()),
			}

			result, err := f.adjustCertificateIfNonFinalizedClaims(tc.certParams)

			if tc.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, tc.expectedToBlock, result.ToBlock)
			}

			mockL1InfoTreeQuerier.AssertExpectations(t)
		})
	}
}
