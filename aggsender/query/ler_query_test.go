package query

import (
	"errors"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonrollupmanager"
	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	aggkittypes "github.com/agglayer/aggkit/types"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetLastLocalExitRoot(t *testing.T) {
	testCases := []struct {
		name          string
		mockFn        func(*mocks.RollupManagerContract)
		expectedLER   common.Hash
		expectedError string
	}{
		{
			name: "rollup manager contract returns error",
			mockFn: func(rmc *mocks.RollupManagerContract) {
				rmc.EXPECT().RollupIDToRollupData(mock.Anything, mock.Anything).
					Return(polygonrollupmanager.PolygonRollupManagerRollupDataReturn{}, errors.New("some error"))
			},
			expectedLER:   aggkitcommon.ZeroHash,
			expectedError: "failed to get rollup data: some error",
		},
		{
			name: "rollup manager contract returns valid data",
			mockFn: func(rmc *mocks.RollupManagerContract) {
				rmc.EXPECT().RollupIDToRollupData(mock.Anything, mock.Anything).
					Return(polygonrollupmanager.PolygonRollupManagerRollupDataReturn{
						LastLocalExitRoot: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
					}, nil)
			},
			expectedLER: common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockL1Client := &aggkittypesmocks.BaseEthereumClienter{}
			mockRollupManagerContract := &mocks.RollupManagerContract{}

			if tc.mockFn != nil {
				tc.mockFn(mockRollupManagerContract)
			}

			funcCreateRollupManagerContract = func(
				_ common.Address,
				_ aggkittypes.BaseEthereumClienter) (types.RollupManagerContract, error) {
				return mockRollupManagerContract, nil
			}

			querier, err := NewLERDataQuerier(common.Address{}, 0, 0, mockL1Client)
			require.NoError(t, err)

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

func TestNewLERDataQuerier(t *testing.T) {
	mockL1Client := &aggkittypesmocks.BaseEthereumClienter{}
	mockRollupManagerContract := &mocks.RollupManagerContract{}

	testCases := []struct {
		name   string
		mockFn func(
			rollupManagerAddr common.Address,
			l1Client aggkittypes.BaseEthereumClienter) (types.RollupManagerContract, error)
		expectedError string
	}{
		{
			name: "successful creation of LERDataQuerier",
			mockFn: func(
				_ common.Address,
				_ aggkittypes.BaseEthereumClienter) (types.RollupManagerContract, error) {
				return mockRollupManagerContract, nil
			},
			expectedError: "",
		},
		{
			name: "error creating RollupManager contract",
			mockFn: func(
				_ common.Address,
				_ aggkittypes.BaseEthereumClienter) (types.RollupManagerContract, error) {
				return nil, errors.New("some error")
			},
			expectedError: "failed to create PolygonRollupManager contract caller: some error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			funcCreateRollupManagerContract = tc.mockFn
			_, err := NewLERDataQuerier(common.Address{}, 0, 0, mockL1Client)

			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.expectedError)
			}
		})
	}
}
