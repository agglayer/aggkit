package flows

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_baseFlow_limitCertSize(t *testing.T) {
	tests := []struct {
		name           string
		maxCertSize    uint
		fullCert       *types.CertificateBuildParams
		allowEmptyCert bool
		expectedCert   *types.CertificateBuildParams
		expectedError  string
	}{
		{
			name:        "certificate size within limit",
			maxCertSize: 1000,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
			},
			allowEmptyCert: false,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{}, {}},
			},
		},
		{
			name:        "certificate size exceeds limit",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{{BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}, {BlockNum: 10}},
			},
			allowEmptyCert: false,
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
			allowEmptyCert: false,
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
			allowEmptyCert: true,
			expectedCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
		},
		{
			name:        "empty certificate not allowed",
			maxCertSize: 500,
			fullCert: &types.CertificateBuildParams{
				FromBlock: 1,
				ToBlock:   10,
				Bridges:   []bridgesync.Bridge{},
			},
			allowEmptyCert: false,
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
			allowEmptyCert: false,
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
			f := &baseFlow{
				maxCertSize: tt.maxCertSize,
				log:         log.WithFields("test", t.Name()),
			}

			result, err := f.limitCertSize(tt.fullCert, tt.allowEmptyCert)

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
		mockFn          func(mockL2Syncer *mocks.L2BridgeSyncer)
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
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(treetypes.Root{Hash: common.HexToHash("0x456")}, nil)
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
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(treetypes.Root{}, db.ErrNotFound)
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
			mockFn: func(mockL2Syncer *mocks.L2BridgeSyncer) {
				mockL2Syncer.EXPECT().GetExitRootByIndex(mock.Anything, mock.Anything).
					Return(treetypes.Root{}, errors.New("unexpected error"))
			},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			if tt.mockFn != nil {
				tt.mockFn(mockL2Syncer)
			}

			f := &baseFlow{
				l2Syncer: mockL2Syncer,
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
func Test_baseFlow_getFromBlockAndRetryCount(t *testing.T) {
	tests := []struct {
		name                    string
		startL2Block            uint64
		lastSentCertificateInfo *types.CertificateInfo
		expectedFromBlock       uint64
		expectedRetryCount      int
		expectedError           string
	}{
		{
			name:                    "no previous certificate, start from startL2Block",
			startL2Block:            100,
			lastSentCertificateInfo: nil,
			expectedFromBlock:       100,
			expectedRetryCount:      0,
		},
		{
			name:         "last certificate not in error, use ToBlock",
			startL2Block: 100,
			lastSentCertificateInfo: &types.CertificateInfo{
				ToBlock: 150,
				Status:  agglayertypes.Settled,
			},
			expectedFromBlock:  151,
			expectedRetryCount: 0,
		},
		{
			name:         "last certificate in error, resend from previous block",
			startL2Block: 100,
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  120,
				ToBlock:    150,
				Status:     agglayertypes.InError,
				RetryCount: 2,
			},
			expectedFromBlock:  120,
			expectedRetryCount: 3,
		},
		{
			name:         "last certificate in error, FromBlock is 0",
			startL2Block: 100,
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  0,
				ToBlock:    150,
				Status:     agglayertypes.InError,
				RetryCount: 1,
			},
			expectedFromBlock:  0,
			expectedRetryCount: 2,
		},
		{
			name:         "last certificate candidate, no close cert",
			startL2Block: 123,
			lastSentCertificateInfo: &types.CertificateInfo{
				FromBlock:  125,
				ToBlock:    150,
				Status:     agglayertypes.Candidate,
				RetryCount: 1,
			},
			expectedFromBlock:  0,
			expectedRetryCount: 2,
			expectedError:      "not closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &baseFlow{
				startL2Block: tt.startL2Block,
			}

			lastSentBlock, retryCount, err := f.getFromBlockAndRetryCount(tt.lastSentCertificateInfo)
			if tt.expectedError != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedFromBlock, lastSentBlock, tt.name)
				require.Equal(t, tt.expectedRetryCount, retryCount, tt.name)
			}
		})
	}
}
