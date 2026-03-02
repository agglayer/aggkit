package query

import (
	"errors"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/aggsender/mocks"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetLastLocalExitRoot(t *testing.T) {
	testCases := []struct {
		name          string
		mockFn        func(*mocks.RollupDataQuerier)
		expectedLER   common.Hash
		expectedError string
	}{
		{
			name: "rollup manager contract returns error",
			mockFn: func(rdq *mocks.RollupDataQuerier) {
				rdq.EXPECT().GetRollupData(mock.Anything).
					Return(agglayermanager.AgglayerManagerRollupDataReturn{}, errors.New("some error"))
			},
			expectedLER:   aggkitcommon.ZeroHash,
			expectedError: "failed to get rollup data: some error",
		},
		{
			name: "GetLastLocalExitRoot returns zero hash - should return EmptyLER",
			mockFn: func(rdq *mocks.RollupDataQuerier) {
				rdq.EXPECT().GetRollupData(mock.Anything).
					Return(agglayermanager.AgglayerManagerRollupDataReturn{
						LastLocalExitRoot: aggkitcommon.ZeroHash,
					}, nil)
			},
			expectedLER: bridgesynctypes.EmptyLER,
		},
		{
			name: "rollup manager contract returns valid data",
			mockFn: func(rdq *mocks.RollupDataQuerier) {
				rdq.EXPECT().GetRollupData(mock.Anything).
					Return(agglayermanager.AgglayerManagerRollupDataReturn{
						LastLocalExitRoot: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
					}, nil)
			},
			expectedLER: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockRollupQuerier := mocks.NewRollupDataQuerier(t)

			if tc.mockFn != nil {
				tc.mockFn(mockRollupQuerier)
			}

			querier := NewLERDataQuerier(0, mockRollupQuerier)

			result, err := querier.GetLastLocalExitRoot()
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedLER, result)
			}
		})
	}
}
