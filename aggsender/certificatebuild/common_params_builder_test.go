package certificatebuild

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
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func timeNowUTCForTest() uint32 {
	return uint32(12345)
}

func Test_GeneratePreBuildParams(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		certType       types.CertificateType
		mockFn         func(*mocks.BridgeQuerier, *mocks.AggSenderStorage, *mocks.L1InfoTreeDataQuerier)
		expectedParams *types.CertificatePreBuildParams
		expectedError  string
	}{
		{
			name:     "storage returns an error",
			certType: types.CertificateTypePP,
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockStorage *mocks.AggSenderStorage,
				mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, errors.New("storage error"))
			},
			expectedError: "error getting last sent certificate: storage error",
		},
		{
			name:     "get last processed block error",
			certType: types.CertificateTypePP,
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockStorage *mocks.AggSenderStorage,
				mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(t.Context()).Return(uint64(0), errors.New("querier error"))
			},
			expectedError: "error getting last processed block from l2: querier error",
		},
		{
			name:     "get latest finalized block error",
			certType: types.CertificateTypePP,
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockStorage *mocks.AggSenderStorage,
				mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(t.Context()).Return(uint64(100), nil)
				mockL1InfoTreeDataQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(t.Context()).Return(nil, nil, errors.New("querier error"))
			},
			expectedError: "error getting latest finalized L1 info root: querier error",
		},
		{
			name:     "success",
			certType: types.CertificateTypePP,
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier,
				mockStorage *mocks.AggSenderStorage,
				mockL1InfoTreeDataQuerier *mocks.L1InfoTreeDataQuerier) {
				mockStorage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil)
				mockL2BridgeQuerier.EXPECT().GetLastProcessedBlock(t.Context()).Return(uint64(100), nil)
				mockL1InfoTreeDataQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(t.Context()).Return(&treetypes.Root{
					Hash:     common.HexToHash("0x1"),
					Index:    1,
					BlockNum: 55,
				}, nil, nil)
			},
			expectedParams: &types.CertificatePreBuildParams{
				CertificateType:     types.CertificateTypePP,
				BlockRange:          types.NewBlockRange(1, 100),
				RetryCount:          0,
				LastSentCertificate: nil,
				L1InfoTreeToProve: &types.CertificateL1InfoTreeData{
					L1InfoTreeRootToProve: common.HexToHash("0x1"),
					L1InfoTreeLeafCount:   2,
				},
				CreatedAt: timeNowUTCForTest(),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL1InfoTreeQuerier := mocks.NewL1InfoTreeDataQuerier(t)
			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)
			mockStorage := mocks.NewAggSenderStorage(t)

			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeQuerier, mockStorage, mockL1InfoTreeQuerier)
			}

			builder := NewCommonParamsBuilder(
				log.WithFields("test", tc.name),
				mockStorage,
				mockL1InfoTreeQuerier,
				mockL2BridgeQuerier,
				nil, // lerQuerier
				NewCommonBuildConfig(0, 0, false),
			)
			bl, ok := builder.(*commonParamsBuilder)
			require.True(t, ok, "builder should be of type *commonParamsBuilder")
			bl.timeNowFunc = timeNowUTCForTest

			result, err := builder.GeneratePreBuildParams(t.Context(), tc.certType)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, result)
			}

			mockL1InfoTreeQuerier.AssertExpectations(t)
			mockL2BridgeQuerier.AssertExpectations(t)
			mockStorage.AssertExpectations(t)
		})
	}
}

