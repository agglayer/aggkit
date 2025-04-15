package flows

import (
	"context"
	"fmt"
	"testing"

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
	tests := []struct {
		name          string
		index         uint32
		toBlock       uint64
		previousLER   common.Hash
		mockExitRoot  *treetypes.Root
		mockError     error
		expectedLER   common.Hash
		expectedError string
	}{
		{
			name:        "successful retrieval of new LER",
			index:       1,
			toBlock:     100,
			previousLER: common.HexToHash("0x123"),
			mockExitRoot: &treetypes.Root{
				Hash: common.HexToHash("0x456"),
			},
			expectedLER: common.HexToHash("0x456"),
		},
		{
			name:          "error retrieving exit root",
			index:         1,
			toBlock:       100,
			previousLER:   common.HexToHash("0x123"),
			mockError:     fmt.Errorf("mock error"),
			expectedError: "error getting exit root by index: 0. Error: mock error",
		},
		{
			name:         "no exit root, return previous LER",
			index:        1,
			toBlock:      100,
			previousLER:  common.HexToHash("0x123"),
			mockExitRoot: nil,
			mockError:    db.ErrNotFound,
			expectedLER:  common.HexToHash("0x123"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockL2Syncer := mocks.NewL2BridgeSyncer(t)
			mockL2Syncer.EXPECT().GetExitRootByIndexAndBlockNumber(mock.Anything, tt.index, tt.toBlock).
				Return(tt.mockExitRoot, tt.mockError)

			f := &baseFlow{
				l2Syncer: mockL2Syncer,
			}

			result, err := f.getNewLocalExitRoot(context.Background(), tt.index, tt.toBlock, tt.previousLER)

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
