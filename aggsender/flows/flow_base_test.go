package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
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
				NewBaseFlowConfig(tt.maxCertSize, 0, false))

			result, err := f.limitCertSize(tt.fullCert)

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
			expectedLER:    emptyLER,
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(aggkitcommon.ZeroHash, nil)
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
			expectedError:  "error getting last local exit root: some error",
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
			expectedLER:    emptyLER,
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(emptyLER, nil)
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
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
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
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
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
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
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
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
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
					GetBridgesAndClaims(ctx, uint64(16), uint64(16)).
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
					GetBridgesAndClaims(ctx, uint64(5), uint64(6)).
					Return([]bridgesync.Bridge{}, []bridgesync.Claim{}, nil)
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