func Test_GenerateBuildParams(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		preParams      types.CertificatePreBuildParams
		mockFn         func(*mocks.BridgeQuerier)
		expectedParams *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name: "L1InfoTreeToProve is nil",
			preParams: types.CertificatePreBuildParams{
				BlockRange:          types.NewBlockRange(1, 10),
				RetryCount:          2,
				LastSentCertificate: &types.CertificateHeader{Height: 5},
				CertificateType:     types.CertificateTypePP,
				L1InfoTreeToProve:   nil,
				CreatedAt:           timeNowUTCForTest(),
			},
			expectedError: "L1InfoTreeWhichToProve should be not nil for GenerateBuildParams",
		},
		{
			name: "GetBridgesAndClaims returns error",
			preParams: types.CertificatePreBuildParams{
				BlockRange:          types.NewBlockRange(1, 10),
				RetryCount:          2,
				LastSentCertificate: &types.CertificateHeader{Height: 5},
				CertificateType:     types.CertificateTypePP,
				L1InfoTreeToProve: &types.CertificateL1InfoTreeData{
					L1InfoTreeRootToProve: common.HexToHash("0xabc"),
					L1InfoTreeLeafCount:   42,
				},
				CreatedAt: timeNowUTCForTest(),
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(t.Context(), uint64(1), uint64(10)).
					Return(nil, nil, errors.New("bridge error"))
			},
			expectedError: "generateBulidParams fails getting bridges and claims. Err: bridge error",
		},
		{
			name: "Success",
			preParams: types.CertificatePreBuildParams{
				BlockRange:          types.NewBlockRange(1, 10),
				RetryCount:          2,
				LastSentCertificate: &types.CertificateHeader{Height: 5},
				CertificateType:     types.CertificateTypePP,
				L1InfoTreeToProve: &types.CertificateL1InfoTreeData{
					L1InfoTreeRootToProve: common.HexToHash("0xabc"),
					L1InfoTreeLeafCount:   42,
				},
				CreatedAt: timeNowUTCForTest(),
			},
			mockFn: func(mockL2BridgeQuerier *mocks.BridgeQuerier) {
				mockL2BridgeQuerier.EXPECT().
					GetBridgesAndClaims(t.Context(), uint64(1), uint64(10)).
					Return(
						[]bridgesync.Bridge{{BlockNum: 2}, {BlockNum: 3}},
						[]bridgesync.Claim{{BlockNum: 4}},
						nil,
					)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				RetryCount:                     2,
				LastSentCertificate:            &types.CertificateHeader{Height: 5},
				Bridges:                        []bridgesync.Bridge{{BlockNum: 2}, {BlockNum: 3}},
				Claims:                         []bridgesync.Claim{{BlockNum: 4}},
				CreatedAt:                      timeNowUTCForTest(),
				CertificateType:                types.CertificateTypePP,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0xabc"),
				L1InfoTreeLeafCount:            42,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockL2BridgeQuerier := mocks.NewBridgeQuerier(t)

			if tc.mockFn != nil {
				tc.mockFn(mockL2BridgeQuerier)
			}

			builder := NewCommonParamsBuilder(
				log.WithFields("test", tc.name),
				nil, // storage
				nil, // l1InfoTreeDataQuerier
				mockL2BridgeQuerier,
				nil, // lerQuerier
				NewCommonBuildConfig(0, 0, false),
			)
			bl, ok := builder.(*commonParamsBuilder)
			require.True(t, ok, "builder should be of type *commonParamsBuilder")
			bl.timeNowFunc = timeNowUTCForTest

			result, err := builder.GenerateBuildParams(t.Context(), tc.preParams)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedParams, result)
			}

			mockL2BridgeQuerier.AssertExpectations(t)
		})
	}
}

