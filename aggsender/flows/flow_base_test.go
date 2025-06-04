package flows

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
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
			f := NewBaseFlow(
				log.WithFields("test", t.Name()),
				nil,
				nil,
				nil,
				NewBaseFlowConfig(tt.maxCertSize, 0))

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
