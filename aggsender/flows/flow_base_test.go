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
				NewBaseFlowConfig(tt.maxCertSize, 0))

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