func Test_LimitCertSize(t *testing.T) {
	t.Parallel()

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
			t.Parallel()

			builder := NewCommonParamsBuilder(
				log.WithFields("test", tt.name),
				nil, // storage
				nil, // l1InfoTreeDataQuerier
				nil, // l2BridgeQuerier
				nil, // lerQuerier
				NewCommonBuildConfig(tt.maxCertSize, 0, false),
			)

			result, err := builder.LimitCertSize(tt.fullCert)

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

func Test_GetNewLocalExitRoot(t *testing.T) {
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

			builder := &commonParamsBuilder{
				l2BridgeQuerier: mockL2BridgeQuerier,
				storage:         mockStorage,
			}

			ctx := context.TODO()
			result, err := builder.GetNewLocalExitRoot(ctx, tt.certParams)

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

func Test_GetCommonCertificateBuildParams(t *testing.T) {
	t.Parallel()

	type mocksSetup struct {
		storage           *mocks.AggSenderStorage
		l2BridgeQuerier   *mocks.BridgeQuerier
		l1InfoTreeQuerier *mocks.L1InfoTreeDataQuerier
		lerQuerier        *mocks.LERQuerier
	}

	newMocks := func(t *testing.T) mocksSetup {
		t.Helper()

		return mocksSetup{
			storage:           mocks.NewAggSenderStorage(t),
			l2BridgeQuerier:   mocks.NewBridgeQuerier(t),
			l1InfoTreeQuerier: mocks.NewL1InfoTreeDataQuerier(t),
			lerQuerier:        mocks.NewLERQuerier(t),
		}
	}

	tests := []struct {
		name           string
		certType       types.CertificateType
		cfg            CommonBuildConfig
		setupMocks     func(m mocksSetup)
		expectedError  string
		expectedParams *types.CertificateBuildParams
	}{
		{
			name:     "error from GeneratePreBuildParams propagates",
			certType: types.CertificateTypePP,
			setupMocks: func(m mocksSetup) {
				m.storage.EXPECT().GetLastSentCertificateHeader().Return(nil, errors.New("storage error"))
			},
			expectedError: "error generating pre build params: error getting last sent certificate: storage error",
		},
		{
			name:     "error from GenerateBuildParams propagates",
			certType: types.CertificateTypePP,
			setupMocks: func(m mocksSetup) {
				m.storage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil)
				m.l2BridgeQuerier.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(10), nil)
				m.l1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(&treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 1,
				}, nil, nil)
				m.l2BridgeQuerier.EXPECT().GetBridgesAndClaims(mock.Anything, uint64(1), uint64(10)).
					Return(nil, nil, errors.New("bridge error"))
			},
			expectedError: "error generating build params: generateBulidParams fails getting bridges and claims. Err: bridge error",
		},
		{
			name:     "success returns build params",
			certType: types.CertificateTypePP,
			setupMocks: func(m mocksSetup) {
				m.storage.EXPECT().GetLastSentCertificateHeader().Return(nil, nil)
				m.l2BridgeQuerier.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(10), nil)
				m.l1InfoTreeQuerier.EXPECT().GetLatestFinalizedL1InfoRoot(mock.Anything).Return(&treetypes.Root{
					Hash:  common.HexToHash("0x1"),
					Index: 1,
				}, nil, nil)
				m.l2BridgeQuerier.EXPECT().GetBridgesAndClaims(mock.Anything, uint64(1), uint64(10)).
					Return([]bridgesync.Bridge{{BlockNum: 2}, {BlockNum: 3}}, []bridgesync.Claim{{BlockNum: 4}}, nil)
			},
			expectedParams: &types.CertificateBuildParams{
				FromBlock:                      1,
				ToBlock:                        10,
				RetryCount:                     0,
				LastSentCertificate:            nil,
				Bridges:                        []bridgesync.Bridge{{BlockNum: 2}, {BlockNum: 3}},
				Claims:                         []bridgesync.Claim{{BlockNum: 4}},
				CreatedAt:                      timeNowUTCForTest(),
				CertificateType:                types.CertificateTypePP,
				L1InfoTreeRootFromWhichToProve: common.HexToHash("0x1"),
				L1InfoTreeLeafCount:            2,
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := newMocks(t)
			if tt.setupMocks != nil {
				tt.setupMocks(m)
			}
			cfg := tt.cfg
			if cfg == (CommonBuildConfig{}) {
				cfg = NewCommonBuildConfigDefault()
			}
			builder := NewCommonParamsBuilder(
				log.WithFields("test", tt.name),
				m.storage,
				m.l1InfoTreeQuerier,
				m.l2BridgeQuerier,
				m.lerQuerier,
				cfg,
			)
			bl, ok := builder.(*commonParamsBuilder)
			require.True(t, ok, "builder should be of type *commonParamsBuilder")
			bl.timeNowFunc = timeNowUTCForTest

			result, err := builder.GetCommonCertificateBuildParams(context.Background(), tt.certType)
			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedParams, result)
			}
			m.storage.AssertExpectations(t)
			m.l2BridgeQuerier.AssertExpectations(t)
			m.l1InfoTreeQuerier.AssertExpectations(t)
			if m.lerQuerier != nil {
				m.lerQuerier.AssertExpectations(t)
			}
		})
	}
}

func Test_getNewLocalExitRootForParams(t *testing.T) {
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

			builder := &commonParamsBuilder{
				l2BridgeQuerier: mockL2BridgeQuerier,
			}

			result, err := builder.getNewLocalExitRootForParams(context.Background(), tt.certParams, tt.previousLER)

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

func Test_getNextHeightAndPreviousLER(t *testing.T) {
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
			expectedLER:    EmptyLER,
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
			expectedLER:    EmptyLER,
			mockFn: func(mockLERQuerier *mocks.LERQuerier, mockStorage *mocks.AggSenderStorage) {
				mockLERQuerier.EXPECT().GetLastLocalExitRoot().Return(EmptyLER, nil)
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
			builder := &commonParamsBuilder{
				lerQuerier: mockLERQuerier,
				storage:    mockStorage,
				log:        log,
			}

			height, ler, err := builder.getNextHeightAndPreviousLER(tc.lastSentCert)
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
