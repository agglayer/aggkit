package query

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestGetAggchainParams(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                   string
		mockFn                 func(*mocks.FEPContractQuerier, *mocks.AggProofPublicValuesQuerier)
		expectedAggchainParams *types.AggchainParams
		expectError            string
	}{
		{
			name: "success",
			mockFn: func(mockFEPContractQuery *mocks.FEPContractQuerier, mockPublicValuesQuery *mocks.AggProofPublicValuesQuerier) {
				mockPublicValuesQuery.EXPECT().GetAggregationProofPublicValuesData(uint64(1), uint64(10), common.HexToHash("0x1")).
					Return(&types.AggregationProofPublicValues{
						L1Head:              common.HexToHash("0x1"),
						L2PreRoot:           common.HexToHash("0x2"),
						ClaimRoot:           common.HexToHash("0x3"),
						L2BlockNumber:       10,
						RollupConfigHash:    common.HexToHash("0x4"),
						MultiBlockVKey:      common.HexToHash("0x5"),
						AggregationVKeyHash: common.HexToHash("0x6"),
						TrustedSigner:       common.HexToAddress("0x2"),
					}, nil)

				mockFEPContractQuery.EXPECT().OptimisticMode((*bind.CallOpts)(nil)).Return(false, nil)
			},
			expectedAggchainParams: &types.AggchainParams{
				AggregationProofPublicValues: types.AggregationProofPublicValues{
					L1Head:              common.HexToHash("0x1"),
					L2PreRoot:           common.HexToHash("0x2"),
					ClaimRoot:           common.HexToHash("0x3"),
					L2BlockNumber:       10,
					RollupConfigHash:    common.HexToHash("0x4"),
					MultiBlockVKey:      common.HexToHash("0x5"),
					AggregationVKeyHash: common.HexToHash("0x6"),
					TrustedSigner:       common.HexToAddress("0x2"),
				},
				OptimisticMode: false,
			},
		},
		{
			name: "fail to get public inputs",
			mockFn: func(mockFEPContractQuery *mocks.FEPContractQuerier, mockPublicValuesQuery *mocks.AggProofPublicValuesQuerier) {
				mockPublicValuesQuery.EXPECT().GetAggregationProofPublicValuesData(uint64(1), uint64(10), common.HexToHash("0x1")).
					Return(nil, errors.New("test error"))
			},
			expectError: "failed to get FEP public inputs: test error",
		},
		{
			name: "fail to check optimistic mode",
			mockFn: func(mockFEPContractQuery *mocks.FEPContractQuerier, mockPublicValuesQuery *mocks.AggProofPublicValuesQuerier) {
				mockPublicValuesQuery.EXPECT().GetAggregationProofPublicValuesData(uint64(1), uint64(10), common.HexToHash("0x1")).
					Return(&types.AggregationProofPublicValues{
						L1Head:              common.HexToHash("0x1"),
						L2PreRoot:           common.HexToHash("0x2"),
						ClaimRoot:           common.HexToHash("0x3"),
						L2BlockNumber:       10,
						RollupConfigHash:    common.HexToHash("0x4"),
						MultiBlockVKey:      common.HexToHash("0x5"),
						AggregationVKeyHash: common.HexToHash("0x6"),
						TrustedSigner:       common.HexToAddress("0x2"),
					}, nil)

				mockFEPContractQuery.EXPECT().OptimisticMode((*bind.CallOpts)(nil)).Return(false, errors.New("test error"))
			},
			expectError: "failed to check if optimistic mode is turned on: test error",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockFEPContractQuery := mocks.NewFEPContractQuerier(t)
			mockPublicValuesQuery := mocks.NewAggProofPublicValuesQuerier(t)

			if tc.mockFn != nil {
				tc.mockFn(mockFEPContractQuery, mockPublicValuesQuery)
			}

			fepInputsQuery := &FEPInputsQuery{
				publicValuesQuery:   mockPublicValuesQuery,
				aggchainFEPContract: mockFEPContractQuery,
			}

			aggchainParams, err := fepInputsQuery.GetAggchainParams(1, 10, common.HexToHash("0x1"))
			if tc.expectError != "" {
				require.ErrorContains(t, err, tc.expectError)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedAggchainParams, aggchainParams)
			}

			mockFEPContractQuery.AssertExpectations(t)
			mockPublicValuesQuery.AssertExpectations(t)
		})
	}
}
